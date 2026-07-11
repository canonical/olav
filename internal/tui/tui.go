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

	width  int
	height int
	focus  focus
	status string

	ociRows      []treeRow
	selectedOCI  int
	ociExpanded  map[string]bool
	searchMode   bool
	searchTarget focus
	searchQuery  string

	preview *preview.Preview

	layerCache       map[string]*layer.Layer
	currentLayer     *layer.Layer
	layerRows        []layerRow
	selectedLayerRow int
	layerExpanded    map[string]bool
	innerPreview     *preview.Preview
}

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
		layout:        layout,
		ociExpanded:   map[string]bool{"/": true, "/blobs": true, "/blobs/sha256": true},
		layerCache:    map[string]*layer.Layer{},
		layerExpanded: map[string]bool{"/": true},
		status:        "Tab focus | j/k move | / search | p pretty | e export | ? help | q quit",
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
	case tea.KeyMsg:
		key := msg.String()
		if m.searchMode {
			return m.updateSearch(key), nil
		}
		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "?":
			m.status = "Keys: Tab focus, Enter expand/open, j/k move, g/G top/bottom, / search, n/N matches, p pretty, e export, q quit"
		case "tab":
			m.nextFocus()
		case "/":
			m.searchMode = true
			m.searchTarget = m.focus
			m.searchQuery = ""
			m.status = "/"
		case "n":
			m.nextMatch(1)
		case "N":
			m.nextMatch(-1)
		case "p":
			m.togglePretty()
		case "e":
			m.exportSelected()
		case "enter", "right", "l":
			m.openOrExpand()
		case "left", "h":
			m.collapse()
		case "j", "down":
			m.move(1)
		case "k", "up":
			m.move(-1)
		case "g":
			m.goTop()
		case "G":
			m.goBottom()
		case "pgdown", " ", "f":
			m.scrollPreview(m.previewHeight())
		case "pgup", "b":
			m.scrollPreview(-m.previewHeight())
		case "ctrl+d":
			m.scrollPreview(m.previewHeight() / 2)
		case "ctrl+u":
			m.scrollPreview(-m.previewHeight() / 2)
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	bodyH := max(1, m.height-3)
	header := lipgloss.NewStyle().Width(m.width).Render("olav " + m.layout.InputPath)
	status := m.status
	if m.searchMode {
		status = "/" + m.searchQuery
	}
	footer := mutedStyle.Width(m.width).Render(status)

	leftW := max(24, m.width/3)
	if m.innerPreview != nil && m.currentLayer != nil {
		midW := max(28, (m.width-leftW)/2)
		rightW := max(20, m.width-leftW-midW)
		return lipgloss.JoinVertical(lipgloss.Left, header, lipgloss.JoinHorizontal(lipgloss.Top,
			m.renderOCI(leftW, bodyH), m.renderLayer(midW, bodyH), m.renderInnerPreview(rightW, bodyH)), footer)
	}
	rightW := max(24, m.width-leftW)
	right := m.renderPreview(rightW, bodyH)
	if m.currentLayer != nil {
		right = m.renderLayer(rightW, bodyH)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, lipgloss.JoinHorizontal(lipgloss.Top, m.renderOCI(leftW, bodyH), right), footer)
}

func (m *Model) updateSearch(key string) Model {
	switch key {
	case "esc":
		m.searchMode = false
		m.status = "search cancelled"
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
				m.status = "matched " + row.node.Path
				return
			}
		}
		m.status = "no OCI file match for " + m.searchQuery
	case focusLayer:
		for i, row := range m.layerRows {
			if strings.Contains(strings.ToLower(row.entry.Path), q) {
				m.selectLayer(i)
				m.status = "matched " + row.entry.Path
				return
			}
		}
		m.status = "no layer file match for " + m.searchQuery
	case focusPreview:
		if m.preview != nil {
			m.preview.SetSearch(m.searchQuery)
			m.status = fmt.Sprintf("%d preview matches", len(m.preview.SearchMatches))
		}
	case focusInnerPreview:
		if m.innerPreview != nil {
			m.innerPreview.SetSearch(m.searchQuery)
			m.status = fmt.Sprintf("%d inner preview matches", len(m.innerPreview.SearchMatches))
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
	} else {
		lt, err := layer.Open(node.Path, node.Blob.MediaType, node.Data)
		if err != nil {
			p := preview.New(node.Path, []byte("Failed to open layer: "+err.Error()), false)
			m.preview = &p
			m.status = err.Error()
			return
		}
		m.layerCache[node.Path] = lt
		m.currentLayer = lt
	}
	m.layerExpanded = map[string]bool{"/": true}
	m.rebuildLayerRows()
	m.selectLayer(0)
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
	if e.IsText() {
		p := preview.New(e.Path, e.Data, false)
		m.innerPreview = &p
	}
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
			m.preview.ScrollBy(len(m.preview.Lines), m.previewHeight())
		}
	case focusInnerPreview:
		if m.innerPreview != nil {
			m.innerPreview.ScrollBy(len(m.innerPreview.Lines), m.previewHeight())
		}
	}
}

func (m *Model) scrollPreview(delta int) {
	if delta == 0 {
		delta = 1
	}
	if m.focus == focusInnerPreview && m.innerPreview != nil {
		m.innerPreview.ScrollBy(delta, m.previewHeight())
	} else if m.preview != nil {
		m.preview.ScrollBy(delta, m.previewHeight())
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
		m.status = m.innerPreview.TogglePretty()
		return
	}
	if m.preview != nil {
		m.status = m.preview.TogglePretty()
	}
}

func (m *Model) exportSelected() {
	if m.focus == focusLayer || m.focus == focusInnerPreview {
		if len(m.layerRows) == 0 || m.currentLayer == nil {
			return
		}
		dest, err := export.LayerEntry(m.currentLayer.Title, m.layerRows[m.selectedLayerRow].entry)
		if err != nil {
			m.status = err.Error()
			return
		}
		m.status = "exported to " + dest
		return
	}
	if len(m.ociRows) == 0 {
		return
	}
	dest, err := export.Node(m.ociRows[m.selectedOCI].node)
	if err != nil {
		m.status = err.Error()
		return
	}
	m.status = "exported to " + dest
}

func (m *Model) renderOCI(width, height int) string {
	lines := []string{headerStyle.Render("OCI Files")}
	for i, row := range visibleTreeRows(m.ociRows, m.selectedOCI, height-2) {
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
		lines = append(lines, line)
	}
	return pane(m.focus == focusOCI, width, height).Render(strings.Join(lines, "\n"))
}

func (m *Model) renderPreview(width, height int) string {
	if m.preview == nil {
		return pane(m.focus == focusPreview, width, height).Render(headerStyle.Render("Preview") + "\n\nSelect a file to preview")
	}
	lines := []string{headerStyle.Render("Preview: " + m.preview.Title)}
	if m.preview.Notice != "" {
		lines = append(lines, mutedStyle.Render(m.preview.Notice))
	}
	lines = append(lines, m.preview.Visible(max(1, height-4))...)
	return pane(m.focus == focusPreview, width, height).Render(strings.Join(lines, "\n"))
}

func (m *Model) renderLayer(width, height int) string {
	if m.currentLayer == nil {
		return m.renderPreview(width, height)
	}
	headerH := 4
	detailsH := max(7, height/3)
	filesH := max(5, height-headerH-detailsH)
	header := pane(false, width, headerH).Render(headerStyle.Render("Layer: "+m.currentLayer.Title) + "\n" + mutedStyle.Render(m.currentLayer.MediaType))
	fileLines := []string{headerStyle.Render("Layer Files")}
	for _, row := range visibleLayerRows(m.layerRows, m.selectedLayerRow, filesH-2) {
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
		fileLines = append(fileLines, line)
	}
	files := pane(m.focus == focusLayer, width, filesH).Render(strings.Join(fileLines, "\n"))
	detailLines := []string{"Entry Details"}
	if len(m.layerRows) > 0 {
		detailLines = m.layerRows[m.selectedLayerRow].entry.Details()
	}
	details := pane(false, width, detailsH).Render(strings.Join(detailLines, "\n"))
	return lipgloss.JoinVertical(lipgloss.Left, header, files, details)
}

func (m *Model) renderInnerPreview(width, height int) string {
	if m.innerPreview == nil {
		return pane(m.focus == focusInnerPreview, width, height).Render("File Preview\n\nNo text file selected")
	}
	lines := []string{headerStyle.Render("File Preview"), m.innerPreview.Title}
	if m.innerPreview.Notice != "" {
		lines = append(lines, mutedStyle.Render(m.innerPreview.Notice))
	} else {
		lines = append(lines, mutedStyle.Render("Raw text"))
	}
	lines = append(lines, m.innerPreview.Visible(max(1, height-5))...)
	return pane(m.focus == focusInnerPreview, width, height).Render(strings.Join(lines, "\n"))
}

func pane(active bool, width, height int) lipgloss.Style {
	style := unfocused
	if active {
		style = focused
	}
	return style.Width(max(1, width-4)).Height(max(1, height-2))
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
