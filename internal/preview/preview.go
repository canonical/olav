package preview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

const MaxPreviewBytes = 2 << 20

type Preview struct {
	Title         string
	Notice        string
	Raw           []byte
	PlainLines    []string
	Lines         []string
	PrettyEnabled bool
	CanPretty     bool
	Text          bool
	Truncated     bool
	Scroll        int
	HScroll       int
	WrapText      bool
	LineNumbers   bool
	Search        string
	SearchMatches []int
	CurrentMatch  int
}

func New(title string, data []byte, prettyDefault bool) Preview {
	p := Preview{Title: title, Raw: data, PrettyEnabled: prettyDefault, WrapText: true, LineNumbers: true}
	if len(data) > MaxPreviewBytes {
		p.Raw = data[:MaxPreviewBytes]
		p.Truncated = true
	}
	p.Text = IsText(p.Raw)
	p.render()
	return p
}

func IsText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	if !utf8.Valid(data) {
		return false
	}
	limit := len(data)
	if limit > 8000 {
		limit = 8000
	}
	for _, b := range data[:limit] {
		if b == 0 {
			return false
		}
		if b < 0x09 || (b > 0x0d && b < 0x20) {
			return false
		}
	}
	return true
}

func (p *Preview) TogglePretty() string {
	if !p.CanPretty && !looksJSON(p.Raw) {
		return "Cannot prettify non-JSON content"
	}
	p.PrettyEnabled = !p.PrettyEnabled
	p.render()
	if p.PrettyEnabled {
		return "JSON prettification enabled"
	}
	return "Raw JSON view enabled"
}

func (p *Preview) ToggleWrap(height, width int) string {
	p.WrapText = !p.WrapText
	if p.WrapText {
		p.HScroll = 0
		p.ScrollBy(0, height, width)
		return "text wrapping enabled"
	}
	p.ScrollBy(0, height, width)
	p.ScrollHoriz(0, width)
	return "text wrapping disabled"
}

func (p *Preview) ToggleLineNumbers() string {
	p.LineNumbers = !p.LineNumbers
	if p.LineNumbers {
		return "line numbers enabled"
	}
	return "line numbers disabled"
}

func (p *Preview) render() {
	p.Notice = ""
	p.CanPretty = false
	if !p.Text {
		p.PlainLines = []string{
			"Binary file",
			"",
			"No text preview available.",
			fmt.Sprintf("Size: %d bytes", len(p.Raw)),
		}
		p.Lines = p.PlainLines
		return
	}

	content := p.Raw
	colorPretty := false
	if looksJSON(content) {
		p.CanPretty = true
		if p.PrettyEnabled {
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, content, "", "  "); err == nil {
				content = pretty.Bytes()
				colorPretty = true
				p.Notice = "Pretty-printed JSON; press p for raw"
			} else {
				p.Notice = "Invalid JSON; showing raw text"
			}
		} else {
			p.Notice = "Raw JSON; press p to prettify"
		}
	}
	if p.Truncated {
		if p.Notice != "" {
			p.Notice += " | "
		}
		p.Notice += fmt.Sprintf("preview truncated at %d bytes", MaxPreviewBytes)
	}
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	p.PlainLines = strings.Split(text, "\n")
	if colorPretty {
		p.Lines = strings.Split(colorJSON(text), "\n")
	} else {
		p.Lines = p.PlainLines
	}
	p.applySearch()
}

func colorJSON(s string) string {
	const (
		reset = "\x1b[0m"
		key   = "\x1b[36m"
		str   = "\x1b[32m"
		num   = "\x1b[33m"
		lit   = "\x1b[35m"
		punct = "\x1b[90m"
	)

	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"':
			end := i + 1
			escaped := false
			for end < len(s) {
				ch := s[end]
				if escaped {
					escaped = false
				} else if ch == '\\' {
					escaped = true
				} else if ch == '"' {
					end++
					break
				}
				end++
			}
			j := end
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			style := str
			if j < len(s) && s[j] == ':' {
				style = key
			}
			b.WriteString(style)
			b.WriteString(s[i:end])
			b.WriteString(reset)
			i = end
		case isNumberStart(c):
			end := i + 1
			for end < len(s) && isNumberPart(s[end]) {
				end++
			}
			b.WriteString(num)
			b.WriteString(s[i:end])
			b.WriteString(reset)
			i = end
		case strings.HasPrefix(s[i:], "true") || strings.HasPrefix(s[i:], "false") || strings.HasPrefix(s[i:], "null"):
			end := i
			for end < len(s) && unicode.IsLetter(rune(s[end])) {
				end++
			}
			b.WriteString(lit)
			b.WriteString(s[i:end])
			b.WriteString(reset)
			i = end
		case strings.ContainsRune("{}[],:", rune(c)):
			b.WriteString(punct)
			b.WriteByte(c)
			b.WriteString(reset)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

func isNumberStart(c byte) bool {
	return c == '-' || (c >= '0' && c <= '9')
}

func isNumberPart(c byte) bool {
	return (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-'
}

func looksJSON(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return false
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return false
	}
	var v any
	return json.Unmarshal(trimmed, &v) == nil
}

func (p *Preview) SetSearch(q string) {
	p.Search = q
	p.applySearch()
}

func (p *Preview) applySearch() {
	p.SearchMatches = nil
	p.CurrentMatch = -1
	if p.Search == "" {
		return
	}
	needle := strings.ToLower(p.Search)
	for i, line := range p.PlainLines {
		if strings.Contains(strings.ToLower(line), needle) {
			p.SearchMatches = append(p.SearchMatches, i)
		}
	}
	if len(p.SearchMatches) > 0 {
		p.CurrentMatch = 0
		p.Scroll = p.SearchMatches[0]
	}
}

func (p *Preview) NextMatch(delta int) {
	if len(p.SearchMatches) == 0 {
		return
	}
	p.CurrentMatch = (p.CurrentMatch + delta + len(p.SearchMatches)) % len(p.SearchMatches)
	p.Scroll = p.SearchMatches[p.CurrentMatch]
}

func (p *Preview) ScrollBy(delta, height, width int) {
	p.Scroll += delta
	max := len(p.displayRows(width)) - height
	if max < 0 {
		max = 0
	}
	if p.Scroll < 0 {
		p.Scroll = 0
	}
	if p.Scroll > max {
		p.Scroll = max
	}
}

func (p *Preview) ScrollHoriz(delta, width int) {
	if p.WrapText {
		p.HScroll = 0
		return
	}
	p.HScroll += delta
	max := p.maxLineWidth() - width
	if max < 0 {
		max = 0
	}
	if p.HScroll < 0 {
		p.HScroll = 0
	}
	if p.HScroll > max {
		p.HScroll = max
	}
}

func (p *Preview) SetHScroll(offset, width int) {
	p.HScroll = offset
	p.ScrollHoriz(0, width)
}

func (p *Preview) Visible(height, width int) []string {
	if height < 0 {
		height = 0
	}
	rows := p.displayRows(width)
	max := len(rows) - height
	if max < 0 {
		max = 0
	}
	if p.Scroll > max {
		p.Scroll = max
	}
	if p.Scroll < 0 {
		p.Scroll = 0
	}
	end := p.Scroll + height
	if end > len(rows) {
		end = len(rows)
	}
	if p.Scroll > end {
		return nil
	}
	return rows[p.Scroll:end]
}

func (p *Preview) displayRows(width int) []string {
	if width < 1 {
		return nil
	}
	gutterW := p.gutterWidth(width)
	textW := width - gutterW
	if textW < 1 {
		textW = 1
	}
	out := make([]string, 0, len(p.Lines))
	for i, line := range p.Lines {
		segments := p.visualSegments(line, textW)
		if len(segments) == 0 {
			segments = []string{""}
		}
		for j, segment := range segments {
			out = append(out, p.gutter(i+1, j > 0, gutterW)+segment)
		}
	}
	return out
}

func (p *Preview) visualSegments(line string, width int) []string {
	if !p.WrapText {
		return []string{p.visibleLine(line, width)}
	}
	if ansi.StringWidth(line) == 0 {
		return []string{""}
	}
	var segments []string
	rest := line
	for ansi.StringWidth(rest) > width {
		segment := ansi.Truncate(rest, width, "")
		segments = append(segments, segment)
		rest = ansi.TruncateLeft(rest, width, "")
	}
	segments = append(segments, rest)
	return segments
}

func (p *Preview) visibleLine(line string, width int) string {
	if width < 1 {
		return ""
	}
	if p.HScroll == 0 {
		return ansi.Truncate(line, width, "")
	}
	return ansi.Truncate(ansi.TruncateLeft(line, p.HScroll, ""), width, "")
}

func (p *Preview) gutterWidth(width int) int {
	if !p.LineNumbers || !p.Text {
		return 0
	}
	digits := len(strconv.Itoa(max(1, len(p.PlainLines))))
	gutterW := digits + 3
	if width-gutterW < 1 {
		return 0
	}
	return gutterW
}

func (p *Preview) gutter(line int, continuation bool, width int) string {
	if width == 0 {
		return ""
	}
	digits := width - 3
	if continuation {
		return strings.Repeat(" ", digits) + " │ "
	}
	return fmt.Sprintf("%*d │ ", digits, line)
}

func (p *Preview) maxLineWidth() int {
	max := 0
	for _, line := range p.Lines {
		if w := ansi.StringWidth(line); w > max {
			max = w
		}
	}
	return max
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
