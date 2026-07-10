package export

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/byte-power/docport/internal/config"
)

func TestParsePageRef(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    PageRef
		wantErr bool
	}{
		{
			name: "bare id",
			in:   "123456",
			want: PageRef{ID: "123456"},
		},
		{
			name: "viewpage url",
			in:   "https://wiki.example.com/pages/viewpage.action?pageId=123456",
			want: PageRef{ID: "123456"},
		},
		{
			name: "viewpage url with context path",
			in:   "https://wiki.example.com/confluence/pages/viewpage.action?pageId=98765",
			want: PageRef{ID: "98765"},
		},
		{
			name: "viewpage by space and title",
			in:   "https://wiki.example.com/pages/viewpage.action?spaceKey=DEV&title=Some+Page",
			want: PageRef{Space: "DEV", Title: "Some Page"},
		},
		{
			name: "display url",
			in:   "https://wiki.example.com/display/DEV/%E9%83%A8%E7%BD%B2%E6%8C%87%E5%8D%97",
			want: PageRef{Space: "DEV", Title: "部署指南"},
		},
		{
			name: "display url with plus",
			in:   "https://wiki.example.com/display/OPS/Backup+Policy",
			want: PageRef{Space: "OPS", Title: "Backup Policy"},
		},
		{
			name: "new-ui spaces url",
			in:   "https://wiki.example.com/spaces/DEV/pages/123456/Some+Title",
			want: PageRef{ID: "123456"},
		},
		{
			name:    "garbage",
			in:      "not a ref",
			wantErr: true,
		},
		{
			name:    "url without page info",
			in:      "https://wiki.example.com/dashboard.action",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePageRef(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParsePageRef(%q) = %+v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePageRef(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParsePageRef(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func mockPage(id, title string, version int) map[string]any {
	return map[string]any{
		"id":    id,
		"type":  "page",
		"title": title,
		"body":  map[string]any{"storage": map[string]any{"value": "<p>hello</p>"}},
		"version": map[string]any{
			"number": version,
			"when":   "2026-07-01T12:00:00.000+08:00",
		},
		"space":  map[string]any{"key": "DEV"},
		"_links": map[string]any{"webui": "/pages/viewpage.action?pageId=" + id},
	}
}

// mockConfluence serves a page tree: 1001 -> (2001 -> 3001, 2002).
func mockConfluence(t *testing.T) *httptest.Server {
	t.Helper()
	pages := map[string]map[string]any{
		"1001": mockPage("1001", "部署指南", 7),
		"2001": mockPage("2001", "子页面A", 2),
		"2002": mockPage("2002", "子页面B", 1),
		"3001": mockPage("3001", "孙页面", 4),
	}
	children := map[string][]string{
		"1001": {"2001", "2002"},
		"2001": {"3001"},
	}
	listing := func(ids []string) map[string]any {
		results := make([]any, 0, len(ids))
		for _, id := range ids {
			results = append(results, pages[id])
		}
		return map[string]any{"results": results, "size": len(results), "limit": 50}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /rest/api/content/{id}", func(w http.ResponseWriter, r *http.Request) {
		if p, ok := pages[r.PathValue("id")]; ok {
			_ = json.NewEncoder(w).Encode(p)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /rest/api/content/{id}/child/page", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(listing(children[r.PathValue("id")]))
	})
	mux.HandleFunc("GET /rest/api/content", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("spaceKey") == "DEV" && r.URL.Query().Get("title") == "部署指南" {
			_ = json.NewEncoder(w).Encode(listing([]string{"1001"}))
			return
		}
		_ = json.NewEncoder(w).Encode(listing(nil))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRunOne(t *testing.T) {
	srv := mockConfluence(t)

	tests := []struct {
		name string
		ref  func(baseURL string) string
	}{
		{"by id", func(string) string { return "1001" }},
		{"by viewpage url", func(base string) string { return base + "/pages/viewpage.action?pageId=1001" }},
		{"by display url", func(base string) string {
			return base + "/display/DEV/%E9%83%A8%E7%BD%B2%E6%8C%87%E5%8D%97"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := &config.Config{}
			cfg.Confluence.BaseURL = srv.URL
			cfg.Confluence.TimeoutSec = 5
			cfg.Output.Dir = dir

			if err := RunOne(cfg, tt.ref(srv.URL), false); err != nil {
				t.Fatalf("RunOne: %v", err)
			}
			b, err := os.ReadFile(filepath.Join(dir, "raw", "DEV", "1001.json"))
			if err != nil {
				t.Fatalf("raw file not written: %v", err)
			}
			var raw RawPage
			if err := json.Unmarshal(b, &raw); err != nil {
				t.Fatalf("bad raw json: %v", err)
			}
			if raw.Page.Title != "部署指南" || raw.Space != "DEV" || raw.Page.Version.Number != 7 {
				t.Errorf("unexpected raw page: space=%s title=%s v%d", raw.Space, raw.Page.Title, raw.Page.Version.Number)
			}
			manifest := loadManifest(filepath.Join(dir, "raw", "manifest.json"))
			if manifest["1001"].Version != 7 {
				t.Errorf("manifest not updated: %+v", manifest["1001"])
			}
			// non-recursive: children must NOT be exported
			if _, err := os.Stat(filepath.Join(dir, "raw", "DEV", "2001.json")); err == nil {
				t.Error("child page exported without -recursive")
			}
		})
	}
}

func TestRunOneRecursive(t *testing.T) {
	srv := mockConfluence(t)
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Confluence.BaseURL = srv.URL
	cfg.Confluence.TimeoutSec = 5
	cfg.Output.Dir = dir

	if err := RunOne(cfg, "1001", true); err != nil {
		t.Fatalf("RunOne recursive: %v", err)
	}
	wantVersions := map[string]int{"1001": 7, "2001": 2, "2002": 1, "3001": 4}
	manifest := loadManifest(filepath.Join(dir, "raw", "manifest.json"))
	if len(manifest) != len(wantVersions) {
		t.Errorf("manifest has %d entries, want %d: %+v", len(manifest), len(wantVersions), manifest)
	}
	for id, v := range wantVersions {
		if _, err := os.Stat(filepath.Join(dir, "raw", "DEV", id+".json")); err != nil {
			t.Errorf("page %s not exported: %v", id, err)
		}
		if manifest[id].Version != v {
			t.Errorf("manifest[%s].Version = %d, want %d", id, manifest[id].Version, v)
		}
	}

	// second run: everything unchanged, nothing rewritten
	before, _ := os.Stat(filepath.Join(dir, "raw", "DEV", "3001.json"))
	if err := RunOne(cfg, "1001", true); err != nil {
		t.Fatalf("RunOne recursive (2nd): %v", err)
	}
	after, _ := os.Stat(filepath.Join(dir, "raw", "DEV", "3001.json"))
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("unchanged page was rewritten on second run")
	}
}
