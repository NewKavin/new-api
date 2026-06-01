package openaicompat

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/samber/lo"
)

func ResponsesRequestToChatCompletionsRequest(req *dto.OpenAIResponsesRequest) (*dto.GeneralOpenAIRequest, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}
	if strings.TrimSpace(req.Model) == "" {
		return nil, errors.New("model is required")
	}

	stream := lo.FromPtrOr(req.Stream, false)

	out := &dto.GeneralOpenAIRequest{
		Model:       req.Model,
		Stream:      &stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		TopLogProbs: req.TopLogProbs,
		Metadata:    req.Metadata,
	}
	if stream {
		out.StreamOptions = req.StreamOptions
	}

	if req.MaxOutputTokens != nil {
		maxTokens := lo.FromPtr(req.MaxOutputTokens)
		out.MaxTokens = &maxTokens
	}

	if req.Reasoning != nil {
		reasoningRaw, err := common.Marshal(req.Reasoning)
		if err != nil {
			return nil, fmt.Errorf("marshal reasoning failed: %w", err)
		}
		out.Reasoning = reasoningRaw
	}

	if len(req.ParallelToolCalls) > 0 {
		var flag bool
		if err := common.Unmarshal(req.ParallelToolCalls, &flag); err == nil {
			out.ParallelTooCalls = &flag
		}
	}

	if len(req.ToolChoice) > 0 {
		var choice any
		if err := common.Unmarshal(req.ToolChoice, &choice); err != nil {
			return nil, fmt.Errorf("unmarshal tool_choice failed: %w", err)
		}
		out.ToolChoice = choice
	}

	if len(req.Tools) > 0 {
		tools, err := parseResponsesTools(req.Tools)
		if err != nil {
			return nil, err
		}
		out.Tools = tools
	}

	instruction := parseResponsesInstructions(req.Instructions)
	if instruction != "" {
		out.Messages = append(out.Messages, dto.Message{
			Role:    "system",
			Content: instruction,
		})
	}

	inputMessages, err := parseResponsesInputToChatMessages(req.Input)
	if err != nil {
		return nil, err
	}
	out.Messages = append(out.Messages, inputMessages...)
	return out, nil
}

func parseResponsesTools(raw []byte) ([]dto.ToolCallRequest, error) {
	var items []map[string]any
	if err := common.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("unmarshal tools failed: %w", err)
	}
	tools := make([]dto.ToolCallRequest, 0, len(items))
	for _, item := range items {
		if common.Interface2String(item["type"]) != "function" {
			continue
		}
		tool := dto.ToolCallRequest{
			Type: "function",
			Function: dto.FunctionRequest{
				Name:        common.Interface2String(item["name"]),
				Description: common.Interface2String(item["description"]),
				Parameters:  item["parameters"],
			},
		}
		if tool.Function.Name == "" {
			continue
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

func parseResponsesInstructions(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	switch common.GetJsonType(raw) {
	case "string":
		var instruction string
		_ = common.Unmarshal(raw, &instruction)
		return strings.TrimSpace(instruction)
	default:
		return strings.TrimSpace(string(raw))
	}
}

func parseResponsesInputToChatMessages(raw []byte) ([]dto.Message, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if common.GetJsonType(raw) == "string" {
		var content string
		if err := common.Unmarshal(raw, &content); err != nil {
			return nil, fmt.Errorf("unmarshal string input failed: %w", err)
		}
		return []dto.Message{{Role: "user", Content: content}}, nil
	}

	var items []map[string]any
	if err := common.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("unmarshal input failed: %w", err)
	}

	messages := make([]dto.Message, 0, len(items))
	for _, item := range items {
		if itemType := common.Interface2String(item["type"]); itemType != "" && itemType != "message" {
			switch itemType {
			case "function_call":
				callID := strings.TrimSpace(common.Interface2String(item["call_id"]))
				name := strings.TrimSpace(common.Interface2String(item["name"]))
				if callID == "" || name == "" {
					continue
				}
				assistantMsg := dto.Message{
					Role:    "assistant",
					Content: "",
				}
				assistantMsg.SetToolCalls([]dto.ToolCallRequest{
					{
						ID:   callID,
						Type: "function",
						Function: dto.FunctionRequest{
							Name:      name,
							Arguments: responsesArgumentsToString(item["arguments"]),
						},
					},
				})
				messages = append(messages, assistantMsg)
			case "function_call_output":
				callID := strings.TrimSpace(common.Interface2String(item["call_id"]))
				if callID == "" {
					continue
				}
				messages = append(messages, dto.Message{
					Role:       "tool",
					ToolCallId: callID,
					Content:    responsesArgumentsToString(item["output"]),
				})
			case "input_text":
				messages = append(messages, dto.Message{
					Role:    "user",
					Content: common.Interface2String(item["text"]),
				})
			case "output_text":
				messages = append(messages, dto.Message{
					Role:    "assistant",
					Content: common.Interface2String(item["text"]),
				})
			default:
				// Ignore unsupported standalone item types in phase1.
			}
			continue
		}

		role := normalizeResponsesChatRole(common.Interface2String(item["role"]))
		if role == "" {
			continue
		}
		content := responsesContentToChatContent(item["content"], role)
		if role == "system" {
			content = responsesContentToSystemString(content)
		}
		msg := dto.Message{
			Role:    role,
			Content: content,
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func normalizeResponsesChatRole(role string) string {
	role = strings.TrimSpace(role)
	if role == "developer" {
		return "system"
	}
	return role
}

func responsesContentToSystemString(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []map[string]any:
		parts := make([]string, 0, len(v))
		for _, part := range v {
			if common.Interface2String(part["type"]) == "text" {
				text := strings.TrimSpace(common.Interface2String(part["text"]))
				if text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return common.Interface2String(v)
	}
}

func responsesContentToChatContent(raw any, role string) any {
	switch v := raw.(type) {
	case string:
		return v
	case []any:
		parts := make([]map[string]any, 0, len(v))
		for _, partAny := range v {
			partMap, ok := partAny.(map[string]any)
			if !ok {
				continue
			}
			partType := common.Interface2String(partMap["type"])
			switch partType {
			case "input_text", "output_text":
				parts = append(parts, map[string]any{
					"type": "text",
					"text": common.Interface2String(partMap["text"]),
				})
			case "input_image":
				parts = append(parts, map[string]any{
					"type":      "image_url",
					"image_url": partMap["image_url"],
				})
			default:
				// Keep unknown parts as-is for best-effort compatibility.
				parts = append(parts, partMap)
			}
		}
		if len(parts) == 0 {
			return ""
		}
		return parts
	default:
		return common.Interface2String(v)
	}
}

func responsesArgumentsToString(v any) string {
	switch vv := v.(type) {
	case nil:
		return ""
	case string:
		return vv
	default:
		b, err := common.Marshal(vv)
		if err != nil {
			return fmt.Sprintf("%v", vv)
		}
		return string(b)
	}
}
