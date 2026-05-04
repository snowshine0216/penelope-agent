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

type OpenAIProvider struct {
	client openai.Client // 值类型，非指针
	model  string
}

// NewZhipuOpenAIProvider constructs an OpenAIProvider backed by an OpenAI-compatible API endpoint.
func NewZhipuOpenAIProvider(model string) (*OpenAIProvider, error) {
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
	var openaiMsgs []openai.ChatCompletionMessageParamUnion

	// 1. 翻译上下文消息
	for _, msg := range msgs {
		switch msg.Role {
		case schema.RoleSystem:
			openaiMsgs = append(openaiMsgs, openai.SystemMessage(msg.Content))

		case schema.RoleTool:
			// 注意：v3 新版参数顺序是 (content, toolCallID)
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

			// 【重要】如果历史包含 ToolCalls，必须原样放回，以维系大模型的逻辑链
			if len(msg.ToolCalls) > 0 {
				var toolCalls []openai.ChatCompletionMessageToolCallUnionParam
				for _, tc := range msg.ToolCalls {
					// OfFunction 对应 GetFunction()，字段类型严格要求为指针
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

	// 2. 翻译工具定义 (v3 新 API 特性适配)
	var openaiTools []openai.ChatCompletionToolUnionParam
	for _, toolDef := range availableTools {
		var params shared.FunctionParameters

		// 尝试直接断言，如果不成功则通过 JSON 往返序列化来保证类型匹配
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

	// 3. 构建请求并发送
	params := openai.ChatCompletionNewParams{
		Model:    p.model,
		Messages: openaiMsgs,
	}

	// 【慢思考机制支撑】仅当 availableTools 存在时才挂载 Tools
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

	// 4. 将 API Response 反向翻译为内部 schema.Message
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
				Arguments: []byte(tc.Function.Arguments), // 提取 JSON 字符串字节
			})
		default:
			return nil, fmt.Errorf("unsupported tool call type from OpenAI: %q", tc.Type)
		}
	}

	return resultMsg, nil
}
