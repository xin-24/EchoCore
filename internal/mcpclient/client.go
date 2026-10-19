package mcpclient

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/xin-24/EchoCore/internal/llm"
)

type catalogTool struct {
	definition llm.ToolDefinition
	remote     string
	schema     *jsonschema.Resolved
}

type catalogError string

func (e catalogError) Error() string { return string(e) }

type operationClient struct {
	session       *mcp.ClientSession
	transport     *boundedTransport
	httpTransport http.RoundTripper
}

func connect(ctx context.Context, s ServerConfig, base http.RoundTripper) (*operationClient, error) {
	if base == nil {
		base = secureTransport(s.URL)
	}
	t := &boundedTransport{base: base, endpoint: s.URL, token: s.Token, ctx: ctx}
	t.remaining.Store(1 << 20)
	h := &http.Client{Transport: t, Timeout: time.Duration(s.TimeoutMS) * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	c := mcp.NewClient(&mcp.Implementation{Name: "EchoCore", Version: "1"}, &mcp.ClientOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Capabilities: &mcp.ClientCapabilities{}})
	session, e := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: s.URL, HTTPClient: h, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
	op := &operationClient{session, t, base}
	if e != nil {
		op.close()
		return op, e
	}
	return op, nil
}
func (o *operationClient) close() {
	if o.session != nil {
		o.session.Close()
	}
	if t, ok := o.httpTransport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
}
func (o *operationClient) discover(ctx context.Context, s ServerConfig) ([]catalogTool, error) {
	var result []catalogTool
	cursor := ""
	seen := map[string]bool{}
	names := map[string]bool{}
	count := 0
	for page := 0; page < 20; page++ {
		r, e := o.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if e != nil {
			return nil, e
		}
		count += len(r.Tools)
		if count > 256 {
			return nil, errLimit
		}
		for _, t := range r.Tools {
			if t == nil || names[t.Name] {
				return nil, catalogError("invalid_catalog")
			}
			names[t.Name] = true
			if !slices.Contains(s.AllowedTools, t.Name) {
				continue
			}
			if len(t.Description) > 2048 {
				return nil, errLimit
			}
			raw, e := json.Marshal(t.InputSchema)
			if e != nil {
				return nil, catalogError("invalid_schema")
			}
			schema, e := compileSchema(raw)
			if e != nil {
				return nil, catalogError("unsupported_or_invalid_schema")
			}
			result = append(result, catalogTool{llm.ToolDefinition{Name: "mcp_" + s.Name + "_" + t.Name, Description: t.Description, Schema: raw}, t.Name, schema})
		}
		cursor = r.NextCursor
		if cursor == "" {
			for _, approved := range s.AllowedTools {
				if !names[approved] {
					return nil, catalogError("approved_tool_missing")
				}
			}
			return result, nil
		}
		if len(cursor) > 4096 || seen[cursor] {
			return nil, catalogError("invalid_cursor")
		}
		seen[cursor] = true
	}
	return nil, errLimit
}
