package server

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
)

// RunMCPHTTP serves MCP over the Streamable HTTP transport: JSON-RPC
// messages POSTed to /mcp, responses returned as application/json.
// The server is stateless, so no SSE stream is offered (GET returns
// 405, which the spec allows). An optional bearer token protects the
// endpoint when it is exposed beyond localhost.
func RunMCPHTTP(engine Searcher, addr, token string, defaultTopK int) error {
	docs, chunks := engine.Counts()
	log.Printf("mcp: %d docs / %d chunks loaded", docs, chunks)
	log.Printf("mcp: streamable HTTP endpoint at http://%s/mcp (auth: %v)", addr, token != "")
	return http.ListenAndServe(addr, MCPHTTPHandler(engine, token, defaultTopK))
}

// MCPHTTPHandler builds the Streamable HTTP handler (exported for tests).
func MCPHTTPHandler(engine Searcher, token string, defaultTopK int) http.Handler {
	sessionID := newSessionID()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		docs, chunks := engine.Counts()
		writeJSON(w, map[string]any{"ok": true, "chunks": chunks, "docs": docs})
	})

	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			got := r.Header.Get("Authorization")
			if subtle.ConstantTimeCompare([]byte(got), []byte("Bearer "+token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		switch r.Method {
		case http.MethodPost:
			mcpPost(engine, defaultTopK, sessionID, w, r)
		case http.MethodDelete:
			// session termination: nothing server-side to clean up
			w.WriteHeader(http.StatusNoContent)
		default:
			// no server-initiated SSE stream
			w.Header().Set("Allow", "POST, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	return mux
}

func mcpPost(engine Searcher, defaultTopK int, sessionID string, w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body = bytes.TrimSpace(body)

	// batch (JSON-RPC array, used by the 2025-03-26 revision)
	if len(body) > 0 && body[0] == '[' {
		var msgs []json.RawMessage
		if err := json.Unmarshal(body, &msgs); err != nil {
			writeRPCParseError(w)
			return
		}
		var responses []*rpcResponse
		for _, m := range msgs {
			if resp := dispatch(engine, defaultTopK, m); resp != nil {
				responses = append(responses, resp)
			}
		}
		if len(responses) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSON(w, responses)
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCParseError(w)
		return
	}
	if req.Method == "initialize" {
		w.Header().Set("Mcp-Session-Id", sessionID)
	}
	resp := handle(engine, defaultTopK, &req)
	if resp == nil { // notification
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, resp)
}

func dispatch(engine Searcher, defaultTopK int, msg json.RawMessage) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return &rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}}
	}
	return handle(engine, defaultTopK, &req)
}

func writeRPCParseError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(rpcResponse{
		JSONRPC: "2.0",
		Error:   &rpcError{Code: -32700, Message: "parse error"},
	})
}

func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "static-session"
	}
	return hex.EncodeToString(b)
}
