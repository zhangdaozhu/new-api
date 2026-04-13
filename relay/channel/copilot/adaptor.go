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

	isClaudeFormat := info.RelayFormat == types.RelayFormatClaude

	var usage dto.Usage
	responseID := fmt.Sprintf("chatcmpl-copilot-%d", time.Now().UnixNano())
	if isClaudeFormat {
		responseID = fmt.Sprintf("msg_copilot_%d", time.Now().UnixNano())
	}
	created := time.Now().Unix()
	model := info.UpstreamModelName

	doneCh := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	var mu sync.Mutex
	var finished bool
	contentBlockStarted := false

	// Helper to send a Claude SSE event
	sendClaudeEvent := func(eventType string, data any) {
		jsonData, marshalErr := common.Marshal(data)
		if marshalErr != nil {
			return
		}
		c.Render(-1, common.CustomEvent{Data: "event: " + eventType})
		c.Render(-1, common.CustomEvent{Data: "data: " + string(jsonData)})
		c.Writer.Flush()
	}

	if isClaudeFormat {
		// Send message_start event
		sendClaudeEvent("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            responseID,
				"type":          "message",
				"role":          "assistant",
				"content":       []any{},
				"model":         model,
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  0,
					"output_tokens": 0,
				},
			},
		})
	}

	unsubscribe := session.On(func(event copilot.SessionEvent) {
		mu.Lock()
		defer mu.Unlock()
		if finished {
			return
		}

		switch event.Type {
		case copilot.SessionEventTypeAssistantMessageDelta:
			if event.Data.DeltaContent != nil {
				if isClaudeFormat {
					if !contentBlockStarted {
						contentBlockStarted = true
						sendClaudeEvent("content_block_start", map[string]any{
							"type":  "content_block_start",
							"index": 0,
							"content_block": map[string]any{
								"type": "text",
								"text": "",
							},
						})
					}
					sendClaudeEvent("content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": 0,
						"delta": map[string]any{
							"type": "text_delta",
							"text": *event.Data.DeltaContent,
						},
					})
				} else {
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
			if isClaudeFormat {
				if contentBlockStarted {
					sendClaudeEvent("content_block_stop", map[string]any{
						"type":  "content_block_stop",
						"index": 0,
					})
				}
				sendClaudeEvent("message_delta", map[string]any{
					"type": "message_delta",
					"delta": map[string]any{
						"stop_reason":   "end_turn",
						"stop_sequence": nil,
					},
					"usage": map[string]any{
						"output_tokens": usage.CompletionTokens,
					},
				})
				sendClaudeEvent("message_stop", map[string]any{
					"type": "message_stop",
				})
			} else {
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
			}
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
	isClaudeFormat := info.RelayFormat == types.RelayFormatClaude

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

	var jsonResponse []byte
	var marshalErr error

	if isClaudeFormat {
		response := dto.ClaudeResponse{
			Id:   fmt.Sprintf("msg_copilot_%d", time.Now().UnixNano()),
			Type: "message",
			Role: "assistant",
			Content: []dto.ClaudeMediaMessage{
				{
					Type: "text",
				},
			},
			StopReason: "end_turn",
			Model:      info.UpstreamModelName,
			Usage: &dto.ClaudeUsage{
				InputTokens:  usage.PromptTokens,
				OutputTokens: usage.CompletionTokens,
			},
		}
		response.Content[0].SetText(content)
		jsonResponse, marshalErr = common.Marshal(response)
	} else {
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
		jsonResponse, marshalErr = common.Marshal(response)
	}

	if marshalErr != nil {
		return nil, types.NewError(marshalErr, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	_, _ = c.Writer.Write(jsonResponse)
	return &usage, nil
}

// messagesToPrompt converts OpenAI messages to a prompt string for the SDK.
// System messages are excluded (handled via SessionConfig.SystemMessage).
// If there are multiple non-system messages, earlier messages are formatted as
// conversation history so the model has full context.
func messagesToPrompt(messages []dto.Message) string {
	var nonSystem []dto.Message
	for _, msg := range messages {
		if msg.Role == "system" {
			continue
		}
		content := msg.StringContent()
		if content == "" {
			continue
		}
		nonSystem = append(nonSystem, msg)
	}
	if len(nonSystem) == 0 {
		return ""
	}
	// Single message: send directly
	if len(nonSystem) == 1 {
		return nonSystem[0].StringContent()
	}
	// Multiple messages: include conversation history for context
	var sb strings.Builder
	for i, msg := range nonSystem[:len(nonSystem)-1] {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		switch msg.Role {
		case "user":
			sb.WriteString("[User]: ")
		case "assistant":
			sb.WriteString("[Assistant]: ")
		default:
			sb.WriteString("[" + msg.Role + "]: ")
		}
		sb.WriteString(msg.StringContent())
	}
	sb.WriteString("\n\n[User]: ")
	sb.WriteString(nonSystem[len(nonSystem)-1].StringContent())
	return sb.String()
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
