package tui

import (
	"fmt"
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

	ociRows      []treeRow
	selectedOCI  int
	ociExpanded  map[string]bool
	searchMode   bool
	searchTarget focus
	searchQuery  string

	preview *preview.Preview

	layerCache         map[string]*layer.Layer
	currentLayer       *layer.Layer
	loadingLayerPath   string
	layerRows          []layerRow
	selectedLayerRow   int
	layerExpanded      map[string]bool
	innerPreview       *preview.Preview
	chiselPreviewCache map[string]*preview.Preview
}

type layerLoadedMsg struct {
	path  string
	layer *layer.Layer
	err   error
}

const helpText = "Tab focus | j/k move | Space toggle/page | z zoom | / search | p pretty | w wrap | # lines | e export | q quit"

type treeRow struct {
	node  *oci.Node
	depth int
}

type layerRow struct {
	entry *layer.Entry
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
		layout:             layout,
		ociExpanded:        map[string]bool{"/": true, "/blobs": true, "/blobs/sha256": true},
		layerCache:         map[string]*layer.Layer{},
		layerExpanded:      map[string]bool{"/": true},
		chiselPreviewCache: map[string]*preview.Preview{},
	}
	m.rebuildOCIRows()
	m.selectOCI(0)
	return m
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
		if m.selectedOCINodePath() != msg.path {
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
		if m.searchMode {
			return m.updateSearch(key), nil
		}
		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "?":
			m.message = "Keys: Tab focus, Space toggle/page, z zoom, Enter open, h/l collapse/expand or horizontal scroll, / search, p pretty, w wrap, # lines, e export"
		case "tab":
			if m.zoomed {
				m.overlayMessage = "Press z again to exit zoom state."
				break
			}
			m.nextFocus()
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
		case "pgdown", "f":
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
	m.currentLayer = nil
	m.innerPreview = nil
	if node.IsDir {
		m.preview = nil
		return
	}
	if node.Blob != nil && oci.IsLayerMediaType(node.Blob.MediaType) {
		m.openLayer(node)
		return
	}
	prettyDefault := strings.HasSuffix(strings.ToLower(node.Name), ".json") || strings.Contains(strings.ToLower(blobMediaType(node)), "json")
	p := preview.New(node.Path, node.Data, prettyDefault)
	m.preview = &p
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
	}
}

func (m *Model) selectChiselManifest(e *layer.Entry) {
	if m.currentLayer == nil || e == nil {
		return
	}
	key := m.currentLayer.Title + ":" + e.Path
	if cached := m.chiselPreviewCache[key]; cached != nil {
		m.innerPreview = cached
		return
	}
	p, err := preview.NewChiselManifest(e.Path, e.Data)
	if err != nil {
		m.message = "Failed to decompress chisel manifest: " + err.Error()
		return
	}
	m.chiselPreviewCache[key] = &p
	m.innerPreview = &p
}

func (m *Model) nextFocus() {
	visible := []focus{focusOCI}
	if m.currentLayer != nil {
		visible = append(visible, focusLayer)
		if m.innerPreview != nil {
			visible = append(visible, focusInnerPreview)
		}
	} else {
		visible = append(visible, focusPreview)
	}
	idx := 0
	for i, f := range visible {
		if f == m.focus {
			idx = i
			break
		}
	}
	m.focus = visible[(idx+1)%len(visible)]
}

func (m *Model) openOrExpand() {
	switch m.focus {
	case focusOCI:
		n := m.ociRows[m.selectedOCI].node
		if n.IsDir {
			m.ociExpanded[n.Path] = true
			m.rebuildOCIRows()
		} else {
			m.selectOCI(m.selectedOCI)
		}
	case focusLayer:
		e := m.layerRows[m.selectedLayerRow].entry
		if e.IsDir() {
			m.layerExpanded[e.Path] = true
			m.rebuildLayerRows()
		}
	}
}

func (m *Model) collapse() {
	switch m.focus {
	case focusOCI:
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

func (m *Model) toggleExpandCollapse() {
	switch m.focus {
	case focusOCI:
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

func toggleMessage(path string, expanded bool) string {
	if expanded {
		return "expanded " + path
	}
	return "collapsed " + path
}

func (m *Model) move(delta int) {
	switch m.focus {
	case focusOCI:
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
		m.message = "preview zoom enabled"
		return
	}
	if m.focus == focusInnerPreview && m.innerPreview != nil {
		m.zoomed = true
		m.zoomTarget = focusInnerPreview
		m.overlayMessage = ""
		m.message = "preview zoom enabled"
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
