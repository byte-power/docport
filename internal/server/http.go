// Package server exposes the search engine to agents: a local HTTP API
// and an MCP stdio server.
package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/byte-power/docport/internal/index"
	"github.com/byte-power/docport/internal/search"
)

// Searcher is the retrieval backend contract shared by the built-in
// BM25/vector engine and the bleve engine.
type Searcher interface {
	Search(query string, k int) ([]search.Result, error)
	ReadDoc(idOrPath string) (string, error)
	ListDocs(space string) []index.DocRecord
	Counts() (docs, chunks int)
}

func RunHTTP(engine Searcher, addr string, defaultTopK int) error {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		docs, chunks := engine.Counts()
		writeJSON(w, map[string]any{"ok": true, "chunks": chunks, "docs": docs})
	})

	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, `missing query parameter "q"`, http.StatusBadRequest)
			return
		}
		k := defaultTopK
		if v, err := strconv.Atoi(r.URL.Query().Get("k")); err == nil && v > 0 {
			k = v
		}
		results, err := engine.Search(q, k)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"query": q, "results": results})
	})

	mux.HandleFunc("GET /doc", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, `missing query parameter "id"`, http.StatusBadRequest)
			return
		}
		md, err := engine.ReadDoc(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(md))
	})

	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, engine.ListDocs(r.URL.Query().Get("space")))
	})

	docs, chunks := engine.Counts()
	log.Printf("serve: %d docs / %d chunks loaded", docs, chunks)
	log.Printf("serve: listening on http://%s  (endpoints: /search /doc /docs /healthz)", addr)
	return http.ListenAndServe(addr, mux)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
