package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/byte-power/docport/internal/index"
	"github.com/byte-power/docport/internal/search"
)

// stubSearcher is a minimal Searcher for transport tests.
type stubSearcher struct{}

func (stubSearcher) Search(query string, k int) ([]search.Result, error) {
	return []search.Result{{
		ID: "1#0", DocID: "1", Title: "测试文档", Space: "DEV",
		Path: "docs/DEV/t-1.md", URL: "https://x", Score: 1, Text: "hit for " + query,
	}}, nil
}

func (stubSearcher) ReadDoc(idOrPath string) (string, error) { return "# doc " + idOrPath, nil }

func (stubSearcher) ListDocs(space string) []index.DocRecord {
	return []index.DocRecord{{ID: "1", Title: "测试文档", Space: "DEV"}}
}

func (stubSearcher) Counts() (int, int) { return 1, 1 }

func post(t *testing.T, srv *httptest.Server, token, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func decodeRPC(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return m
}

func newTestServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(MCPHTTPHandler(stubSearcher{}, token, 8))
	t.Cleanup(srv.Close)
	return srv
}

func TestMCPHTTPInitialize(t *testing.T) {
	srv := newTestServer(t, "")
	resp := post(t, srv, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("Mcp-Session-Id") == "" {
		t.Error("initialize response missing Mcp-Session-Id header")
	}
	m := decodeRPC(t, resp)
	result := m["result"].(map[string]any)
	if result["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v, want echo of client version", result["protocolVersion"])
	}
	if result["serverInfo"].(map[string]any)["name"] != "docport" {
		t.Errorf("unexpected serverInfo: %v", result["serverInfo"])
	}
}

func TestMCPHTTPToolCall(t *testing.T) {
	srv := newTestServer(t, "")
	resp := post(t, srv, "",
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_docs","arguments":{"query":"部署"}}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	m := decodeRPC(t, resp)
	text := m["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "hit for 部署") || !strings.Contains(text, "测试文档") {
		t.Errorf("unexpected tool result: %q", text)
	}
}

func TestMCPHTTPNotification(t *testing.T) {
	srv := newTestServer(t, "")
	resp := post(t, srv, "", `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("notification status = %d, want 202", resp.StatusCode)
	}
}

func TestMCPHTTPBatch(t *testing.T) {
	srv := newTestServer(t, "")
	resp := post(t, srv, "", `[
		{"jsonrpc":"2.0","id":1,"method":"tools/list"},
		{"jsonrpc":"2.0","method":"notifications/initialized"},
		{"jsonrpc":"2.0","id":2,"method":"ping"}
	]`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var arr []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&arr); err != nil {
		t.Fatalf("decode batch: %v", err)
	}
	if len(arr) != 2 {
		t.Fatalf("batch returned %d responses, want 2 (notification excluded)", len(arr))
	}
}

func TestMCPHTTPAuth(t *testing.T) {
	srv := newTestServer(t, "sekret")
	if resp := post(t, srv, "", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: status = %d, want 401", resp.StatusCode)
	}
	if resp := post(t, srv, "wrong", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want 401", resp.StatusCode)
	}
	if resp := post(t, srv, "sekret", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); resp.StatusCode != http.StatusOK {
		t.Errorf("correct token: status = %d, want 200", resp.StatusCode)
	}
}

func TestMCPHTTPMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, "")
	resp, err := srv.Client().Get(srv.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /mcp status = %d, want 405", resp.StatusCode)
	}
}

func TestMCPHTTPParseError(t *testing.T) {
	srv := newTestServer(t, "")
	resp := post(t, srv, "", `{not json`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	m := decodeRPC(t, resp)
	if code := m["error"].(map[string]any)["code"].(float64); code != -32700 {
		t.Errorf("error code = %v, want -32700", code)
	}
}
