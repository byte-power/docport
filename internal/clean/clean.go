package clean

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/byte-power/docport/internal/config"
	"github.com/byte-power/docport/internal/export"
)

// DocMeta is the YAML frontmatter of a cleaned Markdown document.
type DocMeta struct {
	Title   string   `yaml:"title" json:"title"`
	Space   string   `yaml:"space" json:"space"`
	PageID  string   `yaml:"page_id" json:"page_id"`
	URL     string   `yaml:"url" json:"url"`
	Version int      `yaml:"version" json:"version"`
	Updated string   `yaml:"updated" json:"updated"`
	Labels  []string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Path    []string `yaml:"path,omitempty" json:"path,omitempty"` // ancestor breadcrumb
}

type Stats struct {
	Cleaned, Failed int
}

// Run converts every raw page under <out>/raw into Markdown under <out>/docs.
func Run(cfg *config.Config) (Stats, error) {
	var st Stats
	rawDir := filepath.Join(cfg.Output.Dir, "raw")
	docsDir := filepath.Join(cfg.Output.Dir, "docs")

	entries, err := os.ReadDir(rawDir)
	if err != nil {
		return st, fmt.Errorf("read %s (run `export` first?): %w", rawDir, err)
	}
	for _, spaceDir := range entries {
		if !spaceDir.IsDir() {
			continue
		}
		space := spaceDir.Name()
		outDir := filepath.Join(docsDir, space)
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return st, err
		}
		files, err := os.ReadDir(filepath.Join(rawDir, space))
		if err != nil {
			return st, err
		}
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			if err := cleanOne(filepath.Join(rawDir, space, f.Name()), outDir); err != nil {
				st.Failed++
				log.Printf("clean: FAILED %s/%s: %v", space, f.Name(), err)
				continue
			}
			st.Cleaned++
		}
	}
	return st, nil
}

func cleanOne(rawPath, outDir string) error {
	b, err := os.ReadFile(rawPath)
	if err != nil {
		return err
	}
	var raw export.RawPage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	md, err := ConvertStorage(raw.Page.Body.Storage.Value)
	if err != nil {
		return err
	}
	meta := DocMeta{
		Title:   raw.Page.Title,
		Space:   raw.Space,
		PageID:  raw.Page.ID,
		URL:     raw.URL,
		Version: raw.Page.Version.Number,
		Updated: raw.Page.Version.When,
		Labels:  raw.Page.LabelNames(),
		Path:    raw.Page.AncestorTitles(),
	}
	fm, err := yaml.Marshal(meta)
	if err != nil {
		return err
	}
	var out strings.Builder
	out.WriteString("---\n")
	out.Write(fm)
	out.WriteString("---\n\n")
	out.WriteString("# " + raw.Page.Title + "\n\n")
	out.WriteString(md)

	name := Slug(raw.Page.Title) + "-" + raw.Page.ID + ".md"
	return os.WriteFile(filepath.Join(outDir, name), []byte(out.String()), 0o644)
}

var slugBad = regexp.MustCompile(`[\\/:*?"<>|#%\s]+`)

// Slug makes a filesystem-safe file name fragment, keeping CJK characters.
func Slug(s string) string {
	s = slugBad.ReplaceAllString(strings.TrimSpace(s), "-")
	s = strings.Trim(s, "-.")
	if len(s) > 80 {
		s = string([]rune(s)[:40])
	}
	if s == "" {
		s = "untitled"
	}
	return s
}

// ParseDoc splits a cleaned Markdown file into frontmatter and body.
func ParseDoc(path string) (DocMeta, string, error) {
	var meta DocMeta
	b, err := os.ReadFile(path)
	if err != nil {
		return meta, "", err
	}
	s := string(b)
	if !strings.HasPrefix(s, "---\n") {
		return meta, s, nil
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return meta, s, nil
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &meta); err != nil {
		return meta, "", fmt.Errorf("frontmatter of %s: %w", path, err)
	}
	return meta, strings.TrimSpace(rest[end+5:]), nil
}
