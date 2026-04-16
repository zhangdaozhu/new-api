package copilot

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	claudechannel "github.com/QuantumNous/new-api/relay/channel/claude"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// Adaptor supports two modes:
//  1. Direct Copilot API mode: exchange GitHub OAuth token for a Copilot token.
//  2. Proxy mode: forward requests to an external copilot-api compatible service
//     via ChannelBaseUrl, letting that service handle Copilot auth and protocol details.
type Adaptor struct {
	openai.Adaptor
	claudeAdaptor claudechannel.Adaptor
}

const upstreamGitHubTokenHeader = "X-GitHub-Token"
const useResponsesCompatKey = "copilot_use_responses_compat"

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	a.Adaptor.Init(info)
	a.claudeAdaptor.Init(info)
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if useProxyMode(info) {
		return relaycommon.GetFullRequestURL(info.ChannelBaseUrl, info.RequestURLPath, info.ChannelType), nil
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
	if useProxyMode(info) {
		if shouldUseResponsesForChatRequest(info, request) {
			applySystemPromptIfNeeded(info, request)
			responsesReq, err := service.ChatCompletionsRequestToResponsesRequest(request)
			if err != nil {
				return nil, err
			}
			stripUnsupportedResponsesParams(responsesReq)
			info.RequestURLPath = "/v1/responses"
			info.RelayMode = relayconstant.RelayModeResponses
			info.FinalRequestRelayFormat = types.RelayFormatOpenAIResponses
			c.Set(useResponsesCompatKey, true)
			return responsesReq, nil
		}
		return request, nil
	}
	return a.Adaptor.ConvertOpenAIRequest(c, info, request)
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	if useProxyMode(info) {
		return request, nil
	}
	return a.Adaptor.ConvertClaudeRequest(c, info, request)
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	if useProxyMode(info) {
		return request, nil
	}
	return a.Adaptor.ConvertOpenAIResponsesRequest(c, info, request)
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if useProxyMode(info) && c.GetBool(useResponsesCompatKey) {
		if info.IsStream || strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			return openai.OaiResponsesToChatStreamHandler(c, info, resp)
		}
		return openai.OaiResponsesToChatHandler(c, info, resp)
	}
	if useProxyMode(info) && info.RelayFormat == types.RelayFormatClaude {
		return a.claudeAdaptor.DoResponse(c, resp, info)
	}
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

func shouldUseResponsesForChatRequest(info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) bool {
	if info == nil || request == nil {
		return false
	}
	if info.RequestURLPath != "/v1/chat/completions" {
		return false
	}
	modelName := request.Model
	if modelName == "" {
		modelName = info.UpstreamModelName
	}
	modelName = strings.ToLower(modelName)
	return strings.HasPrefix(modelName, "gpt-5") || strings.Contains(modelName, "codex")
}

func applySystemPromptIfNeeded(info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) {
	if info == nil || request == nil || info.ChannelSetting.SystemPrompt == "" {
		return
	}
	systemRole := request.GetSystemRoleName()
	for i, message := range request.Messages {
		if message.Role != systemRole {
			continue
		}
		if !info.ChannelSetting.SystemPromptOverride {
			return
		}
		if message.IsStringContent() {
			request.Messages[i].SetStringContent(info.ChannelSetting.SystemPrompt + "\n" + message.StringContent())
			return
		}
		contents := message.ParseContent()
		contents = append([]dto.MediaContent{{
			Type: dto.ContentTypeText,
			Text: info.ChannelSetting.SystemPrompt,
		}}, contents...)
		request.Messages[i].Content = contents
		return
	}
	request.Messages = append([]dto.Message{{
		Role:    systemRole,
		Content: info.ChannelSetting.SystemPrompt,
	}}, request.Messages...)
}

func stripUnsupportedResponsesParams(request *dto.OpenAIResponsesRequest) {
	if request == nil {
		return
	}
	request.Temperature = nil
	request.TopP = nil
}
