package copilot

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// Adaptor supports two modes:
//  1. Direct Copilot API mode: exchange GitHub OAuth token for a Copilot token.
//  2. Proxy mode: forward requests to an external copilot-api compatible service
//     via ChannelBaseUrl, letting that service handle Copilot auth and protocol details.
type Adaptor struct {
	openai.Adaptor
}

const upstreamGitHubTokenHeader = "X-GitHub-Token"

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	a.Adaptor.Init(info)
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if useProxyMode(info) {
		return a.Adaptor.GetRequestURL(info)
	}
	return fmt.Sprintf("%s/chat/completions", CopilotAPIEndpoint), nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	if useProxyMode(info) {
		channel.SetupApiRequestHeader(info, c, header)
		header.Del("Authorization")
		header.Set(upstreamGitHubTokenHeader, info.ApiKey)
		return nil
	}

	channel.SetupApiRequestHeader(info, c, header)

	token, err := GetCopilotToken(info.ApiKey)
	if err != nil {
		return fmt.Errorf("failed to get copilot token: %w", err)
	}

	header.Set("Authorization", "Bearer "+token)
	header.Set("Accept", "application/json")
	header.Set("User-Agent", copilotUserAgent)
	header.Set("Editor-Version", editorVersion)
	header.Set("Editor-Plugin-Version", editorPlugin)
	header.Set("Copilot-Integration-Id", integrationID)
	header.Set("Openai-Intent", "conversation-panel")
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	return a.Adaptor.ConvertOpenAIRequest(c, info, request)
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	return a.Adaptor.ConvertClaudeRequest(c, info, request)
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	return a.Adaptor.DoResponse(c, resp, info)
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}

func useProxyMode(info *relaycommon.RelayInfo) bool {
	if info == nil {
		return false
	}
	baseURL := strings.TrimSpace(info.ChannelBaseUrl)
	if baseURL == "" {
		return false
	}
	return !strings.Contains(baseURL, "githubcopilot.com")
}
