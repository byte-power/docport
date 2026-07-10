package fulltext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/byte-power/docport/internal/config"
)

func writeIndexFixture(t *testing.T, dir string) {
	t.Helper()
	idxDir := filepath.Join(dir, "index")
	if err := os.MkdirAll(idxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	chunks := `{"id":"1001#0","doc_id":"1001","space":"DEV","title":"服务部署指南","section":"前置条件","path":"docs/DEV/服务部署指南-1001.md","url":"https://c.example.com/1001","text":"安装 Docker 与 docker-compose，确保 Kubernetes 集群可用。","keywords":["docker","kubernetes"]}
{"id":"1001#1","doc_id":"1001","space":"DEV","title":"服务部署指南","section":"回滚流程","path":"docs/DEV/服务部署指南-1001.md","url":"https://c.example.com/1001","text":"使用 helm rollback 回滚到上一版本。"}
{"id":"1002#0","doc_id":"1002","space":"OPS","title":"数据库备份策略","section":"","path":"docs/OPS/数据库备份策略-1002.md","url":"https://c.example.com/1002","text":"PostgreSQL 每日全量备份，binlog 增量备份保留 7 天。","keywords":["postgresql","备份"]}
`
	if err := os.WriteFile(filepath.Join(idxDir, "chunks.jsonl"), []byte(chunks), 0o644); err != nil {
		t.Fatal(err)
	}
	docs := `[
{"id":"1001","path":"docs/DEV/服务部署指南-1001.md","title":"服务部署指南","space":"DEV","chunks":2},
{"id":"1002","path":"docs/OPS/数据库备份策略-1002.md","title":"数据库备份策略","space":"OPS","chunks":1}
]`
	if err := os.WriteFile(filepath.Join(idxDir, "docs.json"), []byte(docs), 0o644); err != nil {
		t.Fatal(err)
	}
}

func loadFixtureEngine(t *testing.T) *Engine {
	t.Helper()
	dir := t.TempDir()
	writeIndexFixture(t, dir)
	cfg := &config.Config{}
	cfg.Output.Dir = dir
	engine, err := Load(cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return engine
}

func TestSearch(t *testing.T) {
	engine := loadFixtureEngine(t)

	tests := []struct {
		name      string
		query     string
		wantFirst string // expected chunk ID of the top hit
	}{
		{"chinese text match", "回滚", "1001#1"},
		{"chinese title match", "数据库备份", "1002#0"},
		{"english term", "kubernetes", "1001#0"},
		{"mixed language", "docker 安装", "1001#0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := engine.Search(tt.query, 5)
			if err != nil {
				t.Fatalf("Search(%q): %v", tt.query, err)
			}
			if len(results) == 0 {
				t.Fatalf("Search(%q): no results", tt.query)
			}
			if results[0].ID != tt.wantFirst {
				t.Errorf("Search(%q) top hit = %s (%s), want %s",
					tt.query, results[0].ID, results[0].Title, tt.wantFirst)
			}
			if results[0].Score <= 0 {
				t.Errorf("Search(%q) top hit score = %v, want > 0", tt.query, results[0].Score)
			}
			if results[0].Text == "" || results[0].URL == "" {
				t.Errorf("Search(%q) result missing text/url: %+v", tt.query, results[0])
			}
		})
	}
}

func TestSearchNoResults(t *testing.T) {
	engine := loadFixtureEngine(t)
	results, err := engine.Search("完全不存在的词汇组合xyzzy", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestCountsAndCatalog(t *testing.T) {
	engine := loadFixtureEngine(t)
	docs, chunks := engine.Counts()
	if docs != 2 || chunks != 3 {
		t.Errorf("Counts() = (%d, %d), want (2, 3)", docs, chunks)
	}
	if got := engine.ListDocs("OPS"); len(got) != 1 || got[0].ID != "1002" {
		t.Errorf("ListDocs(OPS) = %+v, want doc 1002", got)
	}
}
