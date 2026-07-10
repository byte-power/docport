// Package fulltext is a search backend built on bleve: all chunk text
// is loaded into memory and indexed in an in-memory bleve index with a
// CJK-aware analyzer. Pure Go, no embeddings, no external services.
package fulltext

import (
	"fmt"
	"log"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/lang/cjk"
	"github.com/blevesearch/bleve/v2/mapping"

	"github.com/byte-power/docport/internal/config"
	"github.com/byte-power/docport/internal/index"
	"github.com/byte-power/docport/internal/search"
)

// chunkDoc is the searchable projection of a chunk. The full records
// stay in Engine.chunks; bleve only stores the inverted index.
type chunkDoc struct {
	Title    string   `json:"title"`
	Section  string   `json:"section"`
	Text     string   `json:"text"`
	Keywords []string `json:"keywords"`
}

// fieldBoosts weights query matches per field.
var fieldBoosts = map[string]float64{
	"title":    3,
	"keywords": 2,
	"section":  2,
	"text":     1,
}

type Engine struct {
	index.Catalog
	chunks map[string]index.ChunkRecord // by chunk ID
	total  int
	idx    bleve.Index
}

// Load reads the chunk index and builds an in-memory bleve index.
func Load(cfg *config.Config) (*Engine, error) {
	chunks, err := index.LoadChunks(cfg.Output.Dir)
	if err != nil {
		return nil, err
	}
	idx, err := bleve.NewMemOnly(buildMapping())
	if err != nil {
		return nil, fmt.Errorf("create bleve index: %w", err)
	}

	log.Printf("fulltext: indexing %d chunks in memory ...", len(chunks))
	byID := make(map[string]index.ChunkRecord, len(chunks))
	batch := idx.NewBatch()
	for _, c := range chunks {
		byID[c.ID] = c
		err := batch.Index(c.ID, chunkDoc{
			Title:    c.Title,
			Section:  c.Section,
			Text:     c.Text,
			Keywords: c.Keywords,
		})
		if err != nil {
			return nil, err
		}
		if batch.Size() >= 500 {
			if err := idx.Batch(batch); err != nil {
				return nil, err
			}
			batch = idx.NewBatch()
		}
	}
	if batch.Size() > 0 {
		if err := idx.Batch(batch); err != nil {
			return nil, err
		}
	}
	log.Printf("fulltext: index ready")

	return &Engine{
		Catalog: index.LoadCatalog(cfg.Output.Dir),
		chunks:  byID,
		total:   len(chunks),
		idx:     idx,
	}, nil
}

// buildMapping indexes every field with the CJK analyzer (bigram
// tokenization for Chinese, standard tokens for Latin text).
func buildMapping() mapping.IndexMapping {
	im := bleve.NewIndexMapping()
	im.DefaultAnalyzer = cjk.AnalyzerName

	doc := bleve.NewDocumentMapping()
	for field := range fieldBoosts {
		fm := bleve.NewTextFieldMapping()
		fm.Analyzer = cjk.AnalyzerName
		fm.Store = false
		doc.AddFieldMappingsAt(field, fm)
	}
	im.DefaultMapping = doc
	return im
}

// Search runs a boosted multi-field match query.
func (e *Engine) Search(query string, k int) ([]search.Result, error) {
	if k <= 0 {
		k = 8
	}
	dis := bleve.NewDisjunctionQuery()
	for field, boost := range fieldBoosts {
		mq := bleve.NewMatchQuery(query)
		mq.SetField(field)
		mq.SetBoost(boost)
		dis.AddQuery(mq)
	}
	req := bleve.NewSearchRequestOptions(dis, k, 0, false)
	res, err := e.idx.Search(req)
	if err != nil {
		return nil, err
	}
	out := make([]search.Result, 0, len(res.Hits))
	for _, hit := range res.Hits {
		c, ok := e.chunks[hit.ID]
		if !ok {
			continue
		}
		out = append(out, search.Result{
			ID: c.ID, DocID: c.DocID, Title: c.Title, Section: c.Section,
			Space: c.Space, Path: c.Path, URL: c.URL,
			Score: hit.Score,
			Text:  c.Text,
		})
	}
	return out, nil
}

// Counts reports index sizes for health reporting.
func (e *Engine) Counts() (docs, chunks int) {
	return len(e.Docs), e.total
}
