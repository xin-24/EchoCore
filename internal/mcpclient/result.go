package mcpclient

import (
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"unicode/utf8"
)

func resultJSON(result *mcp.CallToolResult, limit int) string {
	if result == nil {
		return `{"error":"empty_result"}`
	}
	out := map[string]any{"is_error": result.IsError, "source": "untrusted_mcp_tool"}
	var text string
	unsupported := false
	truncated := false
	for _, block := range result.Content {
		if t, ok := block.(*mcp.TextContent); ok {
			room := limit - len(text)
			if room > 0 {
				part := t.Text
				if len(part) > room {
					part = part[:room]
					for !utf8.ValidString(part) {
						part = part[:len(part)-1]
					}
					truncated = true
				}
				text += part + "\n"
			} else {
				truncated = true
			}
		} else {
			unsupported = true
		}
	}
	if result.StructuredContent != nil {
		raw, e := json.Marshal(result.StructuredContent)
		if e == nil && len(raw) <= limit/2 {
			if _, e = boundedJSON(raw, limit/2); e == nil {
				out["structured_content"] = json.RawMessage(raw)
			} else {
				out["structured_omitted"] = true
			}
		} else {
			out["structured_omitted"] = true
		}
	}
	if unsupported {
		out["unsupported_content"] = true
	}
	// JSON 转义也计入预算；不将截断后的 JSON 冒充完整结构化结果。
	for {
		out["text"] = text
		out["truncated"] = truncated
		raw, _ := json.Marshal(out)
		if len(raw) <= limit {
			return string(raw)
		}
		if len(text) == 0 {
			delete(out, "structured_content")
			out["structured_omitted"] = true
			continue
		}
		n := len(text) / 2
		text = text[:n]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		truncated = true
	}
}
