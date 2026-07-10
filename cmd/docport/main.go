// Command docport is the Confluence → agent-docs pipeline:
//
//	docport export  # pull pages from Confluence into data/raw (incremental)
//	docport clean   # storage XHTML -> Markdown into data/docs
//	docport index   # chunk + keywords (+ embeddings) into data/index
//	docport all     # export + clean + index
//	docport search  # ad-hoc query from the terminal
//	docport serve   # local HTTP search API for agents
//	docport mcp     # MCP stdio server for Claude Code / OpenCode
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/byte-power/docport/internal/clean"
	"github.com/byte-power/docport/internal/config"
	"github.com/byte-power/docport/internal/export"
	"github.com/byte-power/docport/internal/fulltext"
	"github.com/byte-power/docport/internal/index"
	"github.com/byte-power/docport/internal/search"
	"github.com/byte-power/docport/internal/server"
)

const usage = `usage: docport <command> [flags]

commands:
  export   pull pages from Confluence into <out>/raw (incremental by version)
           -page <id|url> exports a single page (for testing), e.g.
             docport export -page 123456
             docport export -page "https://wiki.example.com/pages/viewpage.action?pageId=123456"
             docport export -page "https://wiki.example.com/display/DEV/Page+Title"
           -page <id|url> -recursive exports the page and its whole subtree
  clean    convert raw storage XHTML to Markdown in <out>/docs
  index    chunk + extract keywords (+ embeddings) into <out>/index
  all      export + clean + index
  search   query the index: docport search [-k 8] [-engine bm25|bleve] "关键词"
  serve    HTTP search API: docport serve [-addr 127.0.0.1:8787] [-engine bm25|bleve]
  mcp      MCP server (for Claude Code / OpenCode) [-engine bm25|bleve]
             default: stdio transport (local mcp config)
             -addr host:port serves MCP over streamable HTTP at /mcp,
             -token xxx requires "Authorization: Bearer xxx" (recommended
             when binding beyond 127.0.0.1), e.g.
               docport mcp -addr 0.0.0.0:8788 -token $MCP_TOKEN

engines (both in-process, all text loaded into memory):
  bm25     built-in BM25 (+ cosine vectors when the index has embeddings)
  bleve    bleve full-text index with CJK analyzer; no embeddings needed

common flags:
  -config path   config file (default "config.yaml")
`

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cfgPath := fs.String("config", "config.yaml", "config file path")
	topK := fs.Int("k", 0, "search: number of results")
	addr := fs.String("addr", "", "serve: listen address")
	noEmbed := fs.Bool("no-embed", false, "index: skip embeddings even if enabled in config")
	pageRef := fs.String("page", "", "export: single page ID or URL")
	recursive := fs.Bool("recursive", false, "export: with -page, also export all descendant pages")
	engineName := fs.String("engine", "", "serve/mcp: search backend, bm25 or bleve (default from config)")
	mcpToken := fs.String("token", "", "mcp: bearer token for the HTTP transport (default from config serve.mcp_token)")
	_ = fs.Parse(os.Args[2:])

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	switch cmd {
	case "export":
		if *pageRef != "" {
			if err := export.RunOne(cfg, *pageRef, *recursive); err != nil {
				log.Fatalf("export error: %v", err)
			}
			return
		}
		runExport(cfg)
	case "clean":
		runClean(cfg)
	case "index":
		runIndex(cfg, !*noEmbed)
	case "all":
		runExport(cfg)
		runClean(cfg)
		runIndex(cfg, !*noEmbed)
	case "search":
		query := strings.TrimSpace(strings.Join(fs.Args(), " "))
		if query == "" {
			log.Fatal("error: search needs a query, e.g. docport search \"部署流程\"")
		}
		runSearch(cfg, *engineName, query, *topK)
	case "serve":
		engine := mustSearcher(cfg, *engineName)
		a := cfg.Serve.Addr
		if *addr != "" {
			a = *addr
		}
		if err := server.RunHTTP(engine, a, cfg.Serve.TopK); err != nil {
			log.Fatalf("error: %v", err)
		}
	case "mcp":
		engine := mustSearcher(cfg, *engineName)
		if *addr != "" {
			token := cfg.Serve.MCPToken
			if *mcpToken != "" {
				token = *mcpToken
			}
			if err := server.RunMCPHTTP(engine, *addr, token, cfg.Serve.TopK); err != nil {
				log.Fatalf("error: %v", err)
			}
			return
		}
		if err := server.RunMCP(engine, cfg.Serve.TopK); err != nil {
			log.Fatalf("error: %v", err)
		}
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func runExport(cfg *config.Config) {
	st, err := export.Run(cfg)
	if err != nil {
		log.Fatalf("export error: %v", err)
	}
	log.Printf("export done: %d fetched, %d unchanged (skipped)", st.Fetched, st.Skipped)
}

func runClean(cfg *config.Config) {
	st, err := clean.Run(cfg)
	if err != nil {
		log.Fatalf("clean error: %v", err)
	}
	log.Printf("clean done: %d converted, %d failed", st.Cleaned, st.Failed)
}

func runIndex(cfg *config.Config, withEmbeddings bool) {
	st, err := index.Run(cfg, withEmbeddings)
	if err != nil {
		log.Fatalf("index error: %v", err)
	}
	log.Printf("index done: %d docs, %d chunks, %d embedded", st.Docs, st.Chunks, st.Embedded)
}

func mustEngine(cfg *config.Config) *search.Engine {
	engine, err := search.Load(cfg)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	return engine
}

// mustSearcher picks the serve/mcp backend: flag overrides config.
func mustSearcher(cfg *config.Config, flagEngine string) server.Searcher {
	name := cfg.Serve.Engine
	if flagEngine != "" {
		name = flagEngine
	}
	switch name {
	case "bm25":
		return mustEngine(cfg)
	case "bleve":
		engine, err := fulltext.Load(cfg)
		if err != nil {
			log.Fatalf("error: %v", err)
		}
		return engine
	default:
		log.Fatalf("error: unknown engine %q (want bm25 or bleve)", name)
		return nil
	}
}

func runSearch(cfg *config.Config, engineName, query string, k int) {
	engine := mustSearcher(cfg, engineName)
	if k <= 0 {
		k = cfg.Serve.TopK
	}
	results, err := engine.Search(query, k)
	if err != nil {
		log.Fatalf("search error: %v", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(results)
}
