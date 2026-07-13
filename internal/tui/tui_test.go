package tui

import (
	"archive/tar"
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/canonical/olav/internal/layer"
	"github.com/canonical/olav/internal/oci"
	"github.com/canonical/olav/internal/preview"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/klauspost/compress/zstd"
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

func TestToggleImageGraphView(t *testing.T) {
	m := New(simpleLayout())
	m.width = 80
	m.height = 16
	m.focus = focusOCI
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = updated.(Model)
	if m.leftView != leftViewGraph {
		t.Fatal("expected graph view")
	}
	view := m.View()
	if !strings.Contains(view, "Image Graph") {
		t.Fatalf("expected image graph header:\n%s", view)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = updated.(Model)
	if m.leftView != leftViewOCI {
		t.Fatal("expected OCI view")
	}
}

func TestGraphExpandedByDefault(t *testing.T) {
	layout := simpleLayout()
	child := &oci.GraphNode{Label: "child", Kind: oci.GraphIndex}
	grandchild := &oci.GraphNode{Label: "grandchild", Kind: oci.GraphManifest, BlobPath: "/index.json"}
	child.Children = []*oci.GraphNode{grandchild}
	layout.GraphRoot = &oci.GraphNode{Label: "index.json", Kind: oci.GraphRoot, Children: []*oci.GraphNode{child}}
	m := New(layout)
	m.width = 80
	m.height = 16
	m.focus = focusOCI
	m.leftView = leftViewGraph
	m.rebuildGraphRows()
	view := m.View()
	if !strings.Contains(view, "grandchild") {
		t.Fatalf("expected graph nodes expanded by default:\n%s", view)
	}
}

func TestCtrlSpaceTogglesAllGraphNodes(t *testing.T) {
	layout := simpleLayout()
	child := &oci.GraphNode{Label: "child", Kind: oci.GraphIndex}
	grandchild := &oci.GraphNode{Label: "grandchild", Kind: oci.GraphManifest, BlobPath: "/index.json"}
	child.Children = []*oci.GraphNode{grandchild}
	layout.GraphRoot = &oci.GraphNode{Label: "index.json", Kind: oci.GraphRoot, Children: []*oci.GraphNode{child}}
	m := New(layout)
	m.width = 80
	m.height = 16
	m.focus = focusOCI
	m.leftView = leftViewGraph
	m.rebuildGraphRows()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlAt})
	m = updated.(Model)
	view := m.View()
	if strings.Contains(view, "grandchild") {
		t.Fatalf("expected ctrl+space to collapse graph nodes:\n%s", view)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlAt})
	m = updated.(Model)
	view = m.View()
	if !strings.Contains(view, "grandchild") {
		t.Fatalf("expected ctrl+space to expand graph nodes:\n%s", view)
	}
}

func TestGraphSelectionPreviewsBlob(t *testing.T) {
	layout := simpleLayout()
	manifest := &oci.GraphNode{Label: "manifest abc", Kind: oci.GraphManifest, BlobPath: "/index.json", Digest: "sha256:abc"}
	layout.GraphRoot = &oci.GraphNode{Label: "index.json", Kind: oci.GraphRoot, Children: []*oci.GraphNode{manifest}}
	m := New(layout)
	m.width = 80
	m.height = 16
	m.focus = focusOCI
	m.leftView = leftViewGraph
	m.graphExpanded[m.graphKey(layout.GraphRoot)] = true
	m.rebuildGraphRows()
	m.selectGraph(1)
	if m.selectedOCINodePath() != "/index.json" {
		t.Fatalf("expected graph selection to sync raw OCI selection, got %s", m.selectedOCINodePath())
	}
	if m.preview == nil || !strings.Contains(strings.Join(m.preview.PlainLines, "\n"), "schemaVersion") {
		t.Fatalf("expected graph blob preview, got %#v", m.preview)
	}
}

func TestGraphLayerOpenDisplaysAfterLoad(t *testing.T) {
	t.Setenv("MAX_NUM_AUTO_TARBALL_EXTRACTION", "0")
	layout := layoutWithLayerNodes(1)
	layerNode := layout.Files["/blobs/sha256/layer0"]
	layout.GraphRoot = &oci.GraphNode{Label: "index.json", Kind: oci.GraphRoot, Children: []*oci.GraphNode{{Label: "layer 1", Kind: oci.GraphLayer, BlobPath: layerNode.Path, Digest: layerNode.Blob.Digest}}}
	m := New(layout)
	m.width = 90
	m.height = 20
	m.focus = focusOCI
	m.leftView = leftViewGraph
	m.rebuildGraphRows()
	m.selectGraph(1)
	m.openOrExpand()
	if m.loadingLayerPath != layerNode.Path {
		t.Fatalf("expected explicit graph open to start layer load, got %q", m.loadingLayerPath)
	}
	lt := mustOpenLayerForTest(t, layerNode.Path, layerNode.Blob.MediaType, layerNode.Data)
	updated, _ := m.Update(layerLoadedMsg{path: layerNode.Path, layer: lt})
	m = updated.(Model)
	if m.currentLayer != lt {
		t.Fatal("expected graph-opened layer to be displayed after load")
	}
	view := m.View()
	if strings.Contains(view, "Select a file to preview") || !strings.Contains(view, "Layer:") {
		t.Fatalf("expected layer pane after graph open:\n%s", view)
	}
}

func TestGraphLayerStaleLoadDoesNotDisplay(t *testing.T) {
	t.Setenv("MAX_NUM_AUTO_TARBALL_EXTRACTION", "0")
	layout := layoutWithLayerNodes(1)
	layerNode := layout.Files["/blobs/sha256/layer0"]
	summary := &oci.GraphNode{Label: "linux/amd64", Kind: oci.GraphPlatform, Summary: []string{"Platform: linux/amd64"}}
	layout.GraphRoot = &oci.GraphNode{Label: "index.json", Kind: oci.GraphRoot, Children: []*oci.GraphNode{{Label: "layer 1", Kind: oci.GraphLayer, BlobPath: layerNode.Path, Digest: layerNode.Blob.Digest}, summary}}
	m := New(layout)
	m.width = 90
	m.height = 20
	m.focus = focusOCI
	m.leftView = leftViewGraph
	m.rebuildGraphRows()
	m.selectGraph(1)
	m.openOrExpand()
	m.selectGraph(2)
	lt := mustOpenLayerForTest(t, layerNode.Path, layerNode.Blob.MediaType, layerNode.Data)
	updated, _ := m.Update(layerLoadedMsg{path: layerNode.Path, layer: lt})
	m = updated.(Model)
	if m.currentLayer == lt {
		t.Fatal("stale graph layer load should not replace current graph selection")
	}
	if m.layerCache[layerNode.Path] != lt {
		t.Fatal("stale graph layer load should still be cached")
	}
}

func TestGraphSummarySelection(t *testing.T) {
	layout := simpleLayout()
	summary := &oci.GraphNode{Label: "linux/amd64", Kind: oci.GraphPlatform, Summary: []string{"Platform: linux/amd64"}}
	layout.GraphRoot = &oci.GraphNode{Label: "index.json", Kind: oci.GraphRoot, Children: []*oci.GraphNode{summary}}
	m := New(layout)
	m.leftView = leftViewGraph
	m.graphExpanded[m.graphKey(layout.GraphRoot)] = true
	m.rebuildGraphRows()
	m.selectGraph(1)
	if m.preview == nil || !strings.Contains(strings.Join(m.preview.PlainLines, "\n"), "Platform: linux/amd64") {
		t.Fatalf("expected summary preview, got %#v", m.preview)
	}
}

func TestGraphSearchIncludesSummaryAndAnnotations(t *testing.T) {
	layout := simpleLayout()
	n := &oci.GraphNode{Label: "linux/amd64", Kind: oci.GraphPlatform, Summary: []string{"Platform: linux/amd64", "org.opencontainers.image.version: 24.10"}, Annotations: map[string]string{"custom": "needle-value"}}
	layout.GraphRoot = &oci.GraphNode{Label: "index.json", Kind: oci.GraphRoot, Children: []*oci.GraphNode{n}}
	m := New(layout)
	m.leftView = leftViewGraph
	m.graphExpanded[m.graphKey(layout.GraphRoot)] = true
	m.rebuildGraphRows()
	m.searchMode = true
	m.searchTarget = focusOCI
	m.searchQuery = "needle-value"
	m.applySearch()
	if m.selectedGraph != 1 {
		t.Fatalf("expected graph search to select annotation node, got %d", m.selectedGraph)
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

func TestLayerAutoExtractionLimitPlaceholder(t *testing.T) {
	t.Setenv("MAX_NUM_AUTO_TARBALL_EXTRACTION", "1")
	layout := layoutWithLayerNodes(2)
	m := New(layout)
	m.width = 90
	m.height = 20
	m.selectOCI(m.indexOfOCI("/blobs/sha256/layer0"))
	if m.loadingLayerPath != "" {
		t.Fatalf("did not expect auto extraction, got loading %q", m.loadingLayerPath)
	}
	if m.preview == nil || !strings.Contains(strings.Join(m.preview.PlainLines, "\n"), "not opened automatically") {
		t.Fatalf("expected placeholder preview, got %#v", m.preview)
	}
}

func TestExplicitLayerOpenOverLimit(t *testing.T) {
	t.Setenv("MAX_NUM_AUTO_TARBALL_EXTRACTION", "0")
	layout := layoutWithLayerNodes(1)
	m := New(layout)
	m.width = 90
	m.height = 20
	m.selectedOCI = m.indexOfOCI("/blobs/sha256/layer0")
	m.focus = focusOCI
	m.openOrExpand()
	if m.loadingLayerPath != "/blobs/sha256/layer0" {
		t.Fatalf("expected explicit open to start extraction, got %q", m.loadingLayerPath)
	}
}

func TestLayerAutoExtractionUnderLimit(t *testing.T) {
	t.Setenv("MAX_NUM_AUTO_TARBALL_EXTRACTION", "3")
	layout := layoutWithLayerNodes(1)
	m := New(layout)
	m.width = 90
	m.height = 20
	m.selectOCI(m.indexOfOCI("/blobs/sha256/layer0"))
	if m.loadingLayerPath != "/blobs/sha256/layer0" {
		t.Fatalf("expected auto extraction under limit, got %q", m.loadingLayerPath)
	}
}

func TestCachedLayerOpensOverLimit(t *testing.T) {
	t.Setenv("MAX_NUM_AUTO_TARBALL_EXTRACTION", "0")
	layout := layoutWithLayerNodes(1)
	m := New(layout)
	m.width = 90
	m.height = 20
	root := &layer.Entry{Name: "/", Path: "/", Type: tar.TypeDir}
	cached := &layer.Layer{Title: "cached", Root: root, Entries: map[string]*layer.Entry{"/": root}}
	m.layerCache["/blobs/sha256/layer0"] = cached
	m.selectOCI(m.indexOfOCI("/blobs/sha256/layer0"))
	if m.currentLayer != cached {
		t.Fatal("expected cached layer to open even over auto extraction limit")
	}
}

func TestAutoExtractionLimitEnvParsing(t *testing.T) {
	t.Setenv("MAX_NUM_AUTO_TARBALL_EXTRACTION", "7")
	if got := autoExtractLayerLimitFromEnv(); got != 7 {
		t.Fatalf("got %d", got)
	}
	t.Setenv("MAX_NUM_AUTO_TARBALL_EXTRACTION", "bad")
	if got := autoExtractLayerLimitFromEnv(); got != 3 {
		t.Fatalf("invalid env should fall back to 3, got %d", got)
	}
	t.Setenv("MAX_NUM_AUTO_TARBALL_EXTRACTION", "0")
	if got := autoExtractLayerLimitFromEnv(); got != 0 {
		t.Fatalf("zero should be accepted, got %d", got)
	}
}

func TestLargeBlobPreviewDeferredUntilExplicitOpen(t *testing.T) {
	t.Setenv("MAX_AUTO_TEXT_PREVIEW_BYTES", "4")
	layout := layoutWithLargeBlob([]byte("hello world"))
	m := New(layout)
	m.width = 90
	m.height = 20
	m.selectOCI(m.indexOfOCI("/blobs/sha256/big"))
	if m.preview == nil || !strings.Contains(strings.Join(m.preview.PlainLines, "\n"), "not opened automatically") {
		t.Fatalf("expected large blob placeholder, got %#v", m.preview)
	}
	m.openOrExpand()
	if m.preview == nil || !strings.Contains(strings.Join(m.preview.PlainLines, "\n"), "hello world") {
		t.Fatalf("expected explicit open to preview blob, got %#v", m.preview)
	}
}

func TestMaxAutoTextPreviewEnvParsing(t *testing.T) {
	t.Setenv("MAX_AUTO_TEXT_PREVIEW_BYTES", "7")
	if got := maxAutoTextPreviewBytesFromEnv(); got != 7 {
		t.Fatalf("got %d", got)
	}
	t.Setenv("MAX_AUTO_TEXT_PREVIEW_BYTES", "bad")
	if got := maxAutoTextPreviewBytesFromEnv(); got != 1<<20 {
		t.Fatalf("invalid env should fall back to default, got %d", got)
	}
	t.Setenv("MAX_AUTO_TEXT_PREVIEW_BYTES", "0")
	if got := maxAutoTextPreviewBytesFromEnv(); got != 0 {
		t.Fatalf("zero should be accepted, got %d", got)
	}
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

func TestZoomOverlayDismissesOnNextKeyAndKeyStillWorks(t *testing.T) {
	m := New(simpleLayoutWithData([]byte(strings.Repeat("line\n", 40))))
	m.width = 80
	m.height = 16
	m.selectOCI(1)
	m.focus = focusPreview
	m.toggleZoom()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.overlayMessage == "" {
		t.Fatal("expected zoom overlay after tab")
	}
	before := m.preview.Scroll
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)
	if m.overlayMessage != "" {
		t.Fatalf("expected overlay to be dismissed, got %q", m.overlayMessage)
	}
	if m.preview.Scroll <= before {
		t.Fatalf("expected key action to still run, before=%d after=%d", before, m.preview.Scroll)
	}
}

func TestZoomOverlayDismissesWhenTogglingWrap(t *testing.T) {
	m := New(simpleLayout())
	m.width = 80
	m.height = 16
	m.selectOCI(1)
	m.focus = focusPreview
	m.toggleZoom()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.overlayMessage == "" {
		t.Fatal("expected zoom overlay after tab")
	}
	before := m.preview.WrapText
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(Model)
	if m.overlayMessage != "" {
		t.Fatalf("expected overlay to be dismissed, got %q", m.overlayMessage)
	}
	if m.preview.WrapText == before {
		t.Fatal("expected w to toggle wrapping while dismissing overlay")
	}
}

func TestQWhileZoomedExitsZoomWithoutQuitting(t *testing.T) {
	m := New(simpleLayout())
	m.width = 80
	m.height = 16
	m.selectOCI(1)
	m.focus = focusPreview
	m.toggleZoom()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("expected q while zoomed not to quit")
	}
	if m.zoomed {
		t.Fatal("expected q while zoomed to exit zoom")
	}
	if !strings.Contains(m.message, "zoom disabled") {
		t.Fatalf("unexpected message: %q", m.message)
	}
}

func TestCtrlCWhileZoomedStillQuits(t *testing.T) {
	m := New(simpleLayout())
	m.width = 80
	m.height = 16
	m.selectOCI(1)
	m.focus = focusPreview
	m.toggleZoom()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected ctrl+c while zoomed to quit")
	}
}

func TestShiftTabMovesFocusBackward(t *testing.T) {
	m := New(simpleLayout())
	m.width = 80
	m.height = 16
	m.selectOCI(1)
	m.focus = focusPreview
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = updated.(Model)
	if m.focus != focusOCI {
		t.Fatalf("expected shift+tab from preview to focus OCI, got %v", m.focus)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = updated.(Model)
	if m.focus != focusPreview {
		t.Fatalf("expected shift+tab from OCI to wrap to preview, got %v", m.focus)
	}
}

func TestShiftTabInThreePaneMode(t *testing.T) {
	m := New(simpleLayout())
	m.width = 100
	m.height = 20
	m.currentLayer = &layer.Layer{Title: "layer", Root: &layer.Entry{Name: "/", Path: "/", Type: tar.TypeDir}, Entries: map[string]*layer.Entry{}}
	entry := &layer.Entry{Name: "file", Path: "/file", Type: tar.TypeReg, Data: []byte("text"), Parent: m.currentLayer.Root}
	m.currentLayer.Root.Children = []*layer.Entry{entry}
	m.currentLayer.Entries["/"] = m.currentLayer.Root
	m.currentLayer.Entries[entry.Path] = entry
	m.layerExpanded = map[string]bool{"/": true}
	m.rebuildLayerRows()
	m.selectLayer(m.indexOfLayer(entry.Path))
	m.focus = focusInnerPreview
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = updated.(Model)
	if m.focus != focusLayer {
		t.Fatalf("expected shift+tab from inner preview to layer, got %v", m.focus)
	}
}

func TestShiftTabWhileZoomedShowsOverlay(t *testing.T) {
	m := New(simpleLayout())
	m.width = 80
	m.height = 16
	m.selectOCI(1)
	m.focus = focusPreview
	m.toggleZoom()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = updated.(Model)
	if m.focus != focusPreview {
		t.Fatalf("expected focus to remain preview, got %v", m.focus)
	}
	view := m.View()
	if !strings.Contains(view, "Press z again to exit zoom state.") {
		t.Fatalf("expected zoom exit overlay:\n%s", view)
	}
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

func TestSelectChiselManifestCreatesCachedPreview(t *testing.T) {
	m := New(simpleLayout())
	m.width = 100
	m.height = 20
	m.currentLayer = &layer.Layer{Title: "layer", Root: &layer.Entry{Name: "/", Path: "/", Type: tar.TypeDir}, Entries: map[string]*layer.Entry{}}
	entry := &layer.Entry{Name: "manifest.wall", Path: "/custom/manifest.wall", Type: tar.TypeReg, Data: zstdTestBytes(t, []byte(`{"kind":"slice","name":"base"}`+"\n")), Parent: m.currentLayer.Root}
	m.currentLayer.Root.Children = []*layer.Entry{entry}
	m.currentLayer.Entries["/"] = m.currentLayer.Root
	m.currentLayer.Entries[entry.Path] = entry
	m.layerExpanded = map[string]bool{"/": true}
	m.rebuildLayerRows()
	m.selectLayer(m.indexOfLayer(entry.Path))
	if m.innerPreview == nil || !m.innerPreview.ChiselManifest {
		t.Fatalf("expected chisel manifest preview, got %#v", m.innerPreview)
	}
	if len(m.chiselPreviewCache) != 1 {
		t.Fatalf("expected one cached preview, got %d", len(m.chiselPreviewCache))
	}
	first := m.innerPreview
	m.selectLayer(m.indexOfLayer(entry.Path))
	if m.innerPreview != first {
		t.Fatal("expected cached preview reuse")
	}
}

func TestNonZstdManifestWallIsNotSpecial(t *testing.T) {
	m := New(simpleLayout())
	m.currentLayer = &layer.Layer{Title: "layer", Root: &layer.Entry{Name: "/", Path: "/", Type: tar.TypeDir}, Entries: map[string]*layer.Entry{}}
	entry := &layer.Entry{Name: "manifest.wall", Path: "/manifest.wall", Type: tar.TypeReg, Data: []byte("plain text"), Parent: m.currentLayer.Root}
	m.currentLayer.Root.Children = []*layer.Entry{entry}
	m.currentLayer.Entries["/"] = m.currentLayer.Root
	m.currentLayer.Entries[entry.Path] = entry
	m.layerExpanded = map[string]bool{"/": true}
	m.rebuildLayerRows()
	m.selectLayer(m.indexOfLayer(entry.Path))
	if m.innerPreview == nil {
		t.Fatal("expected normal text preview")
	}
	if m.innerPreview.ChiselManifest {
		t.Fatal("did not expect chisel preview for non-zstd manifest.wall")
	}
}

func TestSymlinkToTextPreviewsTarget(t *testing.T) {
	m := modelWithLayerEntries()
	target := &layer.Entry{Name: "target", Path: "/etc/target", Type: tar.TypeReg, Data: []byte("hello target"), Parent: m.currentLayer.Root}
	link := &layer.Entry{Name: "link", Path: "/link", Type: tar.TypeSymlink, LinkName: "/etc/target", Parent: m.currentLayer.Root}
	addLayerEntry(m.currentLayer, target)
	addLayerEntry(m.currentLayer, link)
	m.rebuildLayerRows()
	m.selectLayer(m.indexOfLayer(link.Path))
	if m.innerPreview == nil {
		t.Fatal("expected symlink target preview")
	}
	if !strings.Contains(m.innerPreview.Title, "/link -> /etc/target") {
		t.Fatalf("unexpected preview title: %q", m.innerPreview.Title)
	}
	if !strings.Contains(strings.Join(m.innerPreview.PlainLines, "\n"), "hello target") {
		t.Fatalf("unexpected preview lines: %#v", m.innerPreview.PlainLines)
	}
}

func TestFollowSymlinkJumpsToTarget(t *testing.T) {
	m := modelWithLayerEntries()
	dir := &layer.Entry{Name: "etc", Path: "/etc", Type: tar.TypeDir, Parent: m.currentLayer.Root}
	target := &layer.Entry{Name: "target", Path: "/etc/target", Type: tar.TypeReg, Data: []byte("hello"), Parent: dir}
	link := &layer.Entry{Name: "link", Path: "/link", Type: tar.TypeSymlink, LinkName: "/etc/target", Parent: m.currentLayer.Root}
	addLayerEntry(m.currentLayer, dir)
	addLayerEntry(m.currentLayer, target)
	addLayerEntry(m.currentLayer, link)
	m.layerExpanded = map[string]bool{"/": true}
	m.rebuildLayerRows()
	m.focus = focusLayer
	m.selectLayer(m.indexOfLayer(link.Path))
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = updated.(Model)
	if m.layerRows[m.selectedLayerRow].entry.Path != target.Path {
		t.Fatalf("expected selection on target, got %s", m.layerRows[m.selectedLayerRow].entry.Path)
	}
	if !m.layerExpanded["/etc"] {
		t.Fatal("expected target parent to be expanded")
	}
}

func TestFollowMissingSymlinkShowsOverlay(t *testing.T) {
	m := modelWithLayerEntries()
	link := &layer.Entry{Name: "link", Path: "/link", Type: tar.TypeSymlink, LinkName: "/missing", Parent: m.currentLayer.Root}
	addLayerEntry(m.currentLayer, link)
	m.rebuildLayerRows()
	m.focus = focusLayer
	m.selectLayer(m.indexOfLayer(link.Path))
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = updated.(Model)
	view := m.View()
	if !strings.Contains(view, "Link target does not exist: /missing") {
		t.Fatalf("expected missing target overlay:\n%s", view)
	}
}

func TestFStillPagesPreview(t *testing.T) {
	m := New(simpleLayoutWithData([]byte(strings.Repeat("line\n", 40))))
	m.width = 80
	m.height = 16
	m.selectOCI(1)
	m.focus = focusPreview
	before := m.preview.Scroll
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = updated.(Model)
	if m.preview.Scroll <= before {
		t.Fatalf("expected f to page preview, before=%d after=%d", before, m.preview.Scroll)
	}
}

func simpleLayout() *oci.Layout {
	return simpleLayoutWithData([]byte(`{"schemaVersion":2}`))
}

func layoutWithLayerNodes(count int) *oci.Layout {
	root := &oci.Node{Name: "/", Path: "/", IsDir: true}
	blobs := &oci.Node{Name: "blobs", Path: "/blobs", IsDir: true, Parent: root}
	sha := &oci.Node{Name: "sha256", Path: "/blobs/sha256", IsDir: true, Parent: blobs}
	root.Children = []*oci.Node{blobs}
	blobs.Children = []*oci.Node{sha}
	files := map[string]*oci.Node{"/": root, "/blobs": blobs, "/blobs/sha256": sha}
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("layer%d", i)
		p := "/blobs/sha256/" + name
		node := &oci.Node{Name: name, Path: p, Parent: sha, Size: 10, Data: tinyTarForLayout(), Blob: &oci.BlobInfo{MediaType: "application/vnd.oci.image.layer.v1.tar", Role: "layer"}}
		sha.Children = append(sha.Children, node)
		files[p] = node
	}
	return &oci.Layout{InputPath: "fixture", Root: root, Files: files}
}

func layoutWithLargeBlob(data []byte) *oci.Layout {
	root := &oci.Node{Name: "/", Path: "/", IsDir: true}
	blobs := &oci.Node{Name: "blobs", Path: "/blobs", IsDir: true, Parent: root}
	sha := &oci.Node{Name: "sha256", Path: "/blobs/sha256", IsDir: true, Parent: blobs}
	node := &oci.Node{Name: "big", Path: "/blobs/sha256/big", Parent: sha, Size: int64(len(data)), Data: data, Blob: &oci.BlobInfo{MediaType: "text/plain", Role: "blob"}}
	root.Children = []*oci.Node{blobs}
	blobs.Children = []*oci.Node{sha}
	sha.Children = []*oci.Node{node}
	return &oci.Layout{InputPath: "fixture", Root: root, Files: map[string]*oci.Node{"/": root, "/blobs": blobs, "/blobs/sha256": sha, node.Path: node}}
}

func tinyTarForLayout() []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "file", Typeflag: tar.TypeReg, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	return buf.Bytes()
}

func mustOpenLayerForTest(t *testing.T, title, mediaType string, data []byte) *layer.Layer {
	t.Helper()
	lt, err := layer.Open(title, mediaType, data)
	if err != nil {
		t.Fatal(err)
	}
	return lt
}

func modelWithLayerEntries() Model {
	m := New(simpleLayout())
	m.width = 100
	m.height = 20
	root := &layer.Entry{Name: "/", Path: "/", Type: tar.TypeDir}
	m.currentLayer = &layer.Layer{Title: "layer", Root: root, Entries: map[string]*layer.Entry{"/": root}}
	m.layerExpanded = map[string]bool{"/": true}
	m.focus = focusLayer
	return m
}

func addLayerEntry(l *layer.Layer, e *layer.Entry) {
	l.Entries[e.Path] = e
	if e.Parent != nil {
		e.Parent.Children = append(e.Parent.Children, e)
	}
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

func zstdTestBytes(t *testing.T, data []byte) []byte {
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
