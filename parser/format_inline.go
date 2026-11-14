package parser

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var reProtectedSpan = regexp.MustCompile("(````+[^`\n]*(?:`+[^`\n]+)*````+|```(?:[^`\n]|`[^`\n]|``[^`\n])*```|``(?:[^`\n]|`[^`\n])*``|`[^`\n]*`|!?\\[[^\\]\n]*\\]\\([^)\n]*\\)|<https?://[^>\n\\s]+>|<mailto:[^>\n\\s]+>|https?://[A-Za-z0-9\\-._~:/?#\\[\\]@!$&'*+,;=%]+|<[A-Za-z/!][^>\n]*>|\\$[^$\n]+\\$|\\\\.)")

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
