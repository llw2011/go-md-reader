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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatMarkdown(tc.in)
			if got != tc.want {
				t.Fatalf("FormatMarkdown() mismatch:\n--- GOT ---\n%q\n--- WANT ---\n%q", got, tc.want)
			}
		})
	}
}
