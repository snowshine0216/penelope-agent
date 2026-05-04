package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

type ClaudeProvider struct {
	client    anthropic.Client
	model     string
	// MaxTokens caps the number of tokens the model may generate per request.
	// A zero value causes Generate to apply the built-in default of 4096.
	MaxTokens int64
}

// NewZhipuClaudeProvider constructs a ClaudeProvider backed by the Anthropic API.
func NewZhipuClaudeProvider(model string) (*ClaudeProvider, error) {
	cfg, err := loadProviderConfig()
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(model) == "" {
		model = cfg.Model
	}

	return &ClaudeProvider{
		client: anthropic.NewClient(option.WithAPIKey(cfg.APIKey), option.WithBaseURL(cfg.BaseURL)),
		model:  model,
	}, nil
}

// Generate sends messages to the Anthropic API and returns the next assistant message.
func (p *ClaudeProvider) Generate(ctx context.Context, msgs []schema.Message, availableTools []schema.ToolDefinition) (*schema.Message, error) {
	var anthropicMsgs []anthropic.MessageParam
	var systemPrompt string

	// 1. 消息翻译
	for _, msg := range msgs {
		switch msg.Role {
		case schema.RoleSystem:
			systemPrompt = msg.Content
		case schema.RoleTool:
			anthropicMsgs = append(anthropicMsgs, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, msg.IsError),
			))
		case schema.RoleUser:
			anthropicMsgs = append(anthropicMsgs, anthropic.NewUserMessage(
				anthropic.NewTextBlock(msg.Content),
			))
		case schema.RoleAssistant:
			var blocks []anthropic.ContentBlockParamUnion
			if msg.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
			}

			// 将历史工具调用转回 Claude 特有的 ToolUseBlockParam
			for _, tc := range msg.ToolCalls {
				var inputMap map[string]interface{}
				if err := json.Unmarshal(tc.Arguments, &inputMap); err != nil {
					return nil, fmt.Errorf("decode tool call %s arguments: %w", tc.Name, err)
				}
				blocks = append(blocks, anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						ID:    tc.ID,
						Name:  tc.Name,
						Input: inputMap,
					},
				})
			}
			if len(blocks) == 0 {
				// Anthropic requires at least one content block per assistant message.
				// Insert an empty text block to keep history contiguous.
				blocks = append(blocks, anthropic.NewTextBlock(""))
			}
			anthropicMsgs = append(anthropicMsgs, anthropic.NewAssistantMessage(blocks...))
		}
	}

	// 2. 工具 Schema 翻译
	var anthropicTools []anthropic.ToolUnionParam
	for _, toolDef := range availableTools {
		// ToolInputSchemaParam 是结构体，需要通过 Properties 字段精准填充
		var properties map[string]any
		var required []string

		if m, ok := toolDef.InputSchema.(map[string]interface{}); ok {
			if p, ok := m["properties"].(map[string]interface{}); ok {
				properties = p
			}
			required = ExtractRequiredStrings(m["required"])
		}

		tp := anthropic.ToolParam{
			Name:        toolDef.Name,
			Description: anthropic.String(toolDef.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: properties,
				Required:   required,
			},
		}
		anthropicTools = append(anthropicTools, anthropic.ToolUnionParam{OfTool: &tp})
	}

	// 3. 构建请求并发送
	maxTokens := p.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: maxTokens,
		Messages:  anthropicMsgs,
	}

	if systemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{Text: systemPrompt},
		}
	}

	if len(anthropicTools) > 0 {
		params.Tools = anthropicTools
	}

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("claude API request failed: %w", err)
	}

	// 4. 反向解析
	resultMsg := &schema.Message{
		Role: schema.RoleAssistant,
	}

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			resultMsg.Content += block.Text
		case "tool_use":
			argsBytes, err := json.Marshal(block.Input)
			if err != nil {
				return nil, fmt.Errorf("encode tool call %s input: %w", block.Name, err)
			}
			resultMsg.ToolCalls = append(resultMsg.ToolCalls, schema.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: argsBytes,
			})
		}
	}

	return resultMsg, nil
}

// ExtractRequiredStrings reads a JSON-Schema "required" value from an
// untyped slot. Tools build the schema with a []string literal, but any
// schema that round-trips through JSON arrives as []interface{}. Handle
// both shapes.
func ExtractRequiredStrings(v interface{}) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []interface{}:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}
