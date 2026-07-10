package clean

import (
	"strings"
	"testing"
)

func TestConvertStorage(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string // substrings that must appear
	}{
		{
			name: "headings and paragraph",
			in:   `<h1>部署指南</h1><p>先安装 <strong>Docker</strong> 和 <em>compose</em>。</p>`,
			want: []string{"# 部署指南", "先安装 **Docker** 和 *compose*。"},
		},
		{
			name: "code macro with CDATA",
			in: `<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">bash</ac:parameter>` +
				`<ac:plain-text-body><![CDATA[docker compose up -d
echo "a | b"]]></ac:plain-text-body></ac:structured-macro>`,
			want: []string{"```bash", "docker compose up -d", `echo "a | b"`},
		},
		{
			name: "info macro",
			in:   `<ac:structured-macro ac:name="info"><ac:rich-text-body><p>注意事项</p></ac:rich-text-body></ac:structured-macro>`,
			want: []string{"> **ℹ️ Info**", "> 注意事项"},
		},
		{
			name: "table",
			in:   `<table><tbody><tr><th>参数</th><th>说明</th></tr><tr><td>host</td><td>数据库地址</td></tr></tbody></table>`,
			want: []string{"| 参数 | 说明 |", "| --- | --- |", "| host | 数据库地址 |"},
		},
		{
			name: "nested list",
			in:   `<ul><li>步骤一</li><li>步骤二<ul><li>子项</li></ul></li></ul>`,
			want: []string{"- 步骤一", "- 步骤二", "  - 子项"},
		},
		{
			name: "page link",
			in:   `<p>参见 <ac:link><ri:page ri:content-title="安装手册"/></ac:link></p>`,
			want: []string{"[安装手册]"},
		},
		{
			name: "link with label",
			in:   `<p><a href="https://example.com">官网</a></p>`,
			want: []string{"[官网](https://example.com)"},
		},
		{
			name: "task list",
			in: `<ac:task-list><ac:task><ac:task-status>complete</ac:task-status><ac:task-body>已完成项</ac:task-body></ac:task>` +
				`<ac:task-list><ac:task><ac:task-status>incomplete</ac:task-status><ac:task-body>待办项</ac:task-body></ac:task></ac:task-list></ac:task-list>`,
			want: []string{"- [x] 已完成项", "- [ ] 待办项"},
		},
		{
			name: "toc macro dropped",
			in:   `<p>before</p><ac:structured-macro ac:name="toc"/><p>after</p>`,
			want: []string{"before", "after"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ConvertStorage(tt.in)
			if err != nil {
				t.Fatalf("ConvertStorage: %v", err)
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("output missing %q\n--- got ---\n%s", w, got)
				}
			}
		})
	}
}

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"部署指南 v2.0", "部署指南-v2.0"},
		{"a/b\\c:d", "a-b-c-d"},
		{"  ", "untitled"},
	}
	for _, tt := range tests {
		if got := Slug(tt.in); got != tt.want {
			t.Errorf("Slug(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
