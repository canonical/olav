package preview

import (
	"bytes"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestJSONPrettyToggle(t *testing.T) {
	p := New("index.json", []byte(`{"schemaVersion":2,"manifests":[]}`), true)
	if !p.CanPretty {
		t.Fatal("expected JSON to be prettifiable")
	}
	if !p.PrettyEnabled {
		t.Fatal("expected pretty mode to be enabled")
	}
	if !strings.Contains(p.Notice, "Pretty-printed JSON") {
		t.Fatalf("unexpected notice: %q", p.Notice)
	}
	if !containsLine(p.Lines, "schemaVersion") {
		t.Fatalf("expected pretty JSON lines, got %#v", p.Lines)
	}

	msg := p.TogglePretty()
	if msg != "Raw JSON view enabled" {
		t.Fatalf("unexpected toggle message: %q", msg)
	}
	if p.PrettyEnabled {
		t.Fatal("expected pretty mode to be disabled")
	}
	if !strings.Contains(p.Notice, "Raw JSON") {
		t.Fatalf("unexpected notice: %q", p.Notice)
	}
}

func TestVisibleWrapsAndShowsLineNumbers(t *testing.T) {
	p := New("text", []byte("alpha-bravo-charlie-delta"), false)
	lines := p.Visible(4, 12)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "1 │") {
		t.Fatalf("expected line number gutter, got %#v", lines)
	}
	if !strings.Contains(joined, "  │") {
		t.Fatalf("expected continuation gutter, got %#v", lines)
	}
	if strings.Contains(joined, "…") {
		t.Fatalf("wrapped text should not use ellipsis: %#v", lines)
	}
}

func TestWrapAndLineNumberToggles(t *testing.T) {
	p := New("text", []byte("alpha-bravo-charlie-delta"), false)
	if msg := p.ToggleLineNumbers(); msg != "line numbers disabled" {
		t.Fatalf("unexpected message: %q", msg)
	}
	if strings.Contains(strings.Join(p.Visible(2, 12), "\n"), "│") {
		t.Fatalf("expected line numbers to be hidden")
	}
	if msg := p.ToggleWrap(2, 12); msg != "text wrapping disabled" {
		t.Fatalf("unexpected message: %q", msg)
	}
	p.ScrollHoriz(100, 12)
	if p.HScroll == 0 {
		t.Fatal("expected horizontal scroll when wrapping is disabled")
	}
	if msg := p.ToggleWrap(2, 12); msg != "text wrapping enabled" {
		t.Fatalf("unexpected message: %q", msg)
	}
	if p.HScroll != 0 {
		t.Fatal("expected horizontal scroll reset when wrapping is enabled")
	}
}

func TestSearchMatchesPlainLines(t *testing.T) {
	p := New("text", []byte("alpha\nbeta\ngamma beta"), false)
	p.SetSearch("beta")
	if len(p.SearchMatches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(p.SearchMatches))
	}
	if p.Scroll != 1 {
		t.Fatalf("expected scroll to first match, got %d", p.Scroll)
	}
	p.NextMatch(1)
	if p.Scroll != 2 {
		t.Fatalf("expected scroll to next match, got %d", p.Scroll)
	}
}

func TestBinaryPreview(t *testing.T) {
	p := New("blob", []byte{0x00, 0x01, 0x02}, false)
	if p.Text {
		t.Fatal("expected binary detection")
	}
	if !containsLine(p.Lines, "Binary file") {
		t.Fatalf("expected binary notice, got %#v", p.Lines)
	}
}

func TestSyntaxHighlightingByExtension(t *testing.T) {
	tests := []struct {
		title string
		data  string
		lang  string
	}{
		{title: "app.py", data: "def main():\n    print('hi')\n", lang: "Python"},
		{title: "entrypoint.sh", data: "#!/bin/bash\necho hi\n", lang: "Shell"},
		{title: "config.yaml", data: "name: test\nitems:\n  - one\n", lang: "YAML"},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			p := New(tt.title, []byte(tt.data), false)
			if !p.SyntaxColored || p.SyntaxLanguage != tt.lang {
				t.Fatalf("expected %s highlighting, got colored=%v lang=%q", tt.lang, p.SyntaxColored, p.SyntaxLanguage)
			}
			if !strings.Contains(strings.Join(p.Lines, "\n"), "\x1b[") {
				t.Fatalf("expected ANSI highlighting, got %#v", p.Lines)
			}
			if !strings.Contains(p.Notice, "Syntax-highlighted "+tt.lang) {
				t.Fatalf("unexpected notice: %q", p.Notice)
			}
		})
	}
}

func TestSyntaxHighlightingByShebang(t *testing.T) {
	p := New("script", []byte("#!/usr/bin/env python3\nprint('hi')\n"), false)
	if !p.SyntaxColored || p.SyntaxLanguage != "Python" {
		t.Fatalf("expected Python shebang detection, got colored=%v lang=%q", p.SyntaxColored, p.SyntaxLanguage)
	}
	p = New("script", []byte("#!/usr/bin/env bash\necho hi\n"), false)
	if !p.SyntaxColored || p.SyntaxLanguage != "Shell" {
		t.Fatalf("expected shell shebang detection, got colored=%v lang=%q", p.SyntaxColored, p.SyntaxLanguage)
	}
}

func TestPlainTextIsNotSyntaxHighlighted(t *testing.T) {
	p := New("notes.txt", []byte("def not_really_python:\n"), false)
	if p.SyntaxColored || p.SyntaxLanguage != "" {
		t.Fatalf("expected no syntax highlighting, got colored=%v lang=%q", p.SyntaxColored, p.SyntaxLanguage)
	}
	if strings.Contains(strings.Join(p.Lines, "\n"), "\x1b[") {
		t.Fatalf("did not expect ANSI highlighting, got %#v", p.Lines)
	}
}

func TestSearchWorksWithSyntaxHighlighting(t *testing.T) {
	p := New("app.py", []byte("def main():\n    print('needle')\n"), false)
	p.SetSearch("needle")
	if len(p.SearchMatches) != 1 || p.SearchMatches[0] != 1 {
		t.Fatalf("unexpected search matches: %#v", p.SearchMatches)
	}
}

func TestChiselManifestPreview(t *testing.T) {
	jsonl := `{"kind":"slice","name":"base","note":"a:b,c"}` + "\n" + `{"kind":"package","name":"bash","deps":["libc","readline"]}` + "\n"
	p, err := NewChiselManifest("manifest.wall", zstdBytes(t, []byte(jsonl)))
	if err != nil {
		t.Fatal(err)
	}
	if !p.JSONL || !p.ChiselManifest || !p.CanPretty || p.PrettyEnabled {
		t.Fatalf("unexpected chisel preview state: %#v", p)
	}
	if !strings.Contains(p.Notice, "Chisel manifest JSONL") {
		t.Fatalf("unexpected notice: %q", p.Notice)
	}
	if len(p.PlainLines) < 2 {
		t.Fatalf("expected JSONL lines, got %#v", p.PlainLines)
	}
	if !strings.Contains(strings.Join(p.Lines, "\n"), "\x1b[") {
		t.Fatalf("expected colored JSONL lines: %#v", p.Lines)
	}

	msg := p.TogglePretty()
	if msg != "JSONL readable separators enabled" {
		t.Fatalf("unexpected toggle message: %q", msg)
	}
	joined := strings.Join(p.PlainLines, "\n")
	if !strings.Contains(joined, `"kind": "slice"`) || !strings.Contains(joined, `"note": "a:b,c"`) {
		t.Fatalf("expected readable separators without changing string values: %s", joined)
	}
	if strings.Contains(joined, "\n  ") {
		t.Fatalf("expected one-line JSONL items, got %q", joined)
	}
}

func TestChiselManifestInvalidJSONLLine(t *testing.T) {
	p, err := NewChiselManifest("manifest.wall", zstdBytes(t, []byte("{\"ok\":true}\nnot-json\n")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Notice, "some lines are not valid JSON") {
		t.Fatalf("expected invalid JSONL notice, got %q", p.Notice)
	}
	if !containsLine(p.PlainLines, "not-json") {
		t.Fatalf("expected invalid line to be preserved: %#v", p.PlainLines)
	}
}

func zstdBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	return buf.Bytes()
}

func containsLine(lines []string, needle string) bool {
	for _, line := range lines {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}
