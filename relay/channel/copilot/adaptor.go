package copilot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	copilot "github.com/github/copilot-sdk/go"
)

type Adaptor struct {
	request *dto.GeneralOpenAIRequest
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return "", nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	a.request = request
	return request, nil
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	openaiRequest, err := service.ClaudeToOpenAIRequest(*request, info)
	if err != nil {
		return nil, err
	}
	a.request = openaiRequest
	return openaiRequest, nil
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertRerankRequest(*gin.Context, int, dto.RerankRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertEmbeddingRequest(*gin.Context, *relaycommon.RelayInfo, dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertAudioRequest(*gin.Context, *relaycommon.RelayInfo, dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertImageRequest(*gin.Context, *relaycommon.RelayInfo, dto.ImageRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(*gin.Context, *relaycommon.RelayInfo, dto.OpenAIResponsesRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	// Like xunfei, copilot uses SDK (not HTTP), so return a dummy response
	dummyResp := &http.Response{}
	dummyResp.StatusCode = http.StatusOK
	return dummyResp, nil
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if a.request == nil {
		return nil, types.NewError(errors.New("request is nil"), types.ErrorCodeInvalidRequest)
	}

	ctx := c.Request.Context()

	client, clientErr := getClient(ctx, info.ApiKey)
	if clientErr != nil {
		return nil, types.NewError(clientErr, types.ErrorCodeDoRequestFailed)
	}

	// Build prompt from OpenAI messages
	prompt := messagesToPrompt(a.request.Messages)

	// Build system message from system messages
	systemMsg := extractSystemMessage(a.request.Messages)

	// Create session with the requested model
	sessionConfig := &copilot.SessionConfig{
		Model:               info.UpstreamModelName,
		Streaming:           info.IsStream,
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
		ClientName:          "new-api/1.0",
	}

	if systemMsg != "" {
		sessionConfig.SystemMessage = &copilot.SystemMessageConfig{
			Mode: "replace",
			Sections: map[string]copilot.SectionOverride{
				copilot.SectionGuidelines: {
					Action:  copilot.SectionActionReplace,
					Content: systemMsg,
				},
			},
		}
	}

	session, sessionErr := client.CreateSession(ctx, sessionConfig)
	if sessionErr != nil {
		return nil, types.NewError(fmt.Errorf("failed to create session: %w", sessionErr), types.ErrorCodeDoRequestFailed)
	}
	defer session.Disconnect()

	if info.IsStream {
		return a.handleStream(c, session, ctx, prompt, info)
	}
	return a.handleNonStream(c, session, ctx, prompt, info)
}

func (a *Adaptor) handleStream(c *gin.Context, session *copilot.Session, ctx context.Context, prompt string, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	helper.SetEventStreamHeaders(c)

	var usage dto.Usage
	responseID := fmt.Sprintf("chatcmpl-copilot-%d", time.Now().UnixNano())
	created := time.Now().Unix()
	model := info.UpstreamModelName

	doneCh := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	var mu sync.Mutex
	var finished bool

	unsubscribe := session.On(func(event copilot.SessionEvent) {
		mu.Lock()
		defer mu.Unlock()
		if finished {
			return
		}

		switch event.Type {
		case copilot.SessionEventTypeAssistantMessageDelta:
			if event.Data.DeltaContent != nil {
				chunk := &dto.ChatCompletionsStreamResponse{
					Id:      responseID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   model,
					Choices: []dto.ChatCompletionsStreamResponseChoice{
						{
							Index: 0,
							Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
								Content: event.Data.DeltaContent,
								Role:    "assistant",
							},
						},
					},
				}
				jsonData, marshalErr := common.Marshal(chunk)
				if marshalErr != nil {
					return
				}
				c.Render(-1, common.CustomEvent{Data: "data: " + string(jsonData)})
				c.Writer.Flush()
			}
		case copilot.SessionEventTypeAssistantUsage:
			if event.Data.InputTokens != nil {
				usage.PromptTokens = int(*(event.Data.InputTokens))
			}
			if event.Data.OutputTokens != nil {
				usage.CompletionTokens = int(*(event.Data.OutputTokens))
			}
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		case copilot.SessionEventTypeSessionIdle:
			finished = true
			// Send final chunk with finish_reason
			finishReason := "stop"
			finalChunk := &dto.ChatCompletionsStreamResponse{
				Id:      responseID,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   model,
				Choices: []dto.ChatCompletionsStreamResponseChoice{
					{
						Index:        0,
						Delta:        dto.ChatCompletionsStreamResponseChoiceDelta{},
						FinishReason: &finishReason,
					},
				},
				Usage: &usage,
			}
			jsonData, _ := common.Marshal(finalChunk)
			c.Render(-1, common.CustomEvent{Data: "data: " + string(jsonData)})
			c.Render(-1, common.CustomEvent{Data: "data: [DONE]"})
			c.Writer.Flush()
			doneCh <- struct{}{}
		case copilot.SessionEventTypeSessionError:
			finished = true
			msg := "unknown error"
			if event.Data.Message != nil {
				msg = *event.Data.Message
			}
			errCh <- fmt.Errorf("copilot session error: %s", msg)
		}
	})
	defer unsubscribe()

	_, sendErr := session.Send(ctx, copilot.MessageOptions{
		Prompt: prompt,
	})
	if sendErr != nil {
		return nil, types.NewError(fmt.Errorf("failed to send message: %w", sendErr), types.ErrorCodeDoRequestFailed)
	}

	// Wait for completion
	select {
	case <-doneCh:
		return &usage, nil
	case err := <-errCh:
		return nil, types.NewError(err, types.ErrorCodeDoRequestFailed)
	case <-ctx.Done():
		return nil, types.NewError(ctx.Err(), types.ErrorCodeDoRequestFailed)
	}
}

func (a *Adaptor) handleNonStream(c *gin.Context, session *copilot.Session, ctx context.Context, prompt string, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	var usage dto.Usage

	// Collect usage from events
	var mu sync.Mutex
	unsubscribe := session.On(func(event copilot.SessionEvent) {
		if event.Type == copilot.SessionEventTypeAssistantUsage {
			mu.Lock()
			defer mu.Unlock()
			if event.Data.InputTokens != nil {
				usage.PromptTokens = int(*(event.Data.InputTokens))
			}
			if event.Data.OutputTokens != nil {
				usage.CompletionTokens = int(*(event.Data.OutputTokens))
			}
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
	})
	defer unsubscribe()

	result, sendErr := session.SendAndWait(ctx, copilot.MessageOptions{
		Prompt: prompt,
	})
	if sendErr != nil {
		return nil, types.NewError(fmt.Errorf("failed to send message: %w", sendErr), types.ErrorCodeDoRequestFailed)
	}

	content := ""
	if result != nil && result.Data.Content != nil {
		content = *result.Data.Content
	}

	response := dto.OpenAITextResponse{
		Id:      fmt.Sprintf("chatcmpl-copilot-%d", time.Now().UnixNano()),
		Model:   info.UpstreamModelName,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index: 0,
				Message: dto.Message{
					Role:    "assistant",
					Content: content,
				},
				FinishReason: "stop",
			},
		},
		Usage: usage,
	}

	jsonResponse, marshalErr := common.Marshal(response)
	if marshalErr != nil {
		return nil, types.NewError(marshalErr, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	_, _ = c.Writer.Write(jsonResponse)
	return &usage, nil
}

// messagesToPrompt converts OpenAI messages to a single prompt string for the SDK.
// System messages are excluded here (handled separately via SessionConfig.SystemMessage).
func messagesToPrompt(messages []dto.Message) string {
	var parts []string
	for _, msg := range messages {
		if msg.Role == "system" {
			continue
		}
		content := msg.StringContent()
		if content == "" {
			continue
		}
		parts = append(parts, content)
	}
	if len(parts) == 0 {
		return ""
	}
	// Use only the last user message as the prompt for the SDK session
	return parts[len(parts)-1]
}

// extractSystemMessage extracts and concatenates all system messages.
func extractSystemMessage(messages []dto.Message) string {
	var parts []string
	for _, msg := range messages {
		if msg.Role == "system" {
			content := msg.StringContent()
			if content != "" {
				parts = append(parts, content)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
