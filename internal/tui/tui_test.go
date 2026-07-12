package tui

import (
	"archive/tar"
	"bytes"
	"strings"
	"testing"

	"github.com/canonical/olav/internal/layer"
	"github.com/canonical/olav/internal/oci"
	"github.com/canonical/olav/internal/preview"
	tea "github.com/charmbracelet/bubbletea"
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

func TestFooterAlwaysShowsHelpAndMessage(t *testing.T) {
	m := New(simpleLayout())
	m.width = 80
	m.height = 16
	m.message = "exported to olav-export/example"

	view := m.View()
	lines := strings.Split(view, "\n")
	if !strings.Contains(lines[len(lines)-1], "Tab focus") {
		t.Fatalf("expected help on bottom line:\n%s", view)
	}
	if !strings.Contains(lines[len(lines)-2], "exported to") {
		t.Fatalf("expected message on second bottom line:\n%s", view)
	}
	assertViewFits(t, view, m.width, m.height)
}

func TestSpaceTogglesOCIFolder(t *testing.T) {
	root := &oci.Node{Name: "/", Path: "/", IsDir: true}
	dir := &oci.Node{Name: "dir", Path: "/dir", IsDir: true, Parent: root}
	file := &oci.Node{Name: "file", Path: "/dir/file", Data: []byte("x"), Parent: dir}
	root.Children = []*oci.Node{dir}
	dir.Children = []*oci.Node{file}
	layout := &oci.Layout{InputPath: "fixture", Root: root, Files: map[string]*oci.Node{"/": root, "/dir": dir, "/dir/file": file}}
	m := New(layout)
	m.width = 80
	m.height = 16
	m.focus = focusOCI
	m.ociExpanded["/dir"] = true
	m.rebuildOCIRows()
	m.selectedOCI = m.indexOfOCI("/dir")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(Model)
	if m.ociExpanded["/dir"] {
		t.Fatalf("expected /dir to collapse")
	}
	if m.selectedOCINodePath() != "/dir" {
		t.Fatalf("expected selection to remain on /dir, got %s", m.selectedOCINodePath())
	}
}

func TestLayerLoadingOverlayAndSelection(t *testing.T) {
	root := &oci.Node{Name: "/", Path: "/", IsDir: true}
	blob := &oci.Node{Name: "abc", Path: "/blobs/sha256/abc", Data: []byte("not-a-tar"), Parent: root, Blob: &oci.BlobInfo{MediaType: "application/vnd.oci.image.layer.v1.tar"}}
	root.Children = []*oci.Node{blob}
	layout := &oci.Layout{InputPath: "fixture", Root: root, Files: map[string]*oci.Node{"/": root, "/blobs/sha256/abc": blob}}
	m := New(layout)
	m.width = 90
	m.height = 20
	m.selectOCI(1)

	if m.loadingLayerPath != blob.Path {
		t.Fatalf("expected loading path %q, got %q", blob.Path, m.loadingLayerPath)
	}
	if m.selectedOCINodePath() != blob.Path {
		t.Fatalf("expected selection to remain on blob")
	}
	view := m.View()
	if !strings.Contains(view, "Extracting tarball.") || !strings.Contains(view, "This can take a while") {
		t.Fatalf("expected centered extraction overlay:\n%s", view)
	}
	assertViewFits(t, view, m.width, m.height)
}

func TestSpacePagesPreview(t *testing.T) {
	m := New(simpleLayoutWithData([]byte(strings.Repeat("line\n", 40))))
	m.width = 80
	m.height = 16
	m.selectOCI(1)
	m.focus = focusPreview
	before := m.preview.Scroll
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(Model)
	if m.preview.Scroll <= before {
		t.Fatalf("expected preview to page down, before=%d after=%d", before, m.preview.Scroll)
	}
}

func TestSpaceTogglesLayerFolder(t *testing.T) {
	m := New(simpleLayout())
	m.width = 80
	m.height = 16
	m.focus = focusLayer
	m.currentLayer = &layer.Layer{Root: &layer.Entry{Name: "/", Path: "/", Type: tar.TypeDir}, Entries: map[string]*layer.Entry{}}
	dir := &layer.Entry{Name: "etc", Path: "/etc", Type: tar.TypeDir, Parent: m.currentLayer.Root}
	m.currentLayer.Root.Children = []*layer.Entry{dir}
	m.currentLayer.Entries["/"] = m.currentLayer.Root
	m.currentLayer.Entries["/etc"] = dir
	m.layerExpanded = map[string]bool{"/": true, "/etc": true}
	m.rebuildLayerRows()
	m.selectedLayerRow = m.indexOfLayer("/etc")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(Model)
	if m.layerExpanded["/etc"] {
		t.Fatal("expected /etc to collapse")
	}
}

func TestLayerLoadedSuccessAndStaleResult(t *testing.T) {
	m := New(simpleLayout())
	m.width = 80
	m.height = 16
	blob := &oci.Node{Name: "abc", Path: "/blobs/sha256/abc", Data: tinyTar(t), Parent: m.layout.Root, Blob: &oci.BlobInfo{MediaType: "application/vnd.oci.image.layer.v1.tar"}}
	m.layout.Root.Children = append(m.layout.Root.Children, blob)
	m.rebuildOCIRows()
	m.selectedOCI = m.indexOfOCI(blob.Path)
	m.loadingLayerPath = blob.Path
	lt := &layer.Layer{Title: blob.Path, Root: &layer.Entry{Name: "/", Path: "/", Type: tar.TypeDir}, Entries: map[string]*layer.Entry{"/": {Name: "/", Path: "/", Type: tar.TypeDir}}}

	updated, _ := m.Update(layerLoadedMsg{path: blob.Path, layer: lt})
	m = updated.(Model)
	if m.currentLayer != lt {
		t.Fatal("expected current layer to be applied")
	}

	m.selectedOCI = m.indexOfOCI("/index.json")
	stale := &layer.Layer{Title: "stale", Root: &layer.Entry{Name: "/", Path: "/", Type: tar.TypeDir}, Entries: map[string]*layer.Entry{"/": {Name: "/", Path: "/", Type: tar.TypeDir}}}
	updated, _ = m.Update(layerLoadedMsg{path: blob.Path, layer: stale})
	m = updated.(Model)
	if m.currentLayer == stale {
		t.Fatal("stale layer result should not replace current view")
	}
}

func TestSearchPromptAndHelpFooter(t *testing.T) {
	m := New(simpleLayout())
	m.width = 80
	m.height = 16
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = updated.(Model)
	view := m.View()
	lines := strings.Split(view, "\n")
	if !strings.Contains(lines[len(lines)-2], "/i") {
		t.Fatalf("expected search prompt on message line:\n%s", view)
	}
	if !strings.Contains(lines[len(lines)-1], "Tab focus") {
		t.Fatalf("expected help footer:\n%s", view)
	}
}

func TestQuestionMarkSetsMessage(t *testing.T) {
	m := New(simpleLayout())
	m.width = 80
	m.height = 16
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = updated.(Model)
	if !strings.Contains(m.message, "Keys:") {
		t.Fatalf("expected extended help message, got %q", m.message)
	}
}

func TestZoomTopLevelPreview(t *testing.T) {
	m := New(simpleLayout())
	m.width = 80
	m.height = 16
	m.selectOCI(1)
	m.focus = focusPreview
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	m = updated.(Model)
	if !m.zoomed || m.zoomTarget != focusPreview {
		t.Fatalf("expected top-level preview zoom, zoomed=%v target=%v", m.zoomed, m.zoomTarget)
	}
	view := m.View()
	if strings.Contains(view, "OCI Files") {
		t.Fatalf("expected zoomed view to hide OCI tree:\n%s", view)
	}
	if !strings.Contains(view, "Preview: /index.json") {
		t.Fatalf("expected preview in zoomed view:\n%s", view)
	}
	if got := m.previewContentWidth(); got != contentWidth(m.width) {
		t.Fatalf("previewContentWidth = %d, want %d", got, contentWidth(m.width))
	}
	assertViewFits(t, view, m.width, m.height)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	m = updated.(Model)
	if m.zoomed {
		t.Fatal("expected z to unzoom")
	}
}

func TestZoomOnlyWorksForFocusedPreview(t *testing.T) {
	m := New(simpleLayout())
	m.width = 80
	m.height = 16
	m.focus = focusOCI
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	m = updated.(Model)
	if m.zoomed {
		t.Fatal("expected tree focus not to zoom")
	}
	if !strings.Contains(m.message, "focus a text preview") {
		t.Fatalf("unexpected message: %q", m.message)
	}
}

func TestTabWhileZoomedShowsOverlay(t *testing.T) {
	m := New(simpleLayout())
	m.width = 80
	m.height = 16
	m.selectOCI(1)
	m.focus = focusPreview
	m.toggleZoom()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.focus != focusPreview {
		t.Fatalf("expected focus to remain preview, got %v", m.focus)
	}
	view := m.View()
	if !strings.Contains(view, "Press z again to exit zoom state.") {
		t.Fatalf("expected zoom exit overlay:\n%s", view)
	}
	assertViewFits(t, view, m.width, m.height)
}

func TestZoomInnerPreview(t *testing.T) {
	m := New(simpleLayout())
	m.width = 100
	m.height = 20
	m.focus = focusInnerPreview
	p := preview.New("/etc/os-release", []byte("NAME=test\n"), false)
	m.innerPreview = &p
	m.currentLayer = &layer.Layer{Root: &layer.Entry{Name: "/", Path: "/", Type: tar.TypeDir}}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	m = updated.(Model)
	if !m.zoomed || m.zoomTarget != focusInnerPreview {
		t.Fatalf("expected inner preview zoom, zoomed=%v target=%v", m.zoomed, m.zoomTarget)
	}
	view := m.View()
	if !strings.Contains(view, "File Preview") || strings.Contains(view, "OCI Files") {
		t.Fatalf("unexpected zoomed inner preview:\n%s", view)
	}
}

func simpleLayout() *oci.Layout {
	return simpleLayoutWithData([]byte(`{"schemaVersion":2}`))
}

func simpleLayoutWithData(data []byte) *oci.Layout {
	root := &oci.Node{Name: "/", Path: "/", IsDir: true}
	file := &oci.Node{Name: "index.json", Path: "/index.json", Data: data, Parent: root}
	root.Children = []*oci.Node{file}
	return &oci.Layout{InputPath: "fixture", Root: root, Files: map[string]*oci.Node{"/": root, "/index.json": file}}
}

func tinyTar(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "file", Typeflag: tar.TypeReg, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
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
