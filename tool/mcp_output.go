package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	mcpclient "github.com/petal-labs/petalflow/tool/mcp"
)

// ParseMCPCallResult converts MCP call results into PetalFlow outputs.
func ParseMCPCallResult(result mcpclient.ToolsCallResult, action ActionSpec) (map[string]any, error) {
	attachments := extractMCPAttachments(result.Content)
	textValue := collectMCPText(result.Content)

	if len(action.Outputs) > 0 {
		payload := cloneAnyMap(result.StructuredContent)
		if len(payload) == 0 && strings.TrimSpace(textValue) != "" {
			decoded, err := decodeJSONObject(textValue)
			if err != nil {
				return nil, fmt.Errorf("tool: parse mcp typed output: %w", err)
			}
			payload = decoded
		}
		if len(payload) == 0 {
			payload = map[string]any{}
		}
		if len(attachments) > 0 {
			payload["attachments"] = attachments
		}
		return payload, nil
	}

	raw := any(nil)
	switch {
	case len(result.StructuredContent) > 0:
		raw = cloneAnyMap(result.StructuredContent)
	case textValue != "":
		raw = textValue
	default:
		raw = map[string]any{}
	}

	if len(attachments) > 0 {
		switch typed := raw.(type) {
		case map[string]any:
			typed["attachments"] = attachments
			raw = typed
		default:
			raw = map[string]any{
				"text":        raw,
				"attachments": attachments,
			}
		}
	}

	return map[string]any{
		"result": raw,
	}, nil
}

func extractMCPAttachments(content []mcpclient.ContentBlock) []map[string]any {
	attachments := make([]map[string]any, 0)
	for _, block := range content {
		if block.Type == "text" {
			continue
		}
		attachments = append(attachments, map[string]any{
			"type":      block.Type,
			"data":      block.Data,
			"mime_type": block.MimeType,
		})
	}
	return attachments
}

func collectMCPText(content []mcpclient.ContentBlock) string {
	parts := make([]string, 0)
	for _, block := range content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func decodeJSONObject(raw string) (map[string]any, error) {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
