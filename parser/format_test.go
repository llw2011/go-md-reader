package parser

import "testing"

func TestFormatMarkdown(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "empty",
			in:   "   \r\n\t ",
			want: "",
		},
		{
			name: "headings_and_pangu_spacing",
			in:   "#标题一\n使用Go1.25开发Markdown阅读器,效率提升50%以上。\n##  二级标题 ##\n正文继续。",
			want: "# 标题一\n\n使用 Go1.25 开发 Markdown 阅读器,效率提升 50% 以上。\n\n## 二级标题\n\n正文继续。\n",
		},
		{
			name: "lists_and_tasks",
			in:   "-第一项\n-   第二项\n-[x]已完成任务\n-[]待办事项\n1.第一条\n2.   第二条\n2.0版本发布\n-100元折扣",
			want: "- 第一项\n- 第二项\n- [x] 已完成任务\n- [ ] 待办事项\n1. 第一条\n2. 第二条\n2.0 版本发布\n-100 元折扣\n",
		},
		{
			name: "preserve_frontmatter_and_code_blocks",
			in:   "---\ntitle: 测试Go\ntags: [md]\n---\n\n段落一\n```go\n//注释中文andEnglish不该加空格\nfunc main(){fmt.Println(\"测试v1\")}\n```\n段落二",
			want: "---\ntitle: 测试Go\ntags: [md]\n---\n\n段落一\n\n```go\n//注释中文andEnglish不该加空格\nfunc main(){fmt.Println(\"测试v1\")}\n```\n\n段落二\n",
		},
		{
			name: "preserve_inline_code_and_urls",
			in:   "调用`fmt.Println(\"你好Go\")`并访问https://github.com/llw2011/go-md-reader获取更新。",
			want: "调用 `fmt.Println(\"你好Go\")` 并访问 https://github.com/llw2011/go-md-reader 获取更新。\n",
		},
		{
			name: "blockquotes_and_alerts",
			in:   ">引用文本Go语言\n>>嵌套引用\n>[!NOTE]注意事项123",
			want: "> 引用文本 Go 语言\n> > 嵌套引用\n> [!NOTE] 注意事项 123\n",
		},
		{
			name: "gfm_table_alignment",
			in:   "表前段落\n|功能|状态|说明|\n|:---|:---:|---:|\n|自动排版Go|支持|对齐100|\n表后段落",
			want: "表前段落\n\n| 功能        | 状态 | 说明     |\n| :---------- | :--: | -------: |\n| 自动排版 Go | 支持 | 对齐 100 |\n\n表后段落\n",
		},
		{
			name: "preserve_setext_and_math",
			in:   "副标题\n---\n\n$$\nx=中文+English\n$$\n\n\n\n结尾",
			want: "副标题\n---\n\n$$\nx=中文+English\n$$\n\n结尾\n",
		},
		{
			name: "preserve_toml_frontmatter_and_shebang_and_seven_hashes",
			in:   "+++\ntitle = \"Go工具\"\n+++\n\n#!/bin/bash\n#######保留七个井号Go\n\n#   一级标题 ##  \n***\n___\n+100次试验\n--flag参数",
			want: "+++\ntitle = \"Go工具\"\n+++\n\n#!/bin/bash\n#######保留七个井号 Go\n\n# 一级标题\n\n***\n\n___\n\n+100 次试验\n--flag 参数\n",
		},
		{
			name: "preserve_tilde_and_blockquote_fences",
			in:   "前文\n~~~python\nprint(\"中文andEnglish\")\n~~~~\n后文\n\n>引用开始\n> ```js\n> const a=\"中文Go\";\n> ```\n>引用结束",
			want: "前文\n\n~~~python\nprint(\"中文andEnglish\")\n~~~~\n\n后文\n\n> 引用开始\n> ```js\n> const a=\"中文Go\";\n> ```\n> 引用结束\n",
		},
		{
			name: "ordered_list_paren_and_multispaces_and_nested_blockquote",
			in:   "1)第一项\n2)   [X]完成Go集成\n*   [ ]待检查\n>   \n> [!WARNING]请确认Go配置\n正文",
			want: "1) 第一项\n2) [x] 完成 Go 集成\n* [ ] 待检查\n>\n> [!WARNING] 请确认 Go 配置\n正文\n",
		},
		{
			name: "protected_spans_links_images_html_math_escapes",
			in:   "参考[文档Go指南](https://example.com/a_b)与![图Go](img/a.png),公式$x+y=Go$及<span>标签Go</span>还有\\*转义。",
			want: "参考[文档Go指南](https://example.com/a_b) 与![图Go](img/a.png),公式$x+y=Go$及<span>标签 Go</span>还有\\*转义。\n",
		},
		{
			name: "table_escaped_pipe_code_pipe_and_ragged_cols",
			in:   "|名称|示例|\n|---|---|\n|转义\\|管线|调用`a|b`方法|额外列Go|\n|缺列|\n",
			want: "| 名称       | 示例            |           |\n| ---------- | --------------- | --------- |\n| 转义\\|管线 | 调用 `a|b` 方法 | 额外列 Go |\n| 缺列       |                 |           |\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatMarkdown(tc.in)
			if got != tc.want {
				t.Fatalf("FormatMarkdown() mismatch:\n--- GOT ---\n%q\n--- WANT ---\n%q", got, tc.want)
			}
			// Auto-formatting must be strictly idempotent on every fixture.
			if again := FormatMarkdown(got); again != got {
				t.Fatalf("FormatMarkdown is not idempotent:\n--- ONCE ---\n%q\n--- TWICE ---\n%q", got, again)
			}
		})
	}
}
