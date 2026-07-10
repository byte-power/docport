package index

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadChunks reads <dataDir>/index/chunks.jsonl into memory.
func LoadChunks(dataDir string) ([]ChunkRecord, error) {
	f, err := os.Open(filepath.Join(dataDir, "index", "chunks.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("open chunk index (run `index` first?): %w", err)
	}
	defer f.Close()
	var chunks []ChunkRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	for sc.Scan() {
		var r ChunkRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, err
		}
		chunks = append(chunks, r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("chunk index is empty")
	}
	return chunks, nil
}

// LoadCatalog reads <dataDir>/index/docs.json into a Catalog.
func LoadCatalog(dataDir string) Catalog {
	c := Catalog{Dir: dataDir}
	if b, err := os.ReadFile(filepath.Join(dataDir, "index", "docs.json")); err == nil {
		_ = json.Unmarshal(b, &c.Docs)
	}
	return c
}

// Catalog is the in-memory document catalog shared by search backends.
type Catalog struct {
	Dir  string
	Docs []DocRecord
}

// ReadDoc returns the full Markdown of a document by page ID or path.
func (c *Catalog) ReadDoc(idOrPath string) (string, error) {
	for _, d := range c.Docs {
		if d.ID == idOrPath || d.Path == idOrPath {
			b, err := os.ReadFile(filepath.Join(c.Dir, d.Path))
			if err != nil {
				return "", err
			}
			return string(b), nil
		}
	}
	return "", fmt.Errorf("doc not found: %s", idOrPath)
}

// ListDocs returns the catalog, optionally filtered by space key.
func (c *Catalog) ListDocs(space string) []DocRecord {
	if space == "" {
		return c.Docs
	}
	var out []DocRecord
	for _, d := range c.Docs {
		if strings.EqualFold(d.Space, space) {
			out = append(out, d)
		}
	}
	return out
}
