package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Confluence Confluence `yaml:"confluence"`
	Output     Output     `yaml:"output"`
	Index      Index      `yaml:"index"`
	Embedding  Embedding  `yaml:"embedding"`
	Serve      Serve      `yaml:"serve"`
}

type Confluence struct {
	BaseURL     string   `yaml:"base_url"`
	Token       string   `yaml:"token"`    // Personal Access Token (Bearer), DC 7.9+
	Username    string   `yaml:"username"` // basic auth fallback
	Password    string   `yaml:"password"`
	Spaces      []string `yaml:"spaces"` // empty = all global spaces
	PageSize    int      `yaml:"page_size"`
	TimeoutSec  int      `yaml:"timeout_seconds"`
	SleepMs     int      `yaml:"sleep_ms"` // delay between paged requests
	InsecureTLS bool     `yaml:"insecure_tls"`
}

type Output struct {
	Dir string `yaml:"dir"` // root of raw/ docs/ index/
}

type Index struct {
	ChunkSize    int `yaml:"chunk_size"`   // max runes per chunk
	DocKeywords  int `yaml:"doc_keywords"` // top-N keywords per doc
	ChunkKeyword int `yaml:"chunk_keywords"`
}

type Embedding struct {
	Enabled bool   `yaml:"enabled"`
	BaseURL string `yaml:"base_url"` // OpenAI-compatible, e.g. http://localhost:11434/v1
	APIKey  string `yaml:"api_key"`
	Model   string `yaml:"model"`
	Batch   int    `yaml:"batch"`
}

type Serve struct {
	Addr     string `yaml:"addr"`
	TopK     int    `yaml:"top_k"`
	Engine   string `yaml:"engine"`    // "bm25" (default) or "bleve", both in-process
	MCPToken string `yaml:"mcp_token"` // optional bearer token for the MCP HTTP transport
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	// allow ${ENV_VAR} for secrets
	expanded := os.ExpandEnv(string(b))
	var c Config
	if err := yaml.Unmarshal([]byte(expanded), &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	if c.Confluence.BaseURL == "" {
		return nil, fmt.Errorf("confluence.base_url is required")
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Output.Dir == "" {
		c.Output.Dir = "data"
	}
	if c.Confluence.PageSize <= 0 {
		c.Confluence.PageSize = 50
	}
	if c.Confluence.TimeoutSec <= 0 {
		c.Confluence.TimeoutSec = 30
	}
	if c.Index.ChunkSize <= 0 {
		c.Index.ChunkSize = 1800
	}
	if c.Index.DocKeywords <= 0 {
		c.Index.DocKeywords = 12
	}
	if c.Index.ChunkKeyword <= 0 {
		c.Index.ChunkKeyword = 8
	}
	if c.Embedding.Batch <= 0 {
		c.Embedding.Batch = 16
	}
	if c.Serve.Addr == "" {
		c.Serve.Addr = "127.0.0.1:8787"
	}
	if c.Serve.TopK <= 0 {
		c.Serve.TopK = 8
	}
	if c.Serve.Engine == "" {
		c.Serve.Engine = "bm25"
	}
}
