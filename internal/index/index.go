// Package index builds the retrieval index: chunks.jsonl (chunk text +
// keywords + optional vectors), docs.json (document catalog) and a
// human/agent-readable docs/INDEX.md.
package index

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/byte-power/docport/internal/chunk"
	"github.com/byte-power/docport/internal/clean"
	"github.com/byte-power/docport/internal/config"
	"github.com/byte-power/docport/internal/embed"
	"github.com/byte-power/docport/internal/keyword"
)

// ChunkRecord is one line of index/chunks.jsonl.
type ChunkRecord struct {
	ID       string    `json:"id"` // <pageID>#<ord>
	DocID    string    `json:"doc_id"`
	Space    string    `json:"space"`
	Title    string    `json:"title"`
	Section  string    `json:"section,omitempty"`
	Path     string    `json:"path"` // markdown file, relative to output dir
	URL      string    `json:"url"`
	Text     string    `json:"text"`
	Keywords []string  `json:"keywords,omitempty"`
	Vector   []float32 `json:"vector,omitempty"`
}

// DocRecord is one entry of index/docs.json.
type DocRecord struct {
	ID       string   `json:"id"`
	Path     string   `json:"path"`
	Title    string   `json:"title"`
	Space    string   `json:"space"`
	URL      string   `json:"url"`
	Updated  string   `json:"updated"`
	Version  int      `json:"version"`
	Labels   []string `json:"labels,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
	Chunks   int      `json:"chunks"`
}

type Stats struct {
	Docs, Chunks, Embedded int
}

func Run(cfg *config.Config, withEmbeddings bool) (Stats, error) {
	var st Stats
	docsDir := filepath.Join(cfg.Output.Dir, "docs")
	idxDir := filepath.Join(cfg.Output.Dir, "index")
	if err := os.MkdirAll(idxDir, 0o755); err != nil {
		return st, err
	}

	var paths []string
	err := filepath.WalkDir(docsDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") && d.Name() != "INDEX.md" {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return st, fmt.Errorf("scan %s (run `clean` first?): %w", docsDir, err)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return st, fmt.Errorf("no markdown docs under %s", docsDir)
	}

	log.Printf("index: loading segmenter dictionary ...")
	ext, err := keyword.New()
	if err != nil {
		return st, fmt.Errorf("init segmenter: %w", err)
	}

	// 1. parse docs, chunk, tokenize
	type docState struct {
		meta   clean.DocMeta
		rel    string
		chunks []chunk.Chunk
		tokens []string
	}
	var docs []docState
	for _, p := range paths {
		meta, body, err := clean.ParseDoc(p)
		if err != nil {
			log.Printf("index: skip %s: %v", p, err)
			continue
		}
		rel, _ := filepath.Rel(cfg.Output.Dir, p)
		docs = append(docs, docState{
			meta:   meta,
			rel:    rel,
			chunks: chunk.Split(body, cfg.Index.ChunkSize),
			tokens: ext.Tokens(meta.Title + "\n" + body),
		})
	}

	// 2. corpus TF-IDF keywords per document
	corpus := make([][]string, len(docs))
	for i, d := range docs {
		corpus[i] = d.tokens
	}
	docKeywords := keyword.TFIDF(corpus, cfg.Index.DocKeywords)

	// 3. build chunk records; chunk keywords = doc keywords present in chunk
	var records []ChunkRecord
	var docRecords []DocRecord
	for i, d := range docs {
		kwTerms := make([]string, 0, len(docKeywords[i]))
		for _, k := range docKeywords[i] {
			kwTerms = append(kwTerms, k.Term)
		}
		for ord, c := range d.chunks {
			records = append(records, ChunkRecord{
				ID:       fmt.Sprintf("%s#%d", d.meta.PageID, ord),
				DocID:    d.meta.PageID,
				Space:    d.meta.Space,
				Title:    d.meta.Title,
				Section:  c.Section,
				Path:     d.rel,
				URL:      d.meta.URL,
				Text:     c.Text,
				Keywords: presentIn(kwTerms, ext.Tokens(c.Text), cfg.Index.ChunkKeyword),
			})
		}
		docRecords = append(docRecords, DocRecord{
			ID: d.meta.PageID, Path: d.rel, Title: d.meta.Title,
			Space: d.meta.Space, URL: d.meta.URL, Updated: d.meta.Updated,
			Version: d.meta.Version, Labels: d.meta.Labels,
			Keywords: kwTerms, Chunks: len(d.chunks),
		})
	}
	st.Docs, st.Chunks = len(docRecords), len(records)

	// 4. optional embeddings
	if withEmbeddings && cfg.Embedding.Enabled {
		cli := embed.New(cfg.Embedding)
		for i := 0; i < len(records); i += cfg.Embedding.Batch {
			end := min(i+cfg.Embedding.Batch, len(records))
			texts := make([]string, 0, end-i)
			for _, r := range records[i:end] {
				texts = append(texts, embedInput(r))
			}
			vecs, err := cli.Embed(texts)
			if err != nil {
				return st, fmt.Errorf("embed batch %d-%d: %w", i, end, err)
			}
			for j, v := range vecs {
				records[i+j].Vector = v
			}
			st.Embedded = end
			log.Printf("index: embedded %d/%d chunks", end, len(records))
		}
	}

	// 5. write outputs
	if err := writeJSONL(filepath.Join(idxDir, "chunks.jsonl"), records); err != nil {
		return st, err
	}
	if err := writeJSON(filepath.Join(idxDir, "docs.json"), docRecords); err != nil {
		return st, err
	}
	if err := writeCatalog(filepath.Join(docsDir, "INDEX.md"), docRecords); err != nil {
		return st, err
	}
	return st, nil
}

func embedInput(r ChunkRecord) string {
	head := r.Title
	if r.Section != "" {
		head += " > " + r.Section
	}
	return head + "\n" + r.Text
}

func presentIn(docKw, chunkTokens []string, max int) []string {
	set := make(map[string]struct{}, len(chunkTokens))
	for _, t := range chunkTokens {
		set[t] = struct{}{}
	}
	var out []string
	for _, k := range docKw {
		if _, ok := set[k]; ok {
			out = append(out, k)
			if len(out) == max {
				break
			}
		}
	}
	return out
}

func writeJSONL(path string, records []ChunkRecord) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return w.Flush()
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// writeCatalog emits docs/INDEX.md — a per-space catalog agents can read
// directly (and that can be referenced from CLAUDE.md / AGENTS.md).
func writeCatalog(path string, docs []DocRecord) error {
	bySpace := map[string][]DocRecord{}
	for _, d := range docs {
		bySpace[d.Space] = append(bySpace[d.Space], d)
	}
	spaces := make([]string, 0, len(bySpace))
	for s := range bySpace {
		spaces = append(spaces, s)
	}
	sort.Strings(spaces)

	var b strings.Builder
	b.WriteString("# 文档目录（自动生成）\n\n")
	b.WriteString("> 由 docport 从 Confluence 导出。每行格式：标题 — 关键词。\n")
	for _, s := range spaces {
		fmt.Fprintf(&b, "\n## Space: %s\n\n", s)
		ds := bySpace[s]
		sort.Slice(ds, func(i, j int) bool { return ds[i].Title < ds[j].Title })
		for _, d := range ds {
			relToDocs := strings.TrimPrefix(d.Path, "docs/")
			kw := strings.Join(d.Keywords, ", ")
			fmt.Fprintf(&b, "- [%s](%s) — %s\n", d.Title, relToDocs, kw)
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
