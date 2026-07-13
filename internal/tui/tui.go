package tui

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/canonical/olav/internal/export"
	"github.com/canonical/olav/internal/layer"
	"github.com/canonical/olav/internal/oci"
	"github.com/canonical/olav/internal/preview"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type focus int

const (
	focusOCI focus = iota
	focusPreview
	focusLayer
	focusInnerPreview
)

type leftViewMode int

const (
	leftViewOCI leftViewMode = iota
	leftViewGraph
)

type Model struct {
	layout *oci.Layout

	width          int
	height         int
	focus          focus
	message        string
	pending        tea.Cmd
	zoomed         bool
	zoomTarget     focus
	overlayMessage string

	ociRows       []treeRow
	selectedOCI   int
	ociExpanded   map[string]bool
	leftView      leftViewMode
	graphRows     []graphRow
	selectedGraph int
	graphExpanded map[string]bool
	searchMode    bool
	searchTarget  focus
	searchQuery   string

	preview *preview.Preview

	layerCache          map[string]*layer.Layer
	currentLayer        *layer.Layer
	loadingLayerPath    string
	layerBlobCount      int
	autoExtractLimit    int
	maxAutoPreviewBytes int64
	layerRows           []layerRow
	selectedLayerRow    int
	layerExpanded       map[string]bool
	innerPreview        *preview.Preview
	chiselPreviewCache  map[string]*preview.Preview
}

type layerLoadedMsg struct {
	path  string
	layer *layer.Layer
	err   error
}

const helpText = "Tab/Shift+Tab focus | v view | j/k move | Space toggle/page | f follow/page | z zoom | / search | p pretty | e export | q quit"

type treeRow struct {
	node  *oci.Node
	depth int
}

type layerRow struct {
	entry *layer.Entry
	depth int
}

type graphRow struct {
	node  *oci.GraphNode
	depth int
}

var (
	border      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	focused     = border.BorderForeground(lipgloss.Color("63"))
	unfocused   = border.BorderForeground(lipgloss.Color("240"))
	headerStyle = lipgloss.NewStyle().Bold(true)
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	selectStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57"))
)

func New(layout *oci.Layout) Model {
	m := Model{
		layout:              layout,
		ociExpanded:         map[string]bool{"/": true, "/blobs": true, "/blobs/sha256": true},
		graphExpanded:       map[string]bool{},
		layerCache:          map[string]*layer.Layer{},
		layerExpanded:       map[string]bool{"/": true},
		chiselPreviewCache:  map[string]*preview.Preview{},
		layerBlobCount:      countLayerBlobs(layout),
		autoExtractLimit:    autoExtractLayerLimitFromEnv(),
		maxAutoPreviewBytes: maxAutoTextPreviewBytesFromEnv(),
	}
	m.rebuildOCIRows()
	m.expandAllGraphNodes()
	m.rebuildGraphRows()
	m.selectOCI(0)
	return m
}

func maxAutoTextPreviewBytesFromEnv() int64 {
	const defaultLimit = int64(1 << 20)
	value := os.Getenv("MAX_AUTO_TEXT_PREVIEW_BYTES")
	if value == "" {
		return defaultLimit
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return defaultLimit
	}
	return n
}

func autoExtractLayerLimitFromEnv() int {
	value := os.Getenv("MAX_NUM_AUTO_TARBALL_EXTRACTION")
	if value == "" {
		return 3
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 3
	}
	return n
}

func countLayerBlobs(layout *oci.Layout) int {
	if layout == nil {
		return 0
	}
	count := 0
	for _, node := range layout.Files {
		if isLayerNode(node) {
			count++
		}
	}
	return count
}

func isLayerNode(node *oci.Node) bool {
	return node != nil && node.Blob != nil && oci.IsLayerMediaType(node.Blob.MediaType)
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case layerLoadedMsg:
		m.loadingLayerPath = ""
		if msg.err != nil {
			if m.selectedOCINodePath() != msg.path {
				m.message = "Failed to open " + msg.path + ": " + msg.err.Error()
				return m, nil
			}
			p := preview.New(msg.path, []byte("Failed to open layer: "+msg.err.Error()), false)
			m.preview = &p
			m.message = "Failed to open " + msg.path + ": " + msg.err.Error()
			return m, nil
		}
		m.layerCache[msg.path] = msg.layer
		if m.selectedBlobPath() != msg.path {
			m.message = "Opened " + msg.path
			return m, nil
		}
		m.currentLayer = msg.layer
		m.layerExpanded = map[string]bool{"/": true}
		m.rebuildLayerRows()
		m.selectLayer(0)
		m.message = "Opened " + msg.path
		return m, nil
	case tea.KeyMsg:
		key := msg.String()
		m.overlayMessage = ""
		if m.searchMode {
			return m.updateSearch(key), nil
		}
		switch key {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			if m.zoomed {
				m.zoomed = false
				m.overlayMessage = ""
				m.message = "preview zoom disabled"
				break
			}
			return m, tea.Quit
		case "?":
			m.message = "Keys: Tab/Shift+Tab focus, v switch OCI/graph view, Ctrl+Space expand/collapse graph, Space toggle/page, f follow symlink or page preview, z zoom, q exits zoom/quits, Enter open, h/l collapse/expand or horizontal scroll, / search, p pretty, w wrap, # lines, e export"
		case "tab":
			if m.zoomed {
				m.overlayMessage = "Press z again to exit zoom state."
				break
			}
			m.nextFocus()
		case "shift+tab":
			if m.zoomed {
				m.overlayMessage = "Press z again to exit zoom state."
				break
			}
			m.previousFocus()
		case "/":
			m.searchMode = true
			m.searchTarget = m.focus
			m.searchQuery = ""
			m.message = "/"
		case "n":
			m.nextMatch(1)
		case "N":
			m.nextMatch(-1)
		case "p":
			m.togglePretty()
		case "w":
			m.toggleWrap()
		case "#":
			m.toggleLineNumbers()
		case "z":
			m.toggleZoom()
		case "v":
			m.toggleLeftView()
		case "e":
			m.exportSelected()
		case "enter":
			m.openOrExpand()
		case "right", "l":
			if m.focus == focusPreview || m.focus == focusInnerPreview {
				m.scrollPreviewHoriz(8)
			} else {
				m.openOrExpand()
			}
		case "left", "h":
			if m.focus == focusPreview || m.focus == focusInnerPreview {
				m.scrollPreviewHoriz(-8)
			} else {
				m.collapse()
			}
		case "j", "down":
			m.move(1)
		case "k", "up":
			m.move(-1)
		case "g":
			m.goTop()
		case "G":
			m.goBottom()
		case "0":
			m.goLineStart()
		case "$":
			m.goLineEnd()
		case " ":
			if m.focus == focusOCI || m.focus == focusLayer {
				m.toggleExpandCollapse()
			} else {
				m.scrollPreview(m.previewHeight())
			}
		case "ctrl+@", "ctrl+space":
			m.toggleAllGraphNodes()
		case "f":
			if m.focus == focusLayer {
				m.followLayerLink()
			} else {
				m.scrollPreview(m.previewHeight())
			}
		case "pgdown":
			m.scrollPreview(m.previewHeight())
		case "pgup", "b":
			m.scrollPreview(-m.previewHeight())
		case "ctrl+d":
			m.scrollPreview(m.previewHeight() / 2)
		case "ctrl+u":
			m.scrollPreview(-m.previewHeight() / 2)
		}
	}
	cmd := m.pending
	m.pending = nil
	return m, cmd
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	bodyH := max(1, m.height-4)
	header := fixedLine("OCI-Layout Archive Visualizer (olav) "+m.layout.InputPath, m.width)
	message := m.message
	if m.searchMode {
		message = "/" + m.searchQuery
	}
	messageLine := mutedStyle.Render(fixedLine(message, m.width))
	helpLine := mutedStyle.Render(fixedLine(helpText, m.width))

	leftW := max(24, m.width/3)
	var body string
	if m.zoomed {
		body = m.renderZoomed(bodyH)
	} else if m.innerPreview != nil && m.currentLayer != nil {
		midW := max(28, (m.width-leftW)/2)
		rightW := max(20, m.width-leftW-midW)
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.renderOCI(leftW, bodyH), m.renderLayer(midW, bodyH), m.renderInnerPreview(rightW, bodyH))
	} else {
		rightW := max(24, m.width-leftW)
		right := m.renderPreview(rightW, bodyH)
		if m.currentLayer != nil {
			right = m.renderLayer(rightW, bodyH)
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.renderOCI(leftW, bodyH), right)
	}
	if m.loadingLayerPath != "" {
		body = m.renderOverlay(body, bodyH, []string{"Extracting tarball.", "This can take a while for large tarballs."})
	} else if m.overlayMessage != "" {
		body = m.renderOverlay(body, bodyH, []string{m.overlayMessage})
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, messageLine, helpLine)
}

func (m *Model) updateSearch(key string) Model {
	switch key {
	case "esc":
		m.searchMode = false
		m.message = "search cancelled"
	case "enter":
		m.searchMode = false
		m.applySearch()
	case "backspace":
		if len(m.searchQuery) > 0 {
			m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
		}
	default:
		if len(key) == 1 {
			m.searchQuery += key
		}
	}
	return *m
}

func (m *Model) applySearch() {
	q := strings.ToLower(m.searchQuery)
	if q == "" {
		return
	}
	switch m.searchTarget {
	case focusOCI:
		if m.leftView == leftViewGraph {
			for i, row := range m.graphRows {
				if strings.Contains(strings.ToLower(graphSearchText(row.node)), q) {
					m.selectGraph(i)
					m.message = "matched " + row.node.Label
					return
				}
			}
			m.message = "no graph match for " + m.searchQuery
			return
		}
		for i, row := range m.ociRows {
			if strings.Contains(strings.ToLower(oci.DisplayName(row.node)), q) || strings.Contains(strings.ToLower(row.node.Path), q) {
				m.selectOCI(i)
				m.message = "matched " + row.node.Path
				return
			}
		}
		m.message = "no OCI file match for " + m.searchQuery
	case focusLayer:
		for i, row := range m.layerRows {
			if strings.Contains(strings.ToLower(row.entry.Path), q) {
				m.selectLayer(i)
				m.message = "matched " + row.entry.Path
				return
			}
		}
		m.message = "no layer file match for " + m.searchQuery
	case focusPreview:
		if m.preview != nil {
			m.preview.SetSearch(m.searchQuery)
			m.message = fmt.Sprintf("%d preview matches", len(m.preview.SearchMatches))
		}
	case focusInnerPreview:
		if m.innerPreview != nil {
			m.innerPreview.SetSearch(m.searchQuery)
			m.message = fmt.Sprintf("%d inner preview matches", len(m.innerPreview.SearchMatches))
		}
	}
}

func graphSearchText(n *oci.GraphNode) string {
	if n == nil {
		return ""
	}
	parts := []string{n.Label, n.Digest, n.MediaType, n.Platform, n.BlobPath, n.Kind.String()}
	parts = append(parts, n.Summary...)
	for k, v := range n.Annotations {
		parts = append(parts, k, v)
	}
	return strings.Join(parts, " ")
}

func (m *Model) rebuildOCIRows() {
	m.ociRows = nil
	var walk func(*oci.Node, int)
	walk = func(n *oci.Node, depth int) {
		m.ociRows = append(m.ociRows, treeRow{node: n, depth: depth})
		if n.IsDir && m.ociExpanded[n.Path] {
			for _, child := range n.Children {
				walk(child, depth+1)
			}
		}
	}
	walk(m.layout.Root, 0)
}

func (m *Model) rebuildGraphRows() {
	m.graphRows = nil
	var walk func(*oci.GraphNode, int)
	walk = func(n *oci.GraphNode, depth int) {
		if n == nil {
			return
		}
		m.graphRows = append(m.graphRows, graphRow{node: n, depth: depth})
		if m.graphExpanded[m.graphKey(n)] {
			for _, child := range n.Children {
				walk(child, depth+1)
			}
		}
	}
	walk(m.layout.GraphRoot, 0)
}

func (m *Model) graphKey(n *oci.GraphNode) string {
	if n == nil {
		return ""
	}
	if n.Digest != "" {
		return n.Kind.String() + ":" + n.Digest + ":" + n.Label
	}
	return n.Kind.String() + ":" + n.Label
}

func (m *Model) toggleLeftView() {
	if m.focus != focusOCI || m.zoomed {
		return
	}
	if m.leftView == leftViewOCI {
		m.leftView = leftViewGraph
		m.rebuildGraphRows()
		m.selectGraph(m.selectedGraph)
		m.message = "left pane: image graph"
		return
	}
	m.leftView = leftViewOCI
	m.selectOCI(m.selectedOCI)
	m.message = "left pane: OCI layout"
}

func (m *Model) expandAllGraphNodes() {
	var walk func(*oci.GraphNode)
	walk = func(n *oci.GraphNode) {
		if n == nil {
			return
		}
		if len(n.Children) > 0 {
			m.graphExpanded[m.graphKey(n)] = true
			for _, child := range n.Children {
				walk(child)
			}
		}
	}
	walk(m.layout.GraphRoot)
}

func (m *Model) collapseAllGraphNodes() {
	m.graphExpanded = map[string]bool{}
	if m.layout.GraphRoot != nil {
		m.graphExpanded[m.graphKey(m.layout.GraphRoot)] = true
	}
}

func (m *Model) allGraphNodesExpanded() bool {
	all := true
	var walk func(*oci.GraphNode)
	walk = func(n *oci.GraphNode) {
		if n == nil || !all {
			return
		}
		if len(n.Children) > 0 {
			if !m.graphExpanded[m.graphKey(n)] {
				all = false
				return
			}
			for _, child := range n.Children {
				walk(child)
			}
		}
	}
	walk(m.layout.GraphRoot)
	return all
}

func (m *Model) toggleAllGraphNodes() {
	if m.focus != focusOCI || m.leftView != leftViewGraph || m.zoomed {
		m.message = "Ctrl+Space toggles graph expansion in image graph view"
		return
	}
	selected := (*oci.GraphNode)(nil)
	if len(m.graphRows) > 0 && m.selectedGraph >= 0 && m.selectedGraph < len(m.graphRows) {
		selected = m.graphRows[m.selectedGraph].node
	}
	if m.allGraphNodesExpanded() {
		m.collapseAllGraphNodes()
		m.message = "collapsed graph nodes"
	} else {
		m.expandAllGraphNodes()
		m.message = "expanded all graph nodes"
	}
	m.rebuildGraphRows()
	m.selectGraphVisibleOrAncestor(selected)
}

func (m *Model) selectGraphVisibleOrAncestor(target *oci.GraphNode) {
	if target == nil {
		m.selectGraph(0)
		return
	}
	if idx := m.indexOfGraph(target); idx >= 0 {
		m.selectGraph(idx)
		return
	}
	m.selectGraph(0)
}

func (m *Model) rebuildLayerRows() {
	m.layerRows = nil
	if m.currentLayer == nil {
		return
	}
	var walk func(*layer.Entry, int)
	walk = func(e *layer.Entry, depth int) {
		m.layerRows = append(m.layerRows, layerRow{entry: e, depth: depth})
		if e.IsDir() && m.layerExpanded[e.Path] {
			for _, child := range e.Children {
				walk(child, depth+1)
			}
		}
	}
	walk(m.currentLayer.Root, 0)
}

func (m *Model) selectOCI(i int) {
	if len(m.ociRows) == 0 {
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= len(m.ociRows) {
		i = len(m.ociRows) - 1
	}
	m.selectedOCI = i
	node := m.ociRows[i].node
	m.selectOCINode(node, false)
}

func (m *Model) selectGraph(i int) {
	if len(m.graphRows) == 0 {
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= len(m.graphRows) {
		i = len(m.graphRows) - 1
	}
	m.selectedGraph = i
	n := m.graphRows[i].node
	if n == nil {
		return
	}
	if n.BlobPath != "" {
		m.selectedOCI = m.indexOfOCI(n.BlobPath)
		m.selectOCINode(m.layout.Files[n.BlobPath], false)
		return
	}
	text := strings.Join(n.Summary, "\n")
	if text == "" {
		text = n.Label
	}
	p := preview.New(n.Label, []byte(text), false)
	m.currentLayer = nil
	m.innerPreview = nil
	m.preview = &p
}

func (m *Model) selectOCINode(node *oci.Node, explicit bool) {
	m.currentLayer = nil
	m.innerPreview = nil
	if node == nil || node.IsDir {
		m.preview = nil
		return
	}
	if isLayerNode(node) {
		m.selectLayerBlob(node, explicit)
		return
	}
	if !explicit && m.shouldDeferBlobPreview(node) {
		p := preview.New(node.Path, []byte(m.largeBlobPlaceholder(node)), false)
		m.preview = &p
		return
	}
	prettyDefault := strings.HasSuffix(strings.ToLower(node.Name), ".json") || strings.Contains(strings.ToLower(blobMediaType(node)), "json")
	p := preview.New(node.Path, node.Data, prettyDefault)
	m.preview = &p
}

func (m *Model) shouldDeferBlobPreview(node *oci.Node) bool {
	return node != nil && node.Blob != nil && node.Size > m.maxAutoPreviewBytes
}

func (m *Model) largeBlobPlaceholder(node *oci.Node) string {
	var b strings.Builder
	b.WriteString("Preview is not opened automatically for large blobs.\n\n")
	fmt.Fprintf(&b, "Blob size: %d bytes\n", node.Size)
	fmt.Fprintf(&b, "Auto preview limit: %d bytes\n", m.maxAutoPreviewBytes)
	if node.Blob != nil && node.Blob.MediaType != "" {
		fmt.Fprintf(&b, "Media type: %s\n", node.Blob.MediaType)
	}
	b.WriteString("\nPress Enter, l, or Right to preview this file.\n")
	b.WriteString("Set MAX_AUTO_TEXT_PREVIEW_BYTES to change this behavior.\n")
	return b.String()
}

func (m *Model) selectLayerBlob(node *oci.Node, explicit bool) {
	if cached, ok := m.layerCache[node.Path]; ok {
		m.currentLayer = cached
		m.message = "Opened cached layer " + node.Path
		m.layerExpanded = map[string]bool{"/": true}
		m.rebuildLayerRows()
		m.selectLayer(0)
		return
	}
	if explicit || m.layerBlobCount <= m.autoExtractLimit {
		m.openLayer(node)
		return
	}
	m.currentLayer = nil
	m.innerPreview = nil
	m.layerRows = nil
	text := m.layerOpenPlaceholder(node)
	p := preview.New(node.Path, []byte(text), false)
	m.preview = &p
}

func (m *Model) layerOpenPlaceholder(node *oci.Node) string {
	var b strings.Builder
	b.WriteString("Layer tarball preview is not opened automatically.\n\n")
	fmt.Fprintf(&b, "This OCI layout has %d layer tarballs.\n", m.layerBlobCount)
	fmt.Fprintf(&b, "Auto extraction limit: %d.\n", m.autoExtractLimit)
	if node.Blob != nil && node.Blob.MediaType != "" {
		fmt.Fprintf(&b, "Media type: %s\n", node.Blob.MediaType)
	}
	fmt.Fprintf(&b, "Size: %d bytes\n\n", node.Size)
	b.WriteString("Press Enter, l, or Right to open this layer.\n")
	b.WriteString("Set MAX_NUM_AUTO_TARBALL_EXTRACTION to change this behavior.\n")
	return b.String()
}

func (m *Model) openLayer(node *oci.Node) {
	if cached, ok := m.layerCache[node.Path]; ok {
		m.currentLayer = cached
		m.message = "Opened cached layer " + node.Path
	} else {
		m.currentLayer = nil
		m.preview = nil
		m.innerPreview = nil
		m.layerRows = nil
		m.loadingLayerPath = node.Path
		m.message = "Opening " + node.Path
		m.pending = loadLayerCmd(node.Path, node.Blob.MediaType, node.Data)
		return
	}
	m.layerExpanded = map[string]bool{"/": true}
	m.rebuildLayerRows()
	m.selectLayer(0)
}

func loadLayerCmd(path, mediaType string, data []byte) tea.Cmd {
	return func() tea.Msg {
		lt, err := layer.Open(path, mediaType, data)
		return layerLoadedMsg{path: path, layer: lt, err: err}
	}
}

func (m *Model) selectedOCINodePath() string {
	if len(m.ociRows) == 0 || m.selectedOCI < 0 || m.selectedOCI >= len(m.ociRows) {
		return ""
	}
	return m.ociRows[m.selectedOCI].node.Path
}

func (m *Model) selectedBlobPath() string {
	if m.leftView == leftViewGraph {
		if len(m.graphRows) > 0 && m.selectedGraph >= 0 && m.selectedGraph < len(m.graphRows) {
			return m.graphRows[m.selectedGraph].node.BlobPath
		}
		return ""
	}
	return m.selectedOCINodePath()
}

func blobMediaType(n *oci.Node) string {
	if n != nil && n.Blob != nil {
		return n.Blob.MediaType
	}
	return ""
}

func (m *Model) selectLayer(i int) {
	if len(m.layerRows) == 0 {
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= len(m.layerRows) {
		i = len(m.layerRows) - 1
	}
	m.selectedLayerRow = i
	e := m.layerRows[i].entry
	m.innerPreview = nil
	if e.IsChiselManifest() {
		m.selectChiselManifest(e)
		return
	}
	if e.IsText() {
		p := preview.New(e.Path, e.Data, false)
		m.innerPreview = &p
		return
	}
	if e.IsLink() {
		m.selectLinkPreview(e)
	}
}

func (m *Model) selectLinkPreview(e *layer.Entry) {
	if m.currentLayer == nil {
		return
	}
	target, targetPath, err := m.currentLayer.ResolveLink(e)
	if err != nil {
		return
	}
	if target.IsChiselManifest() {
		m.selectChiselManifestWithTitle(target, e.Path+" -> "+targetPath)
		return
	}
	if target.IsText() {
		p := preview.New(e.Path+" -> "+targetPath, target.Data, false)
		m.innerPreview = &p
	}
}

func (m *Model) selectChiselManifest(e *layer.Entry) {
	m.selectChiselManifestWithTitle(e, e.Path)
}

func (m *Model) selectChiselManifestWithTitle(e *layer.Entry, title string) {
	if m.currentLayer == nil || e == nil {
		return
	}
	key := m.currentLayer.Title + ":" + title + ":" + e.Path
	if cached := m.chiselPreviewCache[key]; cached != nil {
		m.innerPreview = cached
		return
	}
	p, err := preview.NewChiselManifest(title, e.Data)
	if err != nil {
		m.message = "Failed to decompress chisel manifest: " + err.Error()
		return
	}
	m.chiselPreviewCache[key] = &p
	m.innerPreview = &p
}

func (m *Model) followLayerLink() {
	if m.currentLayer == nil || len(m.layerRows) == 0 {
		return
	}
	entry := m.layerRows[m.selectedLayerRow].entry
	if !entry.IsLink() {
		m.message = "selected layer item is not a link"
		return
	}
	target, targetPath, err := m.currentLayer.ResolveLink(entry)
	if err != nil {
		if targetPath == "" {
			targetPath = m.currentLayer.ResolveLinkPath(entry)
		}
		m.overlayMessage = "Link target does not exist: " + targetPath
		m.message = err.Error()
		return
	}
	m.expandLayerParents(target.Path)
	m.rebuildLayerRows()
	m.selectLayer(m.indexOfLayer(target.Path))
	m.message = "followed link to " + targetPath
}

func (m *Model) expandLayerParents(p string) {
	for dir := path.Dir(p); dir != "." && dir != "/"; dir = path.Dir(dir) {
		m.layerExpanded[dir] = true
	}
	m.layerExpanded["/"] = true
}

func (m *Model) nextFocus() {
	visible := m.visibleFocuses()
	m.moveFocus(visible, 1)
}

func (m *Model) previousFocus() {
	visible := m.visibleFocuses()
	m.moveFocus(visible, -1)
}

func (m *Model) visibleFocuses() []focus {
	visible := []focus{focusOCI}
	if m.currentLayer != nil {
		visible = append(visible, focusLayer)
		if m.innerPreview != nil {
			visible = append(visible, focusInnerPreview)
		}
	} else {
		visible = append(visible, focusPreview)
	}
	return visible
}

func (m *Model) moveFocus(visible []focus, delta int) {
	if len(visible) == 0 {
		return
	}
	idx := 0
	for i, f := range visible {
		if f == m.focus {
			idx = i
			break
		}
	}
	m.focus = visible[(idx+delta+len(visible))%len(visible)]
}

func (m *Model) openOrExpand() {
	switch m.focus {
	case focusOCI:
		if m.leftView == leftViewGraph {
			m.openOrExpandGraph()
			return
		}
		n := m.ociRows[m.selectedOCI].node
		if n.IsDir {
			m.ociExpanded[n.Path] = true
			m.rebuildOCIRows()
		} else {
			m.selectOCINode(n, true)
		}
	case focusLayer:
		e := m.layerRows[m.selectedLayerRow].entry
		if e.IsDir() {
			m.layerExpanded[e.Path] = true
			m.rebuildLayerRows()
		}
	}
}

func (m *Model) openOrExpandGraph() {
	if len(m.graphRows) == 0 {
		return
	}
	n := m.graphRows[m.selectedGraph].node
	if n == nil {
		return
	}
	if len(n.Children) > 0 {
		m.graphExpanded[m.graphKey(n)] = true
		m.rebuildGraphRows()
		m.selectGraphVisibleOrAncestor(n)
		return
	}
	if n.BlobPath != "" {
		m.selectOCINode(m.layout.Files[n.BlobPath], true)
	}
}

func (m *Model) collapse() {
	switch m.focus {
	case focusOCI:
		if m.leftView == leftViewGraph {
			m.collapseGraph()
			return
		}
		n := m.ociRows[m.selectedOCI].node
		if n.IsDir && m.ociExpanded[n.Path] {
			m.ociExpanded[n.Path] = false
			m.rebuildOCIRows()
		} else if n.Parent != nil {
			m.selectOCI(m.indexOfOCI(n.Parent.Path))
		}
	case focusLayer:
		e := m.layerRows[m.selectedLayerRow].entry
		if e.IsDir() && m.layerExpanded[e.Path] {
			m.layerExpanded[e.Path] = false
			m.rebuildLayerRows()
		} else if e.Parent != nil {
			m.selectLayer(m.indexOfLayer(e.Parent.Path))
		}
	}
}

func (m *Model) collapseGraph() {
	if len(m.graphRows) == 0 {
		return
	}
	n := m.graphRows[m.selectedGraph].node
	if n == nil {
		return
	}
	key := m.graphKey(n)
	if len(n.Children) > 0 && m.graphExpanded[key] {
		m.graphExpanded[key] = false
		m.rebuildGraphRows()
		m.selectGraphVisibleOrAncestor(n)
		return
	}
	for i := m.selectedGraph - 1; i >= 0; i-- {
		if m.graphRows[i].depth < m.graphRows[m.selectedGraph].depth {
			m.selectGraph(i)
			return
		}
	}
}

func (m *Model) toggleExpandCollapse() {
	switch m.focus {
	case focusOCI:
		if m.leftView == leftViewGraph {
			m.toggleGraphExpandCollapse()
			return
		}
		if len(m.ociRows) == 0 {
			return
		}
		n := m.ociRows[m.selectedOCI].node
		if !n.IsDir {
			m.message = "selected OCI item is not a folder"
			return
		}
		m.ociExpanded[n.Path] = !m.ociExpanded[n.Path]
		m.message = toggleMessage(n.Path, m.ociExpanded[n.Path])
		m.rebuildOCIRows()
		m.selectedOCI = m.indexOfOCI(n.Path)
	case focusLayer:
		if len(m.layerRows) == 0 {
			return
		}
		e := m.layerRows[m.selectedLayerRow].entry
		if !e.IsDir() {
			m.message = "selected layer item is not a folder"
			return
		}
		m.layerExpanded[e.Path] = !m.layerExpanded[e.Path]
		m.message = toggleMessage(e.Path, m.layerExpanded[e.Path])
		m.rebuildLayerRows()
		m.selectLayer(m.indexOfLayer(e.Path))
	}
}

func (m *Model) toggleGraphExpandCollapse() {
	if len(m.graphRows) == 0 {
		return
	}
	n := m.graphRows[m.selectedGraph].node
	if n == nil || len(n.Children) == 0 {
		m.message = "selected graph item has no children"
		return
	}
	key := m.graphKey(n)
	m.graphExpanded[key] = !m.graphExpanded[key]
	m.rebuildGraphRows()
	m.selectGraphVisibleOrAncestor(n)
}

func toggleMessage(path string, expanded bool) string {
	if expanded {
		return "expanded " + path
	}
	return "collapsed " + path
}

func (m *Model) move(delta int) {
	switch m.focus {
	case focusOCI:
		if m.leftView == leftViewGraph {
			m.selectGraph(m.selectedGraph + delta)
			return
		}
		m.selectOCI(m.selectedOCI + delta)
	case focusLayer:
		m.selectLayer(m.selectedLayerRow + delta)
	case focusPreview, focusInnerPreview:
		m.scrollPreview(delta)
	}
}

func (m *Model) goTop() {
	switch m.focus {
	case focusOCI:
		if m.leftView == leftViewGraph {
			m.selectGraph(0)
			return
		}
		m.selectOCI(0)
	case focusLayer:
		m.selectLayer(0)
	case focusPreview:
		if m.preview != nil {
			m.preview.Scroll = 0
		}
	case focusInnerPreview:
		if m.innerPreview != nil {
			m.innerPreview.Scroll = 0
		}
	}
}

func (m *Model) goBottom() {
	switch m.focus {
	case focusOCI:
		if m.leftView == leftViewGraph {
			m.selectGraph(len(m.graphRows) - 1)
			return
		}
		m.selectOCI(len(m.ociRows) - 1)
	case focusLayer:
		m.selectLayer(len(m.layerRows) - 1)
	case focusPreview:
		if m.preview != nil {
			m.preview.ScrollBy(1<<30, m.previewHeight(), m.previewContentWidth())
		}
	case focusInnerPreview:
		if m.innerPreview != nil {
			m.innerPreview.ScrollBy(1<<30, m.previewHeight(), m.previewContentWidth())
		}
	}
}

func (m *Model) scrollPreview(delta int) {
	if delta == 0 {
		delta = 1
	}
	if m.focus == focusInnerPreview && m.innerPreview != nil {
		m.innerPreview.ScrollBy(delta, m.previewHeight(), m.previewContentWidth())
	} else if m.preview != nil {
		m.preview.ScrollBy(delta, m.previewHeight(), m.previewContentWidth())
	}
}

func (m *Model) scrollPreviewHoriz(delta int) {
	width := m.previewContentWidth()
	if m.focus == focusInnerPreview && m.innerPreview != nil {
		m.innerPreview.ScrollHoriz(delta, width)
		m.message = fmt.Sprintf("column %d", m.innerPreview.HScroll+1)
	} else if m.preview != nil {
		m.preview.ScrollHoriz(delta, width)
		m.message = fmt.Sprintf("column %d", m.preview.HScroll+1)
	}
}

func (m *Model) goLineStart() {
	width := m.previewContentWidth()
	if m.focus == focusInnerPreview && m.innerPreview != nil {
		m.innerPreview.SetHScroll(0, width)
		m.message = "column 1"
	} else if m.focus == focusPreview && m.preview != nil {
		m.preview.SetHScroll(0, width)
		m.message = "column 1"
	}
}

func (m *Model) goLineEnd() {
	width := m.previewContentWidth()
	if m.focus == focusInnerPreview && m.innerPreview != nil {
		m.innerPreview.SetHScroll(1<<30, width)
		m.message = fmt.Sprintf("column %d", m.innerPreview.HScroll+1)
	} else if m.focus == focusPreview && m.preview != nil {
		m.preview.SetHScroll(1<<30, width)
		m.message = fmt.Sprintf("column %d", m.preview.HScroll+1)
	}
}

func (m *Model) nextMatch(delta int) {
	if m.focus == focusInnerPreview && m.innerPreview != nil {
		m.innerPreview.NextMatch(delta)
	} else if m.preview != nil {
		m.preview.NextMatch(delta)
	}
}

func (m *Model) togglePretty() {
	if m.focus == focusInnerPreview && m.innerPreview != nil {
		m.message = m.innerPreview.TogglePretty()
		return
	}
	if m.preview != nil {
		m.message = m.preview.TogglePretty()
	}
}

func (m *Model) toggleWrap() {
	if m.focus == focusInnerPreview && m.innerPreview != nil {
		m.message = m.innerPreview.ToggleWrap(m.previewHeight(), m.previewContentWidth())
		return
	}
	if m.preview != nil {
		m.message = m.preview.ToggleWrap(m.previewHeight(), m.previewContentWidth())
	}
}

func (m *Model) toggleLineNumbers() {
	if m.focus == focusInnerPreview && m.innerPreview != nil {
		m.message = m.innerPreview.ToggleLineNumbers()
		return
	}
	if m.preview != nil {
		m.message = m.preview.ToggleLineNumbers()
	}
}

func (m *Model) toggleZoom() {
	if m.zoomed {
		m.zoomed = false
		m.overlayMessage = ""
		m.message = "preview zoom disabled"
		return
	}
	if m.focus == focusPreview && m.preview != nil {
		m.zoomed = true
		m.zoomTarget = focusPreview
		m.overlayMessage = ""
		m.message = "preview zoom enabled; press z or q to exit zoom"
		return
	}
	if m.focus == focusInnerPreview && m.innerPreview != nil {
		m.zoomed = true
		m.zoomTarget = focusInnerPreview
		m.overlayMessage = ""
		m.message = "preview zoom enabled; press z or q to exit zoom"
		return
	}
	m.message = "focus a text preview to zoom"
}

func (m *Model) exportSelected() {
	if m.focus == focusLayer || m.focus == focusInnerPreview {
		if len(m.layerRows) == 0 || m.currentLayer == nil {
			return
		}
		dest, err := export.LayerEntry(m.currentLayer.Title, m.layerRows[m.selectedLayerRow].entry)
		if err != nil {
			m.message = err.Error()
			return
		}
		m.message = "exported to " + dest
		return
	}
	if len(m.ociRows) == 0 {
		return
	}
	dest, err := export.Node(m.ociRows[m.selectedOCI].node)
	if err != nil {
		m.message = err.Error()
		return
	}
	m.message = "exported to " + dest
}

func (m *Model) renderOCI(width, height int) string {
	if m.leftView == leftViewGraph {
		return m.renderGraph(width, height)
	}
	contentW := contentWidth(width)
	contentH := contentHeight(height)
	lines := []string{headerStyle.Render("OCI Files")}
	for i, row := range visibleTreeRows(m.ociRows, m.selectedOCI, contentH-1) {
		prefix := strings.Repeat("  ", row.depth)
		marker := "  "
		if row.node.IsDir {
			marker = "▸ "
			if m.ociExpanded[row.node.Path] {
				marker = "▾ "
			}
		}
		line := prefix + marker + oci.DisplayName(row.node)
		actual := m.indexOfOCI(row.node.Path)
		if actual == m.selectedOCI {
			line = selectStyle.Render(line)
		} else if i == 0 {
			_ = i
		}
		lines = append(lines, fixedLine(line, contentW))
	}
	return pane(m.focus == focusOCI, width, height).Render(fixedBlock(lines, contentW, contentH))
}

func (m *Model) renderGraph(width, height int) string {
	contentW := contentWidth(width)
	contentH := contentHeight(height)
	lines := []string{headerStyle.Render("Image Graph")}
	for _, row := range visibleGraphRows(m.graphRows, m.selectedGraph, contentH-1) {
		prefix := strings.Repeat("  ", row.depth)
		marker := "  "
		if len(row.node.Children) > 0 {
			marker = "▸ "
			if m.graphExpanded[m.graphKey(row.node)] {
				marker = "▾ "
			}
		}
		line := prefix + marker + row.node.Label
		if m.indexOfGraph(row.node) == m.selectedGraph {
			line = selectStyle.Render(line)
		}
		lines = append(lines, fixedLine(line, contentW))
	}
	return pane(m.focus == focusOCI, width, height).Render(fixedBlock(lines, contentW, contentH))
}

func (m *Model) renderPreview(width, height int) string {
	contentW := contentWidth(width)
	contentH := contentHeight(height)
	if m.preview == nil {
		return pane(m.focus == focusPreview, width, height).Render(fixedBlock([]string{headerStyle.Render("Preview"), "", "Select a file to preview"}, contentW, contentH))
	}
	lines := []string{headerStyle.Render("Preview: " + m.preview.Title)}
	if m.preview.Notice != "" {
		lines = append(lines, mutedStyle.Render(m.preview.Notice))
	}
	lines = append(lines, m.preview.Visible(max(1, contentH-len(lines)), contentW)...)
	return pane(m.focus == focusPreview, width, height).Render(fixedBlock(lines, contentW, contentH))
}

func (m *Model) renderLayer(width, height int) string {
	if m.currentLayer == nil {
		return m.renderPreview(width, height)
	}
	headerH := 4
	detailsH := max(7, height/3)
	filesH := max(5, height-headerH-detailsH)
	contentW := contentWidth(width)
	header := pane(false, width, headerH).Render(fixedBlock([]string{headerStyle.Render("Layer: " + m.currentLayer.Title), mutedStyle.Render(m.currentLayer.MediaType)}, contentW, contentHeight(headerH)))
	fileLines := []string{headerStyle.Render("Layer Files")}
	for _, row := range visibleLayerRows(m.layerRows, m.selectedLayerRow, contentHeight(filesH)-1) {
		prefix := strings.Repeat("  ", row.depth)
		marker := "  "
		if row.entry.IsDir() {
			marker = "▸ "
			if m.layerExpanded[row.entry.Path] {
				marker = "▾ "
			}
		}
		line := prefix + marker + layer.DisplayName(row.entry)
		if m.indexOfLayer(row.entry.Path) == m.selectedLayerRow {
			line = selectStyle.Render(line)
		}
		fileLines = append(fileLines, fixedLine(line, contentW))
	}
	files := pane(m.focus == focusLayer, width, filesH).Render(fixedBlock(fileLines, contentW, contentHeight(filesH)))
	detailLines := []string{"Entry Details"}
	if len(m.layerRows) > 0 {
		detailLines = m.layerRows[m.selectedLayerRow].entry.Details()
	}
	details := pane(false, width, detailsH).Render(fixedBlock(detailLines, contentW, contentHeight(detailsH)))
	return lipgloss.JoinVertical(lipgloss.Left, header, files, details)
}

func (m *Model) renderInnerPreview(width, height int) string {
	contentW := contentWidth(width)
	contentH := contentHeight(height)
	if m.innerPreview == nil {
		return pane(m.focus == focusInnerPreview, width, height).Render(fixedBlock([]string{"File Preview", "", "No text file selected"}, contentW, contentH))
	}
	lines := []string{headerStyle.Render("File Preview"), m.innerPreview.Title}
	if m.innerPreview.Notice != "" {
		lines = append(lines, mutedStyle.Render(m.innerPreview.Notice))
	} else {
		lines = append(lines, mutedStyle.Render("Raw text"))
	}
	lines = append(lines, m.innerPreview.Visible(max(1, contentH-len(lines)), contentW)...)
	return pane(m.focus == focusInnerPreview, width, height).Render(fixedBlock(lines, contentW, contentH))
}

func (m *Model) renderZoomed(height int) string {
	switch m.zoomTarget {
	case focusPreview:
		if m.preview != nil {
			return m.renderPreview(m.width, height)
		}
	case focusInnerPreview:
		if m.innerPreview != nil {
			return m.renderInnerPreview(m.width, height)
		}
	}
	leftW := max(24, m.width/3)
	rightW := max(24, m.width-leftW)
	return lipgloss.JoinHorizontal(lipgloss.Top, m.renderOCI(leftW, height), m.renderPreview(rightW, height))
}

func (m *Model) renderOverlay(body string, height int, overlayText []string) string {
	lines := strings.Split(body, "\n")
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", m.width))
	}
	if len(lines) > height {
		lines = lines[:height]
	}

	overlayW := min(max(48, m.width/2), max(24, m.width-4))
	contentW := max(1, overlayW-4)
	overlayH := max(3, len(overlayText)+2)
	overlayLines := make([]string, 0, len(overlayText))
	for _, text := range overlayText {
		overlayLines = append(overlayLines, centerLine(text, contentW))
	}
	overlay := pane(false, overlayW, overlayH).Render(fixedBlock(overlayLines, contentW, contentHeight(overlayH)))
	olines := strings.Split(overlay, "\n")
	startY := max(0, (height-len(olines))/2)
	startX := max(0, (m.width-overlayW)/2)

	for i, line := range olines {
		y := startY + i
		if y >= len(lines) {
			break
		}
		lines[y] = overlayLine(lines[y], line, startX, m.width)
	}
	return strings.Join(lines, "\n")
}

func overlayLine(base, overlay string, x, width int) string {
	plain := ansi.Truncate(base, width, "")
	if ansi.StringWidth(plain) < width {
		plain += strings.Repeat(" ", width-ansi.StringWidth(plain))
	}
	left := ansi.Truncate(plain, x, "")
	rightStart := x + ansi.StringWidth(overlay)
	right := ""
	if rightStart < width {
		right = ansi.TruncateLeft(plain, rightStart, "")
	}
	return ansi.Truncate(left+overlay+right, width, "")
}

func centerLine(s string, width int) string {
	if width <= ansi.StringWidth(s) {
		return ansi.Truncate(s, width, "")
	}
	left := (width - ansi.StringWidth(s)) / 2
	return strings.Repeat(" ", left) + s
}

func pane(active bool, width, height int) lipgloss.Style {
	style := unfocused
	if active {
		style = focused
	}
	return style.Width(outerContentWidth(width)).Height(contentHeight(height)).MaxWidth(width)
}

func contentWidth(width int) int {
	return max(1, width-4)
}

func outerContentWidth(width int) int {
	return max(1, width-2)
}

func contentHeight(height int) int {
	return max(1, height-2)
}

func fixedBlock(lines []string, width, height int) string {
	if height < 1 {
		return ""
	}
	out := make([]string, 0, height)
	for _, line := range lines {
		if len(out) == height {
			break
		}
		out = append(out, fixedLine(line, width))
	}
	for len(out) < height {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

func fixedLine(line string, width int) string {
	if width < 1 {
		return ""
	}
	return ansi.Truncate(line, width, "…")
}

func visibleTreeRows(rows []treeRow, selected, count int) []treeRow {
	if count < 1 || len(rows) <= count {
		return rows
	}
	start := selected - count/2
	if start < 0 {
		start = 0
	}
	if start+count > len(rows) {
		start = len(rows) - count
	}
	return rows[start : start+count]
}

func visibleLayerRows(rows []layerRow, selected, count int) []layerRow {
	if count < 1 || len(rows) <= count {
		return rows
	}
	start := selected - count/2
	if start < 0 {
		start = 0
	}
	if start+count > len(rows) {
		start = len(rows) - count
	}
	return rows[start : start+count]
}

func visibleGraphRows(rows []graphRow, selected, count int) []graphRow {
	if count < 1 || len(rows) <= count {
		return rows
	}
	start := selected - count/2
	if start < 0 {
		start = 0
	}
	if start+count > len(rows) {
		start = len(rows) - count
	}
	return rows[start : start+count]
}

func (m *Model) indexOfOCI(p string) int {
	for i, row := range m.ociRows {
		if row.node.Path == p {
			return i
		}
	}
	return 0
}

func (m *Model) indexOfLayer(p string) int {
	for i, row := range m.layerRows {
		if row.entry.Path == p {
			return i
		}
	}
	return 0
}

func (m *Model) indexOfGraph(target *oci.GraphNode) int {
	for i, row := range m.graphRows {
		if row.node == target {
			return i
		}
	}
	return -1
}

func (m *Model) previewHeight() int {
	return max(1, m.height-8)
}

func (m *Model) previewContentWidth() int {
	if m.zoomed {
		return contentWidth(m.width)
	}
	leftW := max(24, m.width/3)
	if m.focus == focusInnerPreview && m.innerPreview != nil && m.currentLayer != nil {
		midW := max(28, (m.width-leftW)/2)
		return contentWidth(max(20, m.width-leftW-midW))
	}
	return contentWidth(max(24, m.width-leftW))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
