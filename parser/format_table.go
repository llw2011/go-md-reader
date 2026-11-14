package parser

import (
	"regexp"
	"strings"
	"unicode"
)

var reTableDelim = regexp.MustCompile(`^\|?\s*:?-{2,}:?\s*(?:\|\s*:?-{2,}:?\s*)*\|?$`)

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
