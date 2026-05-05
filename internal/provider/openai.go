package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// OpenAIProvider is an LLMProvider backed by an OpenAI-compatible API endpoint.
type OpenAIProvider struct {
	client openai.Client // held by value; openai.Client is not a pointer type
	model  string
}

// NewOpenAIProvider constructs an OpenAIProvider backed by an OpenAI-compatible API endpoint.
func NewOpenAIProvider(model string) (*OpenAIProvider, error) {
	cfg, err := loadProviderConfig()
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(model) == "" {
		model = cfg.Model
	}

	return &OpenAIProvider{
		client: openai.NewClient(option.WithAPIKey(cfg.APIKey), option.WithBaseURL(cfg.BaseURL)),
		model:  model,
	}, nil
}

// Generate sends messages to the OpenAI-compatible API and returns the next assistant message.
func (p *OpenAIProvider) Generate(ctx context.Context, msgs []schema.Message, availableTools []schema.ToolDefinition) (*schema.Message, error) {
	openaiMsgs, err := translateMessagesToOpenAI(msgs)
	if err != nil {
		return nil, err
	}

	openaiTools, err := translateToolsToOpenAI(availableTools)
	if err != nil {
		return nil, err
	}

	params := openai.ChatCompletionNewParams{
		Model:    p.model,
		Messages: openaiMsgs,
	}
	if len(openaiTools) > 0 {
		params.Tools = openaiTools
	}

	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai API request failed: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai API returned empty choices")
	}

	choice := resp.Choices[0].Message
	resultMsg := &schema.Message{
		Role:    schema.RoleAssistant,
		Content: choice.Content,
	}

	for _, tc := range choice.ToolCalls {
		switch tc.Type {
		case "function":
			resultMsg.ToolCalls = append(resultMsg.ToolCalls, schema.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: []byte(tc.Function.Arguments),
			})
		default:
			return nil, fmt.Errorf("unsupported tool call type from OpenAI: %q", tc.Type)
		}
	}

	return resultMsg, nil
}

// translateMessagesToOpenAI converts provider-neutral messages into OpenAI
// ChatCompletionMessageParamUnion values.
func translateMessagesToOpenAI(msgs []schema.Message) ([]openai.ChatCompletionMessageParamUnion, error) {
	var openaiMsgs []openai.ChatCompletionMessageParamUnion

	for _, msg := range msgs {
		switch msg.Role {
		case schema.RoleSystem:
			openaiMsgs = append(openaiMsgs, openai.SystemMessage(msg.Content))
		case schema.RoleTool:
			openaiMsgs = append(openaiMsgs, openai.ToolMessage(msg.Content, msg.ToolCallID))
		case schema.RoleUser:
			openaiMsgs = append(openaiMsgs, openai.UserMessage(msg.Content))
		case schema.RoleAssistant:
			astParam := openai.ChatCompletionAssistantMessageParam{}
			if msg.Content != "" {
				astParam.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.String(msg.Content),
				}
			}
			if len(msg.ToolCalls) > 0 {
				var toolCalls []openai.ChatCompletionMessageToolCallUnionParam
				for _, tc := range msg.ToolCalls {
					toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID:   tc.ID,
							Type: "function",
							Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
								Name:      tc.Name,
								Arguments: string(tc.Arguments),
							},
						},
					})
				}
				astParam.ToolCalls = toolCalls
			}
			openaiMsgs = append(openaiMsgs, openai.ChatCompletionMessageParamUnion{
				OfAssistant: &astParam,
			})
		}
	}

	return openaiMsgs, nil
}

// translateToolsToOpenAI converts provider-neutral tool definitions into OpenAI
// ChatCompletionToolUnionParams.
func translateToolsToOpenAI(defs []schema.ToolDefinition) ([]openai.ChatCompletionToolUnionParam, error) {
	var openaiTools []openai.ChatCompletionToolUnionParam

	for _, toolDef := range defs {
		var params shared.FunctionParameters

		if m, ok := toolDef.InputSchema.(map[string]interface{}); ok {
			params = shared.FunctionParameters(m)
		} else {
			b, err := json.Marshal(toolDef.InputSchema)
			if err != nil {
				return nil, fmt.Errorf("encode tool schema for %s: %w", toolDef.Name, err)
			}
			if err := json.Unmarshal(b, &params); err != nil {
				return nil, fmt.Errorf("decode tool schema for %s: %w", toolDef.Name, err)
			}
		}

		openaiTools = append(openaiTools, openai.ChatCompletionFunctionTool(
			shared.FunctionDefinitionParam{
				Name:        toolDef.Name,
				Description: openai.String(toolDef.Description),
				Parameters:  params,
			},
		))
	}

	return openaiTools, nil
}
