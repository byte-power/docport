// Package search provides BM25 keyword search plus optional cosine
// vector search over the chunk index, combined with reciprocal rank
// fusion (RRF).
package search

import (
	"math"
	"sort"

	"github.com/byte-power/docport/internal/config"
	"github.com/byte-power/docport/internal/embed"
	"github.com/byte-power/docport/internal/index"
	"github.com/byte-power/docport/internal/keyword"
)

type posting struct {
	chunk int
	tf    float64
}

type Engine struct {
	index.Catalog
	Chunks []index.ChunkRecord

	ext     *keyword.Extractor
	inv     map[string][]posting
	lens    []float64
	avgLen  float64
	norms   []float64
	hasVecs bool
	embCli  *embed.Client
}

type Result struct {
	ID      string  `json:"id"`
	DocID   string  `json:"doc_id"`
	Title   string  `json:"title"`
	Section string  `json:"section,omitempty"`
	Space   string  `json:"space"`
	Path    string  `json:"path"`
	URL     string  `json:"url"`
	Score   float64 `json:"score"`
	Text    string  `json:"text"`
}

// Load builds an engine from <out>/index. Embeddings are used when the
// index contains vectors and cfg.Embedding is enabled (for the query).
func Load(cfg *config.Config) (*Engine, error) {
	chunks, err := index.LoadChunks(cfg.Output.Dir)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		Catalog: index.LoadCatalog(cfg.Output.Dir),
		Chunks:  chunks,
		inv:     map[string][]posting{},
	}

	e.ext, err = keyword.New()
	if err != nil {
		return nil, err
	}

	// build BM25 structures
	e.lens = make([]float64, len(e.Chunks))
	e.norms = make([]float64, len(e.Chunks))
	var total float64
	for i, c := range e.Chunks {
		toks := e.ext.Tokens(c.Title + "\n" + c.Section + "\n" + c.Text)
		e.lens[i] = float64(len(toks))
		total += e.lens[i]
		tf := map[string]float64{}
		for _, t := range toks {
			tf[t]++
		}
		for t, f := range tf {
			e.inv[t] = append(e.inv[t], posting{chunk: i, tf: f})
		}
		if len(c.Vector) > 0 {
			e.hasVecs = true
			var n float64
			for _, v := range c.Vector {
				n += float64(v) * float64(v)
			}
			e.norms[i] = math.Sqrt(n)
		}
	}
	e.avgLen = total / float64(len(e.Chunks))

	if e.hasVecs && cfg.Embedding.Enabled {
		e.embCli = embed.New(cfg.Embedding)
	}
	return e, nil
}

// Search runs hybrid retrieval and returns the top-k chunks.
func (e *Engine) Search(query string, k int) ([]Result, error) {
	if k <= 0 {
		k = 8
	}
	bm := e.bm25(query)

	var ranked []int
	if e.embCli != nil {
		vec, err := e.embCli.Embed([]string{query})
		if err != nil {
			// degrade to keyword-only rather than failing the query
			ranked = topIdx(bm, 100)
		} else {
			cos := e.cosine(vec[0])
			ranked = rrf(topIdx(bm, 100), topIdx(cos, 100))
		}
	} else {
		ranked = topIdx(bm, 100)
	}

	if len(ranked) > k {
		ranked = ranked[:k]
	}
	out := make([]Result, 0, len(ranked))
	for rank, ci := range ranked {
		c := e.Chunks[ci]
		out = append(out, Result{
			ID: c.ID, DocID: c.DocID, Title: c.Title, Section: c.Section,
			Space: c.Space, Path: c.Path, URL: c.URL,
			Score: 1.0 / float64(rank+1),
			Text:  c.Text,
		})
	}
	return out, nil
}

func (e *Engine) bm25(query string) []float64 {
	const k1, b = 1.2, 0.75
	scores := make([]float64, len(e.Chunks))
	n := float64(len(e.Chunks))
	for _, t := range e.ext.Tokens(query) {
		posts := e.inv[t]
		if len(posts) == 0 {
			continue
		}
		df := float64(len(posts))
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))
		for _, p := range posts {
			denom := p.tf + k1*(1-b+b*e.lens[p.chunk]/e.avgLen)
			scores[p.chunk] += idf * p.tf * (k1 + 1) / denom
		}
	}
	return scores
}

func (e *Engine) cosine(q []float32) []float64 {
	var qn float64
	for _, v := range q {
		qn += float64(v) * float64(v)
	}
	qn = math.Sqrt(qn)
	scores := make([]float64, len(e.Chunks))
	if qn == 0 {
		return scores
	}
	for i, c := range e.Chunks {
		if len(c.Vector) != len(q) || e.norms[i] == 0 {
			continue
		}
		var dot float64
		for j, v := range c.Vector {
			dot += float64(v) * float64(q[j])
		}
		scores[i] = dot / (qn * e.norms[i])
	}
	return scores
}

// topIdx returns indices of the highest-scoring chunks (score > 0).
func topIdx(scores []float64, limit int) []int {
	idx := make([]int, 0, len(scores))
	for i, s := range scores {
		if s > 0 {
			idx = append(idx, i)
		}
	}
	sort.Slice(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
	if len(idx) > limit {
		idx = idx[:limit]
	}
	return idx
}

// rrf merges ranked lists with reciprocal rank fusion.
func rrf(lists ...[]int) []int {
	const c = 60.0
	score := map[int]float64{}
	for _, l := range lists {
		for rank, id := range l {
			score[id] += 1.0 / (c + float64(rank+1))
		}
	}
	out := make([]int, 0, len(score))
	for id := range score {
		out = append(out, id)
	}
	sort.Slice(out, func(a, b int) bool { return score[out[a]] > score[out[b]] })
	return out
}

// Counts reports index sizes for health reporting.
func (e *Engine) Counts() (docs, chunks int) {
	return len(e.Docs), len(e.Chunks)
}
