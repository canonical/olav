package preview

import (
	"strings"
	"testing"
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

func containsLine(lines []string, needle string) bool {
	for _, line := range lines {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}
