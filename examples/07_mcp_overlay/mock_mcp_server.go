package main

import (
	"encoding/json"
	"log"
	"os"

	mcpclient "github.com/petal-labs/petalflow/tool/mcp"
)

func main() {
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)

	for {
		var req mcpclient.Message
		if err := decoder.Decode(&req); err != nil {
			return
		}

		switch req.Method {
		case "initialize":
			writeResponse(encoder, mcpclient.Message{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustRaw(mcpclient.InitializeResult{
					ProtocolVersion: "2025-06-18",
					ServerInfo: mcpclient.ServerInfo{
						Name:    "mock-mcp-server",
						Version: "0.1.0",
					},
				}),
			})
		case "tools/list":
			writeResponse(encoder, mcpclient.Message{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustRaw(mcpclient.ToolsListResult{
					Tools: []mcpclient.Tool{
						{
							Name:        "list_s3_objects",
							Description: "List S3 object keys",
							InputSchema: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"bucket":   map[string]any{"type": "string"},
									"prefix":   map[string]any{"type": "string"},
									"max_keys": map[string]any{"type": "integer"},
								},
								"required": []string{"bucket"},
							},
						},
						{
							Name:        "download_s3_object",
							Description: "Download object by key",
							InputSchema: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"bucket": map[string]any{"type": "string"},
									"key":    map[string]any{"type": "string"},
								},
								"required": []string{"bucket", "key"},
							},
						},
					},
				}),
			})
		case "tools/call":
			var params map[string]any
			_ = json.Unmarshal(req.Params, &params)
			toolName, _ := params["name"].(string)

			switch toolName {
			case "list_s3_objects":
				writeResponse(encoder, mcpclient.Message{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: mustRaw(mcpclient.ToolsCallResult{
						Content: []mcpclient.ContentBlock{
							{Type: "text", Text: `{"keys":["2025/Q1/revenue.pdf","2025/Q1/expenses.pdf"],"count":2}`},
						},
					}),
				})
			case "download_s3_object":
				writeResponse(encoder, mcpclient.Message{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: mustRaw(mcpclient.ToolsCallResult{
						Content: []mcpclient.ContentBlock{
							{Type: "text", Text: `{"content":"example","key":"2025/Q1/revenue.pdf"}`},
						},
					}),
				})
			default:
				writeResponse(encoder, mcpclient.Message{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: &mcpclient.RPCError{
						Code:    -32601,
						Message: "unknown tool",
					},
				})
			}
		default:
			// notifications (initialized/close) are ignored by this example server.
		}
	}
}

func writeResponse(encoder *json.Encoder, message mcpclient.Message) {
	if err := encoder.Encode(message); err != nil {
		log.Printf("failed to encode MCP response: %v", err)
	}
}

func mustRaw(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
