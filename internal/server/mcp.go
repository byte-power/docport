package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// RunMCP serves the Model Context Protocol over stdio (newline-delimited
// JSON-RPC 2.0), exposing search_docs / read_doc / list_docs tools so
// agents like Claude Code and OpenCode can query the exported corpus.
func RunMCP(engine Searcher, defaultTopK int) error {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 16<<20)
	out := bufio.NewWriter(os.Stdout)

	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		resp := handle(engine, defaultTopK, &req)
		if resp == nil { // notification
			continue
		}
		b, err := json.Marshal(resp)
		if err != nil {
			continue
		}
		out.Write(b)
		out.WriteByte('\n')
		out.Flush()
	}
	if err := in.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func handle(engine Searcher, defaultTopK int, req *rpcRequest) *rpcResponse {
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return nil // notifications get no response
	}
	ok := func(result any) *rpcResponse {
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
	}
	fail := func(code int, msg string) *rpcResponse {
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: msg}}
	}

	switch req.Method {
	case "initialize":
		return ok(initializeResult(req.Params))
	case "ping":
		return ok(map[string]any{})
	case "tools/list":
		return ok(map[string]any{"tools": toolDefs()})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fail(-32602, "invalid params")
		}
		text, err := callTool(engine, defaultTopK, p.Name, p.Arguments)
		if err != nil {
			return ok(toolResult(err.Error(), true))
		}
		return ok(toolResult(text, false))
	default:
		return fail(-32601, "method not found: "+req.Method)
	}
}

// initializeResult echoes the protocol version the client asked for
// (this tools-only server behaves identically across MCP revisions).
func initializeResult(params json.RawMessage) map[string]any {
	version := "2024-11-05"
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(params, &p) == nil && p.ProtocolVersion != "" {
		version = p.ProtocolVersion
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "docport", "version": "0.1.0"},
	}
}

func toolResult(text string, isErr bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isErr,
	}
}

func toolDefs() []map[string]any {
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	return []map[string]any{
		{
			"name":        "search_docs",
			"description": "Search the exported Confluence documentation (hybrid keyword + vector retrieval). Returns matching chunks with doc IDs.",
			"inputSchema": obj(map[string]any{
				"query": map[string]any{"type": "string", "description": "search query, Chinese or English"},
				"top_k": map[string]any{"type": "integer", "description": "number of results (default 8)"},
			}, "query"),
		},
		{
			"name":        "read_doc",
			"description": "Read the full Markdown of one exported Confluence page by doc_id (page ID) or path returned by search_docs / list_docs.",
			"inputSchema": obj(map[string]any{
				"doc_id": map[string]any{"type": "string", "description": "page ID or docs/... path"},
			}, "doc_id"),
		},
		{
			"name":        "list_docs",
			"description": "List exported Confluence pages (title, doc_id, space, keywords), optionally filtered by space key.",
			"inputSchema": obj(map[string]any{
				"space": map[string]any{"type": "string", "description": "optional space key filter"},
			}),
		},
	}
}

func callTool(engine Searcher, defaultTopK int, name string, args json.RawMessage) (string, error) {
	switch name {
	case "search_docs":
		var a struct {
			Query string `json:"query"`
			TopK  int    `json:"top_k"`
		}
		if err := json.Unmarshal(args, &a); err != nil || a.Query == "" {
			return "", fmt.Errorf("search_docs requires a non-empty \"query\"")
		}
		if a.TopK <= 0 {
			a.TopK = defaultTopK
		}
		results, err := engine.Search(a.Query, a.TopK)
		if err != nil {
			return "", err
		}
		if len(results) == 0 {
			return "No results.", nil
		}
		var b strings.Builder
		for i, r := range results {
			fmt.Fprintf(&b, "## %d. %s", i+1, r.Title)
			if r.Section != "" {
				fmt.Fprintf(&b, " > %s", r.Section)
			}
			fmt.Fprintf(&b, "\ndoc_id: %s | space: %s | url: %s\n\n%s\n\n---\n\n", r.DocID, r.Space, r.URL, r.Text)
		}
		return b.String(), nil
	case "read_doc":
		var a struct {
			DocID string `json:"doc_id"`
		}
		if err := json.Unmarshal(args, &a); err != nil || a.DocID == "" {
			return "", fmt.Errorf("read_doc requires \"doc_id\"")
		}
		return engine.ReadDoc(a.DocID)
	case "list_docs":
		var a struct {
			Space string `json:"space"`
		}
		_ = json.Unmarshal(args, &a)
		docs := engine.ListDocs(a.Space)
		var b strings.Builder
		for _, d := range docs {
			fmt.Fprintf(&b, "- %s (doc_id: %s, space: %s) — %s\n", d.Title, d.ID, d.Space, strings.Join(d.Keywords, ", "))
		}
		if b.Len() == 0 {
			return "No docs.", nil
		}
		return b.String(), nil
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}
