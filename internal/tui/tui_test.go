package tui

import (
	"strings"
	"testing"

	"github.com/canonical/olav/internal/oci"
	"github.com/charmbracelet/x/ansi"
)

func TestViewDoesNotExceedWindow(t *testing.T) {
	root := &oci.Node{Name: "/", Path: "/", IsDir: true}
	longJSON := []byte(`{"this-is-a-very-long-key-that-would-wrap-without-truncation":"this is a very long value that would otherwise force the pane to exceed its allocated dimensions and scroll the terminal","items":[1,2,3,4,5,6,7,8,9,10]}`)
	index := &oci.Node{Name: "index.json", Path: "/index.json", Data: longJSON, Parent: root}
	root.Children = []*oci.Node{index}
	layout := &oci.Layout{InputPath: strings.Repeat("very-long-input-path/", 20), Root: root, Files: map[string]*oci.Node{"/": root, "/index.json": index}}

	m := New(layout)
	m.width = 72
	m.height = 20
	m.selectOCI(1)
	if m.preview == nil {
		t.Fatal("expected preview")
	}
	m.preview.ScrollBy(100, m.previewHeight(), m.previewContentWidth())

	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) > m.height {
		t.Fatalf("view height = %d, want <= %d\n%s", len(lines), m.height, view)
	}
	for i, line := range lines {
		if w := ansi.StringWidth(line); w > m.width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i+1, w, m.width, line)
		}
	}
}

func TestLongRawPreviewWrapsWithLineNumbers(t *testing.T) {
	root := &oci.Node{Name: "/", Path: "/", IsDir: true}
	longLine := []byte("alpha-" + strings.Repeat("middle-", 20) + "omega")
	file := &oci.Node{Name: "config", Path: "/config", Data: longLine, Parent: root}
	root.Children = []*oci.Node{file}
	layout := &oci.Layout{InputPath: "fixture", Root: root, Files: map[string]*oci.Node{"/": root, "/config": file}}

	m := New(layout)
	m.width = 60
	m.height = 16
	m.selectOCI(1)
	m.focus = focusPreview

	view := m.View()
	if !strings.Contains(view, "alpha-") {
		t.Fatalf("expected start of long line in initial viewport:\n%s", view)
	}
	if !strings.Contains(previewLine(view, "alpha-"), "1 │") {
		t.Fatalf("expected line number gutter in preview:\n%s", view)
	}
	if !strings.Contains(view, "  │") {
		t.Fatalf("expected wrapped continuation gutter:\n%s", view)
	}
	if strings.Contains(previewLine(view, "alpha-"), "…") {
		t.Fatalf("wrapped raw preview line should not use ellipsis truncation:\n%s", view)
	}
	assertViewFits(t, view, m.width, m.height)
}

func TestPreviewToggleWrapAndLineNumbers(t *testing.T) {
	root := &oci.Node{Name: "/", Path: "/", IsDir: true}
	longLine := []byte("alpha-" + strings.Repeat("middle-", 20) + "omega")
	file := &oci.Node{Name: "config", Path: "/config", Data: longLine, Parent: root}
	root.Children = []*oci.Node{file}
	layout := &oci.Layout{InputPath: "fixture", Root: root, Files: map[string]*oci.Node{"/": root, "/config": file}}

	m := New(layout)
	m.width = 60
	m.height = 16
	m.selectOCI(1)
	m.focus = focusPreview
	m.toggleLineNumbers()
	view := m.View()
	if strings.Contains(view, "1 │") {
		t.Fatalf("expected line numbers to be hidden:\n%s", view)
	}

	m.toggleWrap()
	m.scrollPreviewHoriz(1000)
	view = m.View()
	if !strings.Contains(view, "omega") {
		t.Fatalf("expected horizontal scroll to reveal end when wrap is disabled:\n%s", view)
	}
	assertViewFits(t, view, m.width, m.height)
}

func TestLongPreviewKeepsBottomBorder(t *testing.T) {
	root := &oci.Node{Name: "/", Path: "/", IsDir: true}
	file := &oci.Node{Name: "log.txt", Path: "/log.txt", Data: []byte(strings.Repeat("line\n", 100)), Parent: root}
	root.Children = []*oci.Node{file}
	layout := &oci.Layout{InputPath: "fixture", Root: root, Files: map[string]*oci.Node{"/": root, "/log.txt": file}}

	m := New(layout)
	m.width = 80
	m.height = 20
	m.selectOCI(1)
	m.focus = focusPreview
	m.goBottom()

	view := m.View()
	assertViewFits(t, view, m.width, m.height)
	if !strings.Contains(view, "╰") || !strings.Contains(view, "╯") {
		t.Fatalf("expected bottom border corners to be visible:\n%s", view)
	}
}

func previewLine(view, marker string) string {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	return ""
}

func assertViewFits(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		t.Fatalf("view height = %d, want <= %d\n%s", len(lines), height, view)
	}
	for i, line := range lines {
		if w := ansi.StringWidth(line); w > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i+1, w, width, line)
		}
	}
}
