// Package export pulls pages from Confluence into <out>/raw/<SPACE>/<id>.json,
// incrementally: pages whose version is unchanged since the last run are skipped.
package export

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/byte-power/docport/internal/config"
	"github.com/byte-power/docport/internal/source/confluence"
)

// RawPage is what gets written to raw/<space>/<id>.json.
type RawPage struct {
	Space     string          `json:"space"`
	URL       string          `json:"url"`
	FetchedAt string          `json:"fetched_at"`
	Page      confluence.Page `json:"page"`
}

type manifestEntry struct {
	Version int    `json:"version"`
	Space   string `json:"space"`
	Title   string `json:"title"`
}

type Stats struct {
	Fetched, Skipped int
}

func loadManifest(path string) map[string]manifestEntry {
	manifest := map[string]manifestEntry{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &manifest)
	}
	return manifest
}

func saveManifest(path string, manifest map[string]manifestEntry) error {
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// writePage stores one page under raw/<space>/ and records it in the manifest.
func writePage(cli *confluence.Client, rawDir, space string, p confluence.Page, manifest map[string]manifestEntry) (string, error) {
	dir := filepath.Join(rawDir, space)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	raw := RawPage{
		Space:     space,
		URL:       cli.BaseURL() + p.Links.WebUI,
		FetchedAt: time.Now().Format(time.RFC3339),
		Page:      p,
	}
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, p.ID+".json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	manifest[p.ID] = manifestEntry{Version: p.Version.Number, Space: space, Title: p.Title}
	return path, nil
}

func Run(cfg *config.Config) (Stats, error) {
	var st Stats
	cli := confluence.New(cfg.Confluence)
	rawDir := filepath.Join(cfg.Output.Dir, "raw")
	manifestPath := filepath.Join(rawDir, "manifest.json")
	manifest := loadManifest(manifestPath)

	spaces, err := cli.Spaces()
	if err != nil {
		return st, fmt.Errorf("list spaces: %w", err)
	}
	log.Printf("export: %d space(s): %v", len(spaces), spaces)

	for _, space := range spaces {
		err := cli.WalkPages(space, func(p confluence.Page) error {
			if prev, ok := manifest[p.ID]; ok && prev.Version == p.Version.Number {
				st.Skipped++
				return nil
			}
			if _, err := writePage(cli, rawDir, space, p, manifest); err != nil {
				return err
			}
			st.Fetched++
			log.Printf("export: [%s] %s (v%d)", space, p.Title, p.Version.Number)
			return nil
		})
		if err != nil {
			return st, fmt.Errorf("space %s: %w", space, err)
		}
	}

	if err := saveManifest(manifestPath, manifest); err != nil {
		return st, err
	}
	return st, nil
}

// PageRef identifies a single page: either by numeric ID, or by
// space key + title (from a /display/ URL).
type PageRef struct {
	ID    string
	Space string
	Title string
}

// ParsePageRef accepts a bare page ID or a Confluence page URL:
//
//	123456
//	https://host/pages/viewpage.action?pageId=123456
//	https://host/display/SPACE/Page+Title
//	https://host/spaces/SPACE/pages/123456/Page+Title
func ParsePageRef(ref string) (PageRef, error) {
	ref = strings.TrimSpace(ref)
	if ref != "" && strings.IndexFunc(ref, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
		return PageRef{ID: ref}, nil
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" {
		return PageRef{}, fmt.Errorf("not a page ID or URL: %q", ref)
	}
	if id := u.Query().Get("pageId"); id != "" {
		return PageRef{ID: id}, nil
	}
	// viewpage.action?spaceKey=X&title=Y
	if sp, ti := u.Query().Get("spaceKey"), u.Query().Get("title"); sp != "" && ti != "" {
		return PageRef{Space: sp, Title: ti}, nil
	}
	segs := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	for i, s := range segs {
		switch s {
		case "display": // /display/SPACE/Page+Title
			if i+2 < len(segs) {
				title, err := decodeTitle(strings.Join(segs[i+2:], "/"))
				if err != nil {
					return PageRef{}, err
				}
				return PageRef{Space: segs[i+1], Title: title}, nil
			}
		case "pages": // /spaces/SPACE/pages/123456/Title
			if i+1 < len(segs) && isDigits(segs[i+1]) {
				return PageRef{ID: segs[i+1]}, nil
			}
		}
	}
	return PageRef{}, fmt.Errorf("cannot extract a page ID from URL: %q", ref)
}

func isDigits(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

func decodeTitle(seg string) (string, error) {
	// display URLs encode spaces as '+'
	t, err := url.PathUnescape(strings.ReplaceAll(seg, "+", " "))
	if err != nil {
		return "", fmt.Errorf("decode page title %q: %w", seg, err)
	}
	return t, nil
}

// RunOne exports a single page referenced by ID or URL; with recursive
// set it also walks and exports every descendant page (BFS over
// child/page). Pages whose version is unchanged are skipped.
func RunOne(cfg *config.Config, ref string, recursive bool) error {
	pr, err := ParsePageRef(ref)
	if err != nil {
		return err
	}
	cli := confluence.New(cfg.Confluence)

	var root confluence.Page
	if pr.ID != "" {
		root, err = cli.GetPage(pr.ID)
	} else {
		root, err = cli.FindPage(pr.Space, pr.Title)
	}
	if err != nil {
		return fmt.Errorf("fetch page %q: %w", ref, err)
	}
	rootSpace := root.Space.Key
	if rootSpace == "" {
		rootSpace = pr.Space
	}
	if rootSpace == "" {
		rootSpace = "UNKNOWN"
	}

	rawDir := filepath.Join(cfg.Output.Dir, "raw")
	manifestPath := filepath.Join(rawDir, "manifest.json")
	manifest := loadManifest(manifestPath)

	var st Stats
	queue := []confluence.Page{root}
	visited := map[string]bool{root.ID: true}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		space := p.Space.Key
		if space == "" {
			space = rootSpace
		}
		rawPath := filepath.Join(rawDir, space, p.ID+".json")
		if prev, ok := manifest[p.ID]; ok && prev.Version == p.Version.Number && fileExists(rawPath) {
			st.Skipped++
			log.Printf("export: [%s] %s (v%d, id=%s) unchanged, skipped", space, p.Title, p.Version.Number, p.ID)
		} else {
			path, err := writePage(cli, rawDir, space, p, manifest)
			if err != nil {
				return err
			}
			st.Fetched++
			log.Printf("export: [%s] %s (v%d, id=%s) -> %s", space, p.Title, p.Version.Number, p.ID, path)
		}
		if !recursive {
			break
		}
		err := cli.WalkChildPages(p.ID, func(child confluence.Page) error {
			if !visited[child.ID] {
				visited[child.ID] = true
				queue = append(queue, child)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("list children of %s (%s): %w", p.Title, p.ID, err)
		}
	}

	if err := saveManifest(manifestPath, manifest); err != nil {
		return err
	}
	log.Printf("export done: %d fetched, %d unchanged (skipped)", st.Fetched, st.Skipped)
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
