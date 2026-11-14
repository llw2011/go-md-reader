package parser

import (
	"regexp"
	"strings"
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
	reAlertTag         = regexp.MustCompile(`^(\[!(?:NOTE|TIP|IMPORTANT|WARNING|CAUTION)\])(.*)$`)
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
			if i < len(lines) && strings.TrimSpace(lines[i]) != "" && !strings.HasPrefix(strings.TrimSpace(prefix), ">") {
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
