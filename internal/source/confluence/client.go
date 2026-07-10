// Package confluence is a minimal REST client for self-hosted
// Confluence Server / Data Center (rest/api).
package confluence

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/byte-power/docport/internal/config"
)

type Client struct {
	base  string
	cfg   config.Confluence
	hc    *http.Client
	sleep time.Duration
}

type Page struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Body   struct {
		Storage struct {
			Value string `json:"value"`
		} `json:"storage"`
	} `json:"body"`
	Version struct {
		Number int    `json:"number"`
		When   string `json:"when"`
	} `json:"version"`
	Metadata struct {
		Labels struct {
			Results []struct {
				Name string `json:"name"`
			} `json:"results"`
		} `json:"labels"`
	} `json:"metadata"`
	Ancestors []struct {
		Title string `json:"title"`
	} `json:"ancestors"`
	Space struct {
		Key string `json:"key"`
	} `json:"space"`
	Links struct {
		WebUI string `json:"webui"`
	} `json:"_links"`
}

const pageExpand = "body.storage,version,metadata.labels,ancestors,space"

func (p *Page) LabelNames() []string {
	var out []string
	for _, l := range p.Metadata.Labels.Results {
		out = append(out, l.Name)
	}
	return out
}

func (p *Page) AncestorTitles() []string {
	var out []string
	for _, a := range p.Ancestors {
		out = append(out, a.Title)
	}
	return out
}

func New(cfg config.Confluence) *Client {
	tr := &http.Transport{}
	if cfg.InsecureTLS {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Client{
		base:  strings.TrimRight(cfg.BaseURL, "/"),
		cfg:   cfg,
		hc:    &http.Client{Timeout: time.Duration(cfg.TimeoutSec) * time.Second, Transport: tr},
		sleep: time.Duration(cfg.SleepMs) * time.Millisecond,
	}
}

func (c *Client) BaseURL() string { return c.base }

func (c *Client) get(path string, q url.Values, out any) error {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	} else if c.cfg.Username != "" {
		req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("GET %s: %s: %s", u, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Spaces returns the configured space keys, or all global spaces when
// none are configured.
func (c *Client) Spaces() ([]string, error) {
	if len(c.cfg.Spaces) > 0 {
		return c.cfg.Spaces, nil
	}
	var keys []string
	start := 0
	for {
		var page struct {
			Results []struct {
				Key string `json:"key"`
			} `json:"results"`
			Size  int `json:"size"`
			Limit int `json:"limit"`
		}
		q := url.Values{}
		q.Set("type", "global")
		q.Set("limit", "100")
		q.Set("start", fmt.Sprint(start))
		if err := c.get("/rest/api/space", q, &page); err != nil {
			return nil, err
		}
		for _, r := range page.Results {
			keys = append(keys, r.Key)
		}
		if page.Size < page.Limit || page.Size == 0 {
			break
		}
		start += page.Size
		c.pause()
	}
	return keys, nil
}

// GetPage fetches a single page by its numeric content ID.
func (c *Client) GetPage(id string) (Page, error) {
	var p Page
	q := url.Values{}
	q.Set("expand", pageExpand)
	if err := c.get("/rest/api/content/"+id, q, &p); err != nil {
		return p, err
	}
	return p, nil
}

// FindPage resolves a page by space key and exact title.
func (c *Client) FindPage(space, title string) (Page, error) {
	var page struct {
		Results []Page `json:"results"`
	}
	q := url.Values{}
	q.Set("spaceKey", space)
	q.Set("title", title)
	q.Set("type", "page")
	q.Set("status", "current")
	q.Set("expand", pageExpand)
	q.Set("limit", "1")
	if err := c.get("/rest/api/content", q, &page); err != nil {
		return Page{}, err
	}
	if len(page.Results) == 0 {
		return Page{}, fmt.Errorf("page not found: space=%s title=%q", space, title)
	}
	return page.Results[0], nil
}

// WalkChildPages fetches the direct child pages of a page (paginated)
// and invokes fn for each. Descend further by calling it again with the
// child IDs.
func (c *Client) WalkChildPages(parentID string, fn func(Page) error) error {
	start := 0
	for {
		var page struct {
			Results []Page `json:"results"`
			Size    int    `json:"size"`
			Limit   int    `json:"limit"`
		}
		q := url.Values{}
		q.Set("expand", pageExpand)
		q.Set("limit", fmt.Sprint(c.cfg.PageSize))
		q.Set("start", fmt.Sprint(start))
		if err := c.get("/rest/api/content/"+parentID+"/child/page", q, &page); err != nil {
			return err
		}
		for _, p := range page.Results {
			if err := fn(p); err != nil {
				return err
			}
		}
		if page.Size < page.Limit || page.Size == 0 {
			break
		}
		start += page.Size
		c.pause()
	}
	return nil
}

// WalkPages fetches every current page of a space (with storage body,
// version, labels, ancestors) and invokes fn for each.
func (c *Client) WalkPages(space string, fn func(Page) error) error {
	start := 0
	for {
		var page struct {
			Results []Page `json:"results"`
			Size    int    `json:"size"`
			Limit   int    `json:"limit"`
		}
		q := url.Values{}
		q.Set("spaceKey", space)
		q.Set("type", "page")
		q.Set("status", "current")
		q.Set("expand", pageExpand)
		q.Set("limit", fmt.Sprint(c.cfg.PageSize))
		q.Set("start", fmt.Sprint(start))
		if err := c.get("/rest/api/content", q, &page); err != nil {
			return err
		}
		for _, p := range page.Results {
			if err := fn(p); err != nil {
				return err
			}
		}
		if page.Size < page.Limit || page.Size == 0 {
			break
		}
		start += page.Size
		c.pause()
	}
	return nil
}

func (c *Client) pause() {
	if c.sleep > 0 {
		time.Sleep(c.sleep)
	}
}
