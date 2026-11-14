package parser

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	reHeadingLine      = regexp.MustCompile(`^\s{0,3}(#{1,6})(.*)$`)
	reTrailingHashes   = regexp.MustCompile(`\s+#+\s*$`)
	reHRLine           = regexp.MustCompile(`^\s{0,3}(?:(?:-\s*){3,}|(?:\*\s*){3,}|(?:_\s*){3,})$`)
	reBulletNoSpace    = regexp.MustCompile(`^(\s*)([+-])([^\s+-].*)$`)
	reBulletMultiSpace = regexp.MustCompile(`^(\s*)([-*+])[ \t]{2,}(.*)$`)
	reBulletNormal     = regexp.MustCompile(`^(\s*)([-*+])[ \t]+(.*)$`)
	reTaskBox          = regexp.MustCompile(`^\[([ xX]?)\](.*)$`)
	reOrderedLine      = regexp.MustCompile(`^(\s*)(\d+)([.)])(.*)$`)
	reFenceStart       = regexp.MustCompile("^(\\s{0,3}(?:>\\s*)*)(`{3,}|~{3,})(.*)$")
	reTableDelim       = regexp.MustCompile(`^\|?\s*:?-{2,}:?\s*(?:\|\s*:?-{2,}:?\s*)*\|?$`)
	reAlertTag      = regexp.MustCompile(`^(\[!(?:NOTE|TIP|IMPORTANT|WARNING|CAUTION)\])(.*)$`)
	reProtectedSpan = regexp.MustCompile("(`+[^`]*`+|!?\\[[^\\]\n]*\\]\\([^)\n]*\\)|<https?://[^>\n\\s]+>|<mailto:[^>\n\\s]+>|https?://[A-Za-z0-9\\-._~:/?#\\[\\]@!$&'*+,;=%]+|<[A-Za-z/!][^>\n]*>|\\$[^$\n]+\\$|\\\\.)")
)

// FormatMarkdown normalizes Markdown structure, whitespace, GFM tables, and CJK/ASCII spacing
// while keeping YAML/TOML frontmatter, fenced code blocks, math blocks, inline code, and URLs intact.
func FormatMarkdown(src string) string {
	if strings.TrimSpace(src) == "" {
		return ""
	}

	normalized := strings.ReplaceAll(src, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")

	var out []string
	i := 0

	// 1. Preserve leading YAML (---) or TOML (+++) frontmatter verbatim.
	if len(lines) >= 3 && (strings.TrimSpace(lines[0]) == "---" || strings.TrimSpace(lines[0]) == "+++") {
		delim := strings.TrimSpace(lines[0])
		endIdx := -1
		for j := 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == delim {
				endIdx = j
				break
			}
		}
		if endIdx != -1 {
			for j := 0; j <= endIdx; j++ {
				out = append(out, strings.TrimRight(lines[j], " \t"))
			}
			i = endIdx + 1
			for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
				i++
			}
			if i < len(lines) {
				out = append(out, "")
			}
		}
	}

	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Skip duplicate blank lines.
		if trimmed == "" {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			i++
			continue
		}

		// 2. Preserve fenced code blocks verbatim.
		if m := reFenceStart.FindStringSubmatch(line); m != nil {
			prefix := m[1]
			fence := m[2]
			fenceChar := fence[0]
			fenceLen := len(fence)

			if len(out) > 0 && out[len(out)-1] != "" && !strings.HasPrefix(strings.TrimSpace(prefix), ">") {
				out = append(out, "")
			}
			out = append(out, strings.TrimRight(line, " \t"))
			i++
			for i < len(lines) {
				cur := lines[i]
				out = append(out, cur)
				i++
				if isFenceClose(cur, fenceChar, fenceLen) {
					break
				}
			}
			if i < len(lines) && strings.TrimSpace(lines[i]) != "" {
				out = append(out, "")
			}
			continue
		}

		// 3. Preserve display math blocks ($$ ... $$) verbatim.
		if trimmed == "$$" {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			out = append(out, "$$")
			i++
			for i < len(lines) {
				cur := lines[i]
				out = append(out, strings.TrimRight(cur, " \t"))
				i++
				if strings.TrimSpace(cur) == "$$" {
					break
				}
			}
			if i < len(lines) && strings.TrimSpace(lines[i]) != "" {
				out = append(out, "")
			}
			continue
		}

		// 4. Format GFM tables with clean column alignment.
		if i+1 < len(lines) && looksLikeTableRow(line) && isTableDelimiterLine(lines[i+1]) {
			tableStart := i
			i += 2
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" && looksLikeTableRow(lines[i]) {
				i++
			}
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			formattedTable := formatTableBlock(lines[tableStart:i])
			out = append(out, formattedTable...)
			if i < len(lines) && strings.TrimSpace(lines[i]) != "" {
				out = append(out, "")
			}
			continue
		}

		// 5. ATX Headings (# 标题).
		if m := reHeadingLine.FindStringSubmatch(line); m != nil {
			hashes := m[1]
			rest := m[2]
			// Guard against 7+ hashes or #tag without space when preceded by non-heading text?
			// In CommonMark, 1-6 `#` followed by text or space: normalize `#标题` -> `# 标题`,
			// but skip hashbang `#!` or issue references only when clearly not intended? Wait:
			// CommonMark requires space after `#`, so `#标题` in Markdown was unrendered/broken heading syntax.
			// Still, never treat `#tag` mid-sentence or `#!` shebang as heading.
			if !strings.HasPrefix(rest, "#") && !strings.HasPrefix(rest, "!") {
				cleanText := strings.TrimSpace(rest)
				cleanText = reTrailingHashes.ReplaceAllString(cleanText, "")
				cleanText = strings.TrimSpace(cleanText)
				if len(out) > 0 && out[len(out)-1] != "" {
					out = append(out, "")
				}
				if cleanText == "" {
					out = append(out, hashes)
				} else {
					out = append(out, hashes+" "+formatInlineText(cleanText))
				}
				if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" {
					out = append(out, "")
				}
				i++
				continue
			}
		}

		// 6. Preserve Setext headings (`Heading\n---` or `Heading\n===`) without breaking them.
		if isSetextUnderline(trimmed) && len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, trimmed)
			if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" {
				out = append(out, "")
			}
			i++
			continue
		}

		// 7. Thematic breaks (`---`, `***`, `___`).
		if reHRLine.MatchString(line) {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			out = append(out, trimmed)
			if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" {
				out = append(out, "")
			}
			i++
			continue
		}

		// 8. Blockquotes (`> ...`).
		if strings.HasPrefix(trimmed, ">") {
			out = append(out, formatBlockquoteLine(line))
			i++
			continue
		}

		// 9. Normal / list / prose line.
		out = append(out, formatBodyLine(line))
		i++
	}

	// Trim leading/trailing empty lines cleanly and ensure single trailing newline.
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

func isFenceClose(line string, fenceChar byte, minLen int) bool {
	s := strings.TrimSpace(line)
	for strings.HasPrefix(s, ">") {
		s = strings.TrimSpace(s[1:])
	}
	if len(s) < minLen {
		return false
	}
	for j := 0; j < len(s); j++ {
		if s[j] != fenceChar {
			return false
		}
	}
	return true
}

func isSetextUnderline(trimmed string) bool {
	if len(trimmed) < 2 {
		return false
	}
	ch := trimmed[0]
	if ch != '=' && ch != '-' {
		return false
	}
	for j := 0; j < len(trimmed); j++ {
		if trimmed[j] != ch {
			return false
		}
	}
	return true
}

func formatBlockquoteLine(line string) string {
	trimmed := strings.TrimRight(line, " \t")
	leadingEnd := 0
	for leadingEnd < len(trimmed) && (trimmed[leadingEnd] == ' ' || trimmed[leadingEnd] == '\t') {
		leadingEnd++
	}
	indent := trimmed[:leadingEnd]
	rest := trimmed[leadingEnd:]

	quoteDepth := 0
	for strings.HasPrefix(rest, ">") {
		quoteDepth++
		rest = rest[1:]
		rest = strings.TrimLeft(rest, " \t")
	}
	prefix := indent + strings.Repeat("> ", quoteDepth)
	if rest == "" {
		return strings.TrimRight(prefix, " ")
	}
	if m := reAlertTag.FindStringSubmatch(rest); m != nil {
		tail := strings.TrimSpace(m[2])
		if tail == "" {
			return prefix + m[1]
		}
		return prefix + m[1] + " " + formatInlineText(tail)
	}
	return prefix + formatBodyLine(rest)
}

func formatBodyLine(line string) string {
	trimmedRight := strings.TrimRight(line, " \t")
	if trimmedRight == "" {
		return ""
	}

	// Task or bullet list missing space after `-` or `+` (e.g., `-第一项`, `-[x]完成`, `- []待办`).
	if m := reBulletNoSpace.FindStringSubmatch(trimmedRight); m != nil {
		indent, marker, rest := m[1], m[2], m[3]
		// Do not alter CLI-style flags like `--verbose` if `-` is followed by `-` (caught by [^\s+-]),
		// or negative numbers like `-123` / `-0.5`.
		if marker == "-" && len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
			return formatInlineText(trimmedRight)
		}
		if marker == "+" && len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
			return formatInlineText(trimmedRight)
		}
		return indent + marker + " " + formatListRest(rest)
	}

	// Bullet list with 2+ spaces or single space after marker.
	if m := reBulletMultiSpace.FindStringSubmatch(trimmedRight); m != nil {
		return m[1] + m[2] + " " + formatListRest(m[3])
	}
	if m := reBulletNormal.FindStringSubmatch(trimmedRight); m != nil {
		return m[1] + m[2] + " " + formatListRest(m[3])
	}

	// Ordered list (`1.第一项` -> `1. 第一项`, `1.   item` -> `1. item`).
	if m := reOrderedLine.FindStringSubmatch(trimmedRight); m != nil {
		indent, num, delim, rest := m[1], m[2], m[3], m[4]
		if rest != "" {
			if rest[0] == ' ' || rest[0] == '\t' {
				return indent + num + delim + " " + formatListRest(strings.TrimLeft(rest, " \t"))
			}
			// Guard against decimals/versions at line start (e.g., `2.0版本`).
			if rest[0] < '0' || rest[0] > '9' {
				return indent + num + delim + " " + formatListRest(rest)
			}
		}
	}

	// Preserve leading indentation on continuation lines while formatting text.
	leading := len(trimmedRight) - len(strings.TrimLeft(trimmedRight, " \t"))
	return trimmedRight[:leading] + formatInlineText(trimmedRight[leading:])
}

func formatListRest(rest string) string {
	rest = strings.TrimSpace(rest)
	if m := reTaskBox.FindStringSubmatch(rest); m != nil {
		state := m[1]
		if state == "" {
			state = " "
		} else if state == "X" {
			state = "x"
		}
		tail := strings.TrimSpace(m[2])
		if tail == "" {
			return "[" + state + "]"
		}
		return "[" + state + "] " + formatInlineText(tail)
	}
	return formatInlineText(rest)
}

func looksLikeTableRow(line string) bool {
	s := strings.TrimSpace(line)
	return s != "" && strings.Contains(s, "|") && !strings.HasPrefix(s, "```") && !strings.HasPrefix(s, ">")
}

func isTableDelimiterLine(line string) bool {
	s := strings.TrimSpace(line)
	return strings.Contains(s, "|") && reTableDelim.MatchString(s)
}

func splitTableCells(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")

	var cells []string
	var cur strings.Builder
	inCode := false
	escaped := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			cur.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			cur.WriteByte(ch)
			escaped = true
			continue
		}
		if ch == '`' {
			inCode = !inCode
			cur.WriteByte(ch)
			continue
		}
		if ch == '|' && !inCode {
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(ch)
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	return cells
}

func formatTableBlock(rawRows []string) []string {
	rows := make([][]string, len(rawRows))
	maxCols := 0
	for rIdx, raw := range rawRows {
		cells := splitTableCells(raw)
		if rIdx != 1 {
			for cIdx, cell := range cells {
				cells[cIdx] = formatInlineText(cell)
			}
		} else {
			for cIdx, cell := range cells {
				cells[cIdx] = normalizeTableDelimCell(cell)
			}
		}
		rows[rIdx] = cells
		if len(cells) > maxCols {
			maxCols = len(cells)
		}
	}

	widths := make([]int, maxCols)
	for j := range widths {
		widths[j] = 3
	}
	for rIdx, cells := range rows {
		for len(cells) < maxCols {
			if rIdx == 1 {
				cells = append(cells, "---")
			} else {
				cells = append(cells, "")
			}
		}
		rows[rIdx] = cells
		for cIdx, cell := range cells {
			if rIdx == 1 {
				continue
			}
			if w := displayWidth(cell); w > widths[cIdx] {
				widths[cIdx] = w
			}
		}
	}

	out := make([]string, len(rows))
	for rIdx, cells := range rows {
		var b strings.Builder
		b.WriteString("|")
		for cIdx, cell := range cells {
			b.WriteByte(' ')
			if rIdx == 1 {
				b.WriteString(padDelimCell(cell, widths[cIdx]))
			} else {
				b.WriteString(cell)
				if pad := widths[cIdx] - displayWidth(cell); pad > 0 {
					b.WriteString(strings.Repeat(" ", pad))
				}
			}
			b.WriteString(" |")
		}
		out[rIdx] = b.String()
	}
	return out
}

func normalizeTableDelimCell(cell string) string {
	s := strings.TrimSpace(cell)
	leftColon := strings.HasPrefix(s, ":")
	rightColon := strings.HasSuffix(s, ":")
	switch {
	case leftColon && rightColon:
		return ":---:"
	case leftColon:
		return ":---"
	case rightColon:
		return "---:"
	default:
		return "---"
	}
}

func padDelimCell(delim string, width int) string {
	if width < 3 {
		width = 3
	}
	switch delim {
	case ":---:":
		if width <= 3 {
			return ":-:"
		}
		return ":" + strings.Repeat("-", width-2) + ":"
	case ":---":
		return ":" + strings.Repeat("-", width-1)
	case "---:":
		return strings.Repeat("-", width-1) + ":"
	default:
		return strings.Repeat("-", width)
	}
}

func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if isWideRune(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

func isWideRune(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) ||
		(r >= 0x3000 && r <= 0x303F) ||
		(r >= 0xFF01 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x1F300 && r <= 0x1FAFF)
}

// formatInlineText inserts spaces between CJK characters and Latin/digit tokens
// while skipping protected spans (inline code, links, URLs, HTML tags, math).
func formatInlineText(line string) string {
	if line == "" {
		return ""
	}
	matches := reProtectedSpan.FindAllStringIndex(line, -1)
	if len(matches) == 0 {
		return addPanguSpaces(line)
	}

	var b strings.Builder
	last := 0
	for _, span := range matches {
		if span[0] > last {
			gap := addPanguSpaces(line[last:span[0]])
			if shouldSpaceAfterToken(b.String(), gap) {
				b.WriteByte(' ')
			}
			b.WriteString(gap)
		}
		// Add boundary spacing around protected inline tokens (e.g., `中文 `code` 中文`).
		chunk := line[span[0]:span[1]]
		if b.Len() > 0 && shouldSpaceBeforeToken(b.String(), chunk) {
			b.WriteByte(' ')
		}
		b.WriteString(chunk)
		last = span[1]
	}
	if last < len(line) {
		tail := addPanguSpaces(line[last:])
		if shouldSpaceAfterToken(b.String(), tail) {
			b.WriteByte(' ')
		}
		b.WriteString(tail)
	}
	return b.String()
}

func shouldSpaceBeforeToken(prevOut, token string) bool {
	if !strings.HasPrefix(token, "`") && !strings.HasPrefix(token, "http") {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(prevOut)
	return isCJKRune(r)
}

func shouldSpaceAfterToken(prevOut, nextText string) bool {
	if prevOut == "" || nextText == "" {
		return false
	}
	lastByte := prevOut[len(prevOut)-1]
	if lastByte != '`' && lastByte != ')' && !isASCIIAlphaNum(rune(lastByte)) && lastByte != '/' {
		return false
	}
	r, _ := utf8.DecodeRuneInString(nextText)
	return isCJKRune(r)
}

func addPanguSpaces(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s) + 8)

	for i, r := range runes {
		if i > 0 {
			prev := runes[i-1]
			var prev2 rune
			if i >= 2 {
				prev2 = runes[i-2]
			}
			if (isCJKRune(prev) && isASCIIAlphaNum(r)) ||
				(isASCIIAlphaNum(prev) && isCJKRune(r)) ||
				(prev == '%' && prev2 >= '0' && prev2 <= '9' && isCJKRune(r)) {
				b.WriteByte(' ')
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isCJKRune(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

func isASCIIAlphaNum(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}
