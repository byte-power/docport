// Package chunk splits Markdown documents into retrieval-sized pieces
// along heading boundaries.
package chunk

import (
	"strings"
)

type Chunk struct {
	Section string // heading path, e.g. "部署 > 前置条件"
	Text    string
}

// Split cuts a Markdown body into chunks of at most maxRunes runes,
// preferring heading boundaries; oversized sections are hard-split on
// blank lines.
func Split(md string, maxRunes int) []Chunk {
	type section struct {
		path []string
		buf  []string
	}
	var sections []section
	cur := section{}
	headings := make([]string, 0, 6) // heading text by level-1 index
	inFence := false

	flush := func() {
		if len(cur.buf) > 0 && strings.TrimSpace(strings.Join(cur.buf, "\n")) != "" {
			sections = append(sections, cur)
		}
	}

	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		}
		lvl, title := headingOf(line)
		if !inFence && lvl > 0 {
			flush()
			if lvl > len(headings)+1 {
				lvl = len(headings) + 1
			}
			headings = append(headings[:lvl-1], title)
			cur = section{path: append([]string(nil), headings...)}
			continue
		}
		cur.buf = append(cur.buf, line)
	}
	flush()

	var out []Chunk
	for _, s := range sections {
		text := strings.TrimSpace(strings.Join(s.buf, "\n"))
		path := strings.Join(s.path, " > ")
		for _, part := range hardSplit(text, maxRunes) {
			out = append(out, Chunk{Section: path, Text: part})
		}
	}
	// merge tiny neighbours within the same section budget
	return merge(out, maxRunes)
}

func headingOf(line string) (int, string) {
	t := line
	lvl := 0
	for lvl < len(t) && lvl < 6 && t[lvl] == '#' {
		lvl++
	}
	if lvl == 0 || lvl >= len(t) || t[lvl] != ' ' {
		return 0, ""
	}
	return lvl, strings.TrimSpace(t[lvl+1:])
}

// hardSplit cuts text longer than maxRunes on blank-line boundaries.
func hardSplit(text string, maxRunes int) []string {
	if len([]rune(text)) <= maxRunes {
		if text == "" {
			return nil
		}
		return []string{text}
	}
	paras := strings.Split(text, "\n\n")
	var out []string
	var buf strings.Builder
	size := 0
	for _, p := range paras {
		pr := len([]rune(p))
		if size > 0 && size+pr > maxRunes {
			out = append(out, strings.TrimSpace(buf.String()))
			buf.Reset()
			size = 0
		}
		if pr > maxRunes {
			// single huge paragraph (e.g. giant code block): cut by runes
			runes := []rune(p)
			for len(runes) > maxRunes {
				out = append(out, string(runes[:maxRunes]))
				runes = runes[maxRunes:]
			}
			buf.WriteString(string(runes))
			size = len(runes)
			continue
		}
		if size > 0 {
			buf.WriteString("\n\n")
			size += 2
		}
		buf.WriteString(p)
		size += pr
	}
	if strings.TrimSpace(buf.String()) != "" {
		out = append(out, strings.TrimSpace(buf.String()))
	}
	return out
}

// merge concatenates consecutive chunks that share a section prefix while
// staying under maxRunes, to avoid many fragmentary chunks.
func merge(in []Chunk, maxRunes int) []Chunk {
	var out []Chunk
	for _, c := range in {
		if n := len(out); n > 0 {
			prev := &out[n-1]
			if len([]rune(prev.Text))+len([]rune(c.Text)) < maxRunes/2 {
				sec := prev.Section
				if sec == "" {
					sec = c.Section
				}
				prev.Section = sec
				prev.Text += "\n\n" + headerLine(c.Section, prev.Section) + c.Text
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

func headerLine(section, mergedInto string) string {
	if section == "" || section == mergedInto {
		return ""
	}
	return "## " + section + "\n\n"
}
