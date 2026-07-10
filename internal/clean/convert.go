// Package clean converts Confluence storage format (XHTML with ac:/ri:
// macro elements) into agent-friendly Markdown.
package clean

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// selfClosing matches self-closed ac:/ri: elements. The HTML parser
// treats unknown elements as non-void, so <ac:structured-macro ... />
// would otherwise swallow its following siblings as children.
var selfClosing = regexp.MustCompile(`<((?:ac|ri):[\w-]+)((?:"[^"]*"|'[^']*'|[^>"'])*?)/>`)

// ConvertStorage converts a storage-format body to Markdown.
func ConvertStorage(src string) (string, error) {
	src = selfClosing.ReplaceAllString(src, "<$1$2></$1>")
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return "", err
	}
	body := findElement(doc, "body")
	if body == nil {
		body = doc
	}
	var b strings.Builder
	blocks(body, &b)
	return tidy(b.String()), nil
}

func findElement(n *html.Node, name string) *html.Node {
	if n.Type == html.ElementNode && n.Data == name {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if r := findElement(c, name); r != nil {
			return r
		}
	}
	return nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

var wsRe = regexp.MustCompile(`[ \t\r\n]+`)

func collapse(s string) string {
	s = strings.ReplaceAll(s, " ", " ")
	return wsRe.ReplaceAllString(s, " ")
}

// cdata unwraps the comment node the html parser produces for
// <![CDATA[...]]> sections in storage format.
func cdata(d string) (string, bool) {
	if strings.HasPrefix(d, "[CDATA[") && strings.HasSuffix(d, "]]") {
		return d[len("[CDATA[") : len(d)-2], true
	}
	return "", false
}

var inlineTags = map[string]bool{
	"span": true, "a": true, "strong": true, "b": true, "em": true, "i": true,
	"code": true, "u": true, "s": true, "del": true, "sub": true, "sup": true,
	"br": true, "time": true, "small": true, "big": true, "abbr": true,
	"ac:link": true, "ac:image": true, "ac:emoticon": true,
	"ac:inline-comment-marker": true, "ac:placeholder": true,
}

// blocks walks block-level children of n, flushing runs of inline
// content as paragraphs.
func blocks(n *html.Node, b *strings.Builder) {
	var run strings.Builder
	flush := func() {
		if t := strings.TrimSpace(run.String()); t != "" {
			fmt.Fprintf(b, "\n\n%s\n\n", t)
		}
		run.Reset()
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			run.WriteString(collapse(c.Data))
			continue
		case html.CommentNode:
			if t, ok := cdata(c.Data); ok {
				run.WriteString(collapse(t))
			}
			continue
		case html.ElementNode:
			// handled below
		default:
			continue
		}
		if inlineTags[c.Data] {
			run.WriteString(inline(c))
			continue
		}
		flush()
		switch c.Data {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			lvl := int(c.Data[1] - '0')
			if t := strings.TrimSpace(inlineChildren(c)); t != "" {
				fmt.Fprintf(b, "\n\n%s %s\n\n", strings.Repeat("#", lvl), t)
			}
		case "p":
			if t := strings.TrimSpace(inlineChildren(c)); t != "" {
				fmt.Fprintf(b, "\n\n%s\n\n", t)
			}
		case "ul", "ol":
			b.WriteString("\n\n")
			list(c, b, 0)
			b.WriteString("\n\n")
		case "table":
			table(c, b)
		case "pre":
			fmt.Fprintf(b, "\n\n```\n%s\n```\n\n", strings.Trim(rawText(c), "\n"))
		case "blockquote":
			quote(b, renderBlocks(c))
		case "hr":
			b.WriteString("\n\n---\n\n")
		case "ac:structured-macro", "ac:macro":
			macro(c, b)
		case "ac:task-list":
			b.WriteString("\n\n")
			taskList(c, b)
			b.WriteString("\n\n")
		default:
			// layouts, divs, rich-text bodies, unknown containers: recurse
			blocks(c, b)
		}
	}
	flush()
}

func renderBlocks(n *html.Node) string {
	var sb strings.Builder
	blocks(n, &sb)
	return tidy(sb.String())
}

func quote(b *strings.Builder, body string) {
	if body == "" {
		return
	}
	b.WriteString("\n\n")
	for _, line := range strings.Split(body, "\n") {
		b.WriteString("> " + line + "\n")
	}
	b.WriteString("\n")
}

// inlineChildren renders the children of n as inline Markdown.
func inlineChildren(n *html.Node) string {
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(inline(c))
	}
	return sb.String()
}

// inline renders a single node as inline Markdown.
func inline(n *html.Node) string {
	switch n.Type {
	case html.TextNode:
		return collapse(n.Data)
	case html.CommentNode:
		if t, ok := cdata(n.Data); ok {
			return collapse(t)
		}
		return ""
	case html.ElementNode:
		// handled below
	default:
		return ""
	}
	switch n.Data {
	case "strong", "b":
		return wrap("**", inlineChildren(n))
	case "em", "i":
		return wrap("*", inlineChildren(n))
	case "s", "del":
		return wrap("~~", inlineChildren(n))
	case "code":
		if t := strings.TrimSpace(inlineChildren(n)); t != "" {
			return "`" + t + "`"
		}
		return ""
	case "br":
		return "\n"
	case "a":
		text := strings.TrimSpace(inlineChildren(n))
		href := attr(n, "href")
		if href == "" {
			return text
		}
		if text == "" {
			text = href
		}
		return fmt.Sprintf("[%s](%s)", text, href)
	case "ac:link":
		return confluenceLink(n)
	case "ac:image":
		return confluenceImage(n)
	case "ac:emoticon":
		return ":" + attr(n, "ac:name") + ":"
	case "ac:structured-macro", "ac:macro":
		// inline macros such as status / jira inside a paragraph
		var sb strings.Builder
		macro(n, &sb)
		return strings.TrimSpace(sb.String())
	default:
		return inlineChildren(n)
	}
}

func wrap(mark, s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return s
	}
	lead, trail := "", ""
	if strings.HasPrefix(s, " ") {
		lead = " "
	}
	if strings.HasSuffix(s, " ") {
		trail = " "
	}
	return lead + mark + t + mark + trail
}

// confluenceLink renders <ac:link> (page / attachment / user references).
func confluenceLink(n *html.Node) string {
	var target, label string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch c.Data {
		case "ri:page":
			target = attr(c, "ri:content-title")
		case "ri:attachment":
			target = attr(c, "ri:filename")
		case "ri:user":
			target = "@user"
		case "ac:plain-text-link-body":
			label = strings.TrimSpace(rawText(c))
		case "ac:link-body":
			label = strings.TrimSpace(inlineChildren(c))
		}
	}
	if label == "" {
		label = target
	}
	if label == "" {
		return ""
	}
	if target != "" && target != label {
		return fmt.Sprintf("[%s](confluence-page:%s)", label, target)
	}
	return "[" + label + "]"
}

func confluenceImage(n *html.Node) string {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch c.Data {
		case "ri:attachment":
			return fmt.Sprintf("![attachment](%s)", attr(c, "ri:filename"))
		case "ri:url":
			return fmt.Sprintf("![](%s)", attr(c, "ri:value"))
		}
	}
	return ""
}

// rawText returns the concatenated text of n without whitespace
// collapsing (for code bodies), unwrapping CDATA comments.
func rawText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(m *html.Node) {
		switch m.Type {
		case html.TextNode:
			sb.WriteString(m.Data)
		case html.CommentNode:
			if t, ok := cdata(m.Data); ok {
				sb.WriteString(t)
			}
		}
		for c := m.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

func macroParam(n *html.Node, name string) string {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "ac:parameter" && attr(c, "ac:name") == name {
			return strings.TrimSpace(rawText(c))
		}
	}
	return ""
}

func macroBody(n *html.Node) (plain string, rich *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch c.Data {
		case "ac:plain-text-body":
			plain = rawText(c)
		case "ac:rich-text-body":
			rich = c
		}
	}
	return
}

var admonitionLabels = map[string]string{
	"info":    "ℹ️ Info",
	"note":    "📝 Note",
	"tip":     "💡 Tip",
	"warning": "⚠️ Warning",
	"panel":   "Panel",
}

func macro(n *html.Node, b *strings.Builder) {
	name := attr(n, "ac:name")
	plain, rich := macroBody(n)
	switch name {
	case "code":
		lang := macroParam(n, "language")
		body := plain
		if body == "" && rich != nil {
			body = rawText(rich)
		}
		fmt.Fprintf(b, "\n\n```%s\n%s\n```\n\n", lang, strings.Trim(body, "\n"))
	case "info", "note", "tip", "warning", "panel":
		title := macroParam(n, "title")
		label := admonitionLabels[name]
		if title != "" {
			label += ": " + title
		}
		body := ""
		if rich != nil {
			body = renderBlocks(rich)
		} else if strings.TrimSpace(plain) != "" {
			body = strings.TrimSpace(plain)
		}
		quote(b, strings.TrimSpace("**"+label+"**\n\n"+body))
	case "expand":
		if t := macroParam(n, "title"); t != "" {
			fmt.Fprintf(b, "\n\n**%s**\n\n", t)
		}
		if rich != nil {
			blocks(rich, b)
		}
	case "status":
		if t := macroParam(n, "title"); t != "" {
			fmt.Fprintf(b, " **[%s]** ", t)
		}
	case "jira":
		if k := macroParam(n, "key"); k != "" {
			fmt.Fprintf(b, " `JIRA:%s` ", k)
		}
	case "toc", "children", "pagetree", "recently-updated", "livesearch",
		"contributors", "anchor", "attachments":
		// navigation/dynamic macros carry no exportable content
	default:
		if rich != nil {
			blocks(rich, b)
		} else if strings.TrimSpace(plain) != "" {
			fmt.Fprintf(b, "\n\n```\n%s\n```\n\n", strings.Trim(plain, "\n"))
		}
	}
}

func list(n *html.Node, b *strings.Builder, depth int) {
	ordered := n.Data == "ol"
	idx := 1
	indent := strings.Repeat("  ", depth)
	for li := n.FirstChild; li != nil; li = li.NextSibling {
		if li.Type != html.ElementNode || li.Data != "li" {
			continue
		}
		var text strings.Builder
		var nested []*html.Node
		for c := li.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && (c.Data == "ul" || c.Data == "ol") {
				nested = append(nested, c)
				continue
			}
			if c.Type == html.ElementNode && c.Data == "p" {
				text.WriteString(" " + inlineChildren(c))
				continue
			}
			text.WriteString(inline(c))
		}
		marker := "- "
		if ordered {
			marker = fmt.Sprintf("%d. ", idx)
			idx++
		}
		line := strings.TrimSpace(collapse(text.String()))
		b.WriteString(indent + marker + line + "\n")
		for _, nl := range nested {
			list(nl, b, depth+1)
		}
	}
}

func taskList(n *html.Node, b *strings.Builder) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if c.Data == "ac:task-list" { // nested task list
			taskList(c, b)
			continue
		}
		if c.Data != "ac:task" {
			continue
		}
		status, body := "", ""
		for t := c.FirstChild; t != nil; t = t.NextSibling {
			if t.Type != html.ElementNode {
				continue
			}
			switch t.Data {
			case "ac:task-status":
				status = strings.TrimSpace(rawText(t))
			case "ac:task-body":
				body = strings.TrimSpace(collapse(inlineChildren(t)))
			}
		}
		mark := " "
		if status == "complete" {
			mark = "x"
		}
		fmt.Fprintf(b, "- [%s] %s\n", mark, body)
	}
}

func table(n *html.Node, b *strings.Builder) {
	var rows [][]string
	var walkRows func(*html.Node)
	walkRows = func(m *html.Node) {
		for c := m.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.Data {
			case "thead", "tbody", "tfoot":
				walkRows(c)
			case "tr":
				var cells []string
				for td := c.FirstChild; td != nil; td = td.NextSibling {
					if td.Type == html.ElementNode && (td.Data == "td" || td.Data == "th") {
						cell := strings.TrimSpace(collapse(renderCell(td)))
						cell = strings.ReplaceAll(cell, "|", `\|`)
						cell = strings.ReplaceAll(cell, "\n", " ")
						cells = append(cells, cell)
					}
				}
				if len(cells) > 0 {
					rows = append(rows, cells)
				}
			}
		}
	}
	walkRows(n)
	if len(rows) == 0 {
		return
	}
	width := 0
	for _, r := range rows {
		if len(r) > width {
			width = len(r)
		}
	}
	pad := func(r []string) []string {
		for len(r) < width {
			r = append(r, "")
		}
		return r
	}
	b.WriteString("\n\n")
	b.WriteString("| " + strings.Join(pad(rows[0]), " | ") + " |\n")
	sep := make([]string, width)
	for i := range sep {
		sep[i] = "---"
	}
	b.WriteString("| " + strings.Join(sep, " | ") + " |\n")
	for _, r := range rows[1:] {
		b.WriteString("| " + strings.Join(pad(r), " | ") + " |\n")
	}
	b.WriteString("\n\n")
}

// renderCell renders a table cell: block content flattened to one line.
func renderCell(n *html.Node) string {
	body := renderBlocks(n)
	if body == "" {
		return inlineChildren(n)
	}
	return strings.ReplaceAll(body, "\n\n", " ⏎ ")
}

// tidy collapses runs of blank lines outside fenced code blocks and
// trims the result.
func tidy(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	inFence := false
	blank := 0
	for _, l := range lines {
		trimmed := strings.TrimRight(l, " \t")
		if strings.HasPrefix(strings.TrimSpace(trimmed), "```") {
			inFence = !inFence
			out = append(out, trimmed)
			blank = 0
			continue
		}
		if inFence {
			out = append(out, l)
			continue
		}
		if strings.TrimSpace(trimmed) == "" {
			blank++
			if blank == 1 {
				out = append(out, "")
			}
			continue
		}
		blank = 0
		out = append(out, trimmed)
	}
	return strings.TrimSpace(strings.Join(out, "\n")) + "\n"
}
