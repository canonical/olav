package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type Layout struct {
	InputPath string
	Root      *Node
	Files     map[string]*Node
	Blobs     map[string]*BlobInfo
	GraphRoot *GraphNode
}

type Node struct {
	Name     string
	Path     string
	Size     int64
	Mode     fs.FileMode
	IsDir    bool
	Data     []byte
	Blob     *BlobInfo
	Parent   *Node
	Children []*Node
}

type BlobInfo struct {
	Digest    string
	Algorithm string
	Encoded   string
	MediaType string
	Role      string
}

type GraphKind int

const (
	GraphRoot GraphKind = iota
	GraphIndex
	GraphPlatform
	GraphManifest
	GraphConfig
	GraphLayer
	GraphArtifact
	GraphBlob
	GraphSummary
)

type GraphNode struct {
	Label     string
	Kind      GraphKind
	Digest    string
	MediaType string
	Platform  string
	Size      int64
	BlobPath  string
	Summary   []string
	Children  []*GraphNode
}

func (k GraphKind) String() string {
	switch k {
	case GraphRoot:
		return "root"
	case GraphIndex:
		return "index"
	case GraphPlatform:
		return "platform"
	case GraphManifest:
		return "manifest"
	case GraphConfig:
		return "config"
	case GraphLayer:
		return "layer"
	case GraphArtifact:
		return "artifact"
	case GraphBlob:
		return "blob"
	case GraphSummary:
		return "summary"
	default:
		return "unknown"
	}
}

type descriptor struct {
	MediaType string            `json:"mediaType"`
	Digest    string            `json:"digest"`
	Size      int64             `json:"size"`
	Platform  map[string]string `json:"platform"`
}

type indexFile struct {
	SchemaVersion int          `json:"schemaVersion"`
	Manifests     []descriptor `json:"manifests"`
}

type manifestFile struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        descriptor   `json:"config"`
	Layers        []descriptor `json:"layers"`
	Subject       *descriptor  `json:"subject"`
	Blobs         []descriptor `json:"blobs"`
}

type imageIndexFile struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Manifests     []descriptor `json:"manifests"`
}

func Load(input string) (*Layout, error) {
	info, err := os.Stat(input)
	if err != nil {
		return nil, err
	}

	l := &Layout{
		InputPath: input,
		Root:      &Node{Name: "/", Path: "/", IsDir: true, Mode: fs.ModeDir | 0o755},
		Files:     map[string]*Node{},
		Blobs:     map[string]*BlobInfo{},
	}
	l.Files["/"] = l.Root

	if info.IsDir() {
		if err := loadDir(l, input); err != nil {
			return nil, err
		}
	} else {
		if err := loadTar(l, input); err != nil {
			return nil, err
		}
	}

	if _, ok := l.Files["/oci-layout"]; !ok {
		if _, docker := l.Files["/manifest.json"]; docker {
			return nil, errors.New("docker archive detected; convert it to OCI layout with skopeo first")
		}
		return nil, errors.New("not an OCI image layout: missing oci-layout")
	}
	if _, ok := l.Files["/index.json"]; !ok {
		return nil, errors.New("not an OCI image layout: missing index.json")
	}

	l.annotateBlobs()
	l.GraphRoot = l.buildGraph()
	sortTree(l.Root)
	return l, nil
}

func loadDir(l *Layout, root string) error {
	return filepath.WalkDir(root, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if file == root {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		p := cleanPath(filepath.ToSlash(rel))
		node := l.ensureNode(p, d.IsDir())
		node.Size = info.Size()
		node.Mode = info.Mode()
		if !d.IsDir() {
			data, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			node.Data = data
		}
		return nil
	})
}

func loadTar(l *Layout, file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(file, ".gz") || strings.HasSuffix(file, ".tgz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	}

	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if h.Name == "" {
			continue
		}
		p := cleanPath(h.Name)
		isDir := h.FileInfo().IsDir()
		node := l.ensureNode(p, isDir)
		node.Size = h.Size
		node.Mode = h.FileInfo().Mode()
		if !isDir && h.Typeflag == tar.TypeReg {
			data, err := io.ReadAll(tr)
			if err != nil {
				return err
			}
			node.Data = data
		}
	}
	return nil
}

func (l *Layout) ensureNode(p string, isDir bool) *Node {
	p = cleanPath(p)
	if node, ok := l.Files[p]; ok {
		if isDir {
			node.IsDir = true
		}
		return node
	}
	parentPath := path.Dir(p)
	if parentPath == "." {
		parentPath = "/"
	}
	parent := l.ensureNode(parentPath, true)
	node := &Node{Name: path.Base(p), Path: p, IsDir: isDir, Parent: parent}
	if isDir {
		node.Mode = fs.ModeDir | 0o755
	}
	parent.Children = append(parent.Children, node)
	l.Files[p] = node
	return node
}

func cleanPath(p string) string {
	p = path.Clean("/" + strings.TrimPrefix(p, "/"))
	if p == "." {
		return "/"
	}
	return p
}

func sortTree(n *Node) {
	sort.Slice(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return a.Name < b.Name
	})
	for _, child := range n.Children {
		sortTree(child)
	}
}

func (l *Layout) annotateBlobs() {
	for p, node := range l.Files {
		parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
		if len(parts) == 3 && parts[0] == "blobs" {
			blob := &BlobInfo{Algorithm: parts[1], Encoded: parts[2], Digest: parts[1] + ":" + parts[2], Role: "blob"}
			node.Blob = blob
			l.Blobs[blob.Digest] = blob
		}
	}

	idxNode := l.Files["/index.json"]
	if idxNode == nil {
		return
	}
	var idx indexFile
	if json.Unmarshal(idxNode.Data, &idx) != nil {
		return
	}
	visited := map[string]bool{}
	for _, manifest := range idx.Manifests {
		l.annotateDescriptor(manifest, "manifest", visited)
	}
}

func (l *Layout) annotateDescriptor(desc descriptor, fallbackRole string, visited map[string]bool) {
	if desc.Digest == "" || visited[desc.Digest] {
		return
	}
	visited[desc.Digest] = true
	l.mark(desc, roleForMediaType(desc.MediaType, fallbackRole))
	if IsIndexMediaType(desc.MediaType) {
		l.annotateIndex(desc.Digest, visited)
		return
	}
	if IsManifestMediaType(desc.MediaType) || desc.MediaType == "" {
		l.annotateManifest(desc.Digest, visited)
	}
}

func (l *Layout) annotateIndex(digest string, visited map[string]bool) {
	node := l.nodeByDigest(digest)
	if node == nil {
		return
	}
	var idx imageIndexFile
	if json.Unmarshal(node.Data, &idx) != nil {
		return
	}
	for _, manifest := range idx.Manifests {
		l.annotateDescriptor(manifest, "manifest", visited)
	}
}

func (l *Layout) annotateManifest(digest string, visited map[string]bool) {
	node := l.nodeByDigest(digest)
	if node == nil {
		return
	}
	var mf manifestFile
	if json.Unmarshal(node.Data, &mf) != nil {
		return
	}
	if mf.Config.Digest != "" {
		l.mark(mf.Config, roleForMediaType(mf.Config.MediaType, "config"))
	}
	for i, layer := range mf.Layers {
		role := fmt.Sprintf("layer %d", i+1)
		l.mark(layer, roleForMediaType(layer.MediaType, role))
	}
	for _, blob := range mf.Blobs {
		l.annotateDescriptor(blob, "artifact", visited)
	}
	if mf.Subject != nil {
		l.annotateDescriptor(*mf.Subject, "subject", visited)
	}
}

func (l *Layout) buildGraph() *GraphNode {
	root := &GraphNode{Label: "index.json", Kind: GraphRoot, BlobPath: "/index.json"}
	idxNode := l.Files["/index.json"]
	if idxNode == nil {
		return root
	}
	var idx indexFile
	if json.Unmarshal(idxNode.Data, &idx) != nil {
		return root
	}
	visited := map[string]bool{}
	for _, desc := range idx.Manifests {
		root.Children = append(root.Children, l.graphDescriptor(desc, "manifest", visited))
	}
	return root
}

func (l *Layout) graphDescriptor(desc descriptor, fallbackRole string, visited map[string]bool) *GraphNode {
	node := &GraphNode{
		Label:     graphLabel(desc, fallbackRole),
		Kind:      graphKind(desc, fallbackRole),
		Digest:    desc.Digest,
		MediaType: desc.MediaType,
		Platform:  platformLabel(desc.Platform),
		Size:      desc.Size,
		BlobPath:  l.blobPath(desc.Digest),
	}
	if desc.Digest == "" || visited[desc.Digest] {
		return node
	}
	visited[desc.Digest] = true
	if IsIndexMediaType(desc.MediaType) {
		l.graphIndex(node, desc.Digest, visited)
		return node
	}
	if IsManifestMediaType(desc.MediaType) || desc.MediaType == "" {
		l.graphManifest(node, desc, visited)
	}
	return node
}

func (l *Layout) graphIndex(parent *GraphNode, digest string, visited map[string]bool) {
	node := l.nodeByDigest(digest)
	if node == nil {
		return
	}
	var idx imageIndexFile
	if json.Unmarshal(node.Data, &idx) != nil {
		return
	}
	for _, desc := range idx.Manifests {
		child := l.graphDescriptor(desc, "manifest", visited)
		if child.Platform != "" && child.Platform != "unknown/unknown" {
			platform := &GraphNode{Label: child.Platform, Kind: GraphPlatform, Platform: child.Platform, Summary: []string{"Platform: " + child.Platform, "Manifest: " + desc.Digest, "Media type: " + desc.MediaType}}
			platform.Children = append(platform.Children, child)
			parent.Children = append(parent.Children, platform)
		} else {
			parent.Children = append(parent.Children, child)
		}
	}
}

func (l *Layout) graphManifest(parent *GraphNode, desc descriptor, visited map[string]bool) {
	node := l.nodeByDigest(desc.Digest)
	if node == nil {
		return
	}
	var mf manifestFile
	if json.Unmarshal(node.Data, &mf) != nil {
		return
	}
	parent.Summary = []string{"Manifest: " + desc.Digest, "Media type: " + desc.MediaType}
	if desc.Platform != nil {
		parent.Summary = append(parent.Summary, "Platform: "+platformLabel(desc.Platform))
	}
	if mf.Config.Digest != "" {
		parent.Children = append(parent.Children, l.graphLeaf(mf.Config, GraphConfig, "config"))
	}
	for i, layerDesc := range mf.Layers {
		role := fmt.Sprintf("layer %d", i+1)
		parent.Children = append(parent.Children, l.graphLeaf(layerDesc, GraphLayer, role))
	}
	for _, blob := range mf.Blobs {
		parent.Children = append(parent.Children, l.graphDescriptor(blob, "artifact", visited))
	}
	if mf.Subject != nil {
		parent.Children = append(parent.Children, l.graphDescriptor(*mf.Subject, "subject", visited))
	}
}

func (l *Layout) graphLeaf(desc descriptor, kind GraphKind, role string) *GraphNode {
	return &GraphNode{Label: graphLabel(desc, role), Kind: kind, Digest: desc.Digest, MediaType: desc.MediaType, Size: desc.Size, BlobPath: l.blobPath(desc.Digest), Summary: []string{titleWord(role) + ": " + desc.Digest, "Media type: " + desc.MediaType, fmt.Sprintf("Size: %d bytes", desc.Size)}}
}

func titleWord(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func graphKind(desc descriptor, fallbackRole string) GraphKind {
	if IsIndexMediaType(desc.MediaType) {
		return GraphIndex
	}
	if IsManifestMediaType(desc.MediaType) || strings.Contains(strings.ToLower(fallbackRole), "manifest") {
		return GraphManifest
	}
	return GraphBlob
}

func graphLabel(desc descriptor, fallbackRole string) string {
	short := shortDigest(desc.Digest)
	role := roleForMediaType(desc.MediaType, fallbackRole)
	if role == "" {
		role = fallbackRole
	}
	if short == "" {
		return role
	}
	if desc.Size > 0 {
		return fmt.Sprintf("%s %s  %d B", role, short, desc.Size)
	}
	return role + " " + short
}

func platformLabel(platform map[string]string) string {
	if platform == nil {
		return ""
	}
	os := platform["os"]
	arch := platform["architecture"]
	variant := platform["variant"]
	if os == "" && arch == "" {
		return ""
	}
	if variant != "" {
		return os + "/" + arch + "/" + variant
	}
	return os + "/" + arch
}

func shortDigest(digest string) string {
	if digest == "" {
		return ""
	}
	parts := strings.SplitN(digest, ":", 2)
	encoded := digest
	if len(parts) == 2 {
		encoded = parts[1]
	}
	if len(encoded) > 12 {
		encoded = encoded[:12] + "..."
	}
	return encoded
}

func (l *Layout) blobPath(digest string) string {
	blob, ok := l.Blobs[digest]
	if !ok {
		return ""
	}
	return "/blobs/" + blob.Algorithm + "/" + blob.Encoded
}

func (l *Layout) mark(desc descriptor, role string) {
	if desc.Digest == "" {
		return
	}
	node := l.nodeByDigest(desc.Digest)
	if node == nil {
		return
	}
	if node.Blob == nil {
		return
	}
	node.Blob.MediaType = desc.MediaType
	node.Blob.Role = role
}

func (l *Layout) nodeByDigest(digest string) *Node {
	blob, ok := l.Blobs[digest]
	if !ok {
		return nil
	}
	return l.Files["/blobs/"+blob.Algorithm+"/"+blob.Encoded]
}

func roleForMediaType(mediaType, fallback string) string {
	lower := strings.ToLower(mediaType)
	switch {
	case strings.Contains(lower, "layer"):
		if strings.Contains(lower, "zstd") || strings.Contains(lower, "zst") {
			return fallback + " zstd"
		}
		if strings.Contains(lower, "gzip") || strings.Contains(lower, "+gz") {
			return fallback + " gzip"
		}
		return fallback
	case strings.Contains(lower, "manifest"):
		return "manifest"
	case strings.Contains(lower, "config"):
		return "config"
	case strings.Contains(lower, "sarif"):
		return "SARIF"
	case strings.Contains(lower, "spdx"):
		return "SBOM"
	case strings.Contains(lower, "cyclonedx"):
		return "SBOM"
	default:
		return fallback
	}
}

func IsLayerMediaType(mediaType string) bool {
	lower := strings.ToLower(mediaType)
	return strings.Contains(lower, "layer") && strings.Contains(lower, "tar")
}

func IsIndexMediaType(mediaType string) bool {
	lower := strings.ToLower(mediaType)
	return lower == "application/vnd.oci.image.index.v1+json" || lower == "application/vnd.docker.distribution.manifest.list.v2+json"
}

func IsManifestMediaType(mediaType string) bool {
	lower := strings.ToLower(mediaType)
	return lower == "application/vnd.oci.image.manifest.v1+json" || lower == "application/vnd.docker.distribution.manifest.v2+json"
}

func IsGzip(data []byte, mediaType string) bool {
	return bytes.HasPrefix(data, []byte{0x1f, 0x8b}) || strings.Contains(strings.ToLower(mediaType), "gzip") || strings.Contains(strings.ToLower(mediaType), "+gz")
}

func IsZstd(data []byte, mediaType string) bool {
	return bytes.HasPrefix(data, []byte{0x28, 0xb5, 0x2f, 0xfd}) || strings.Contains(strings.ToLower(mediaType), "zstd") || strings.Contains(strings.ToLower(mediaType), "zst")
}

func DisplayName(n *Node) string {
	if n == nil {
		return ""
	}
	if n.Blob != nil && n.Blob.Role != "" && n.Blob.Role != "blob" {
		name := n.Name
		if len(name) > 12 {
			name = name[:12] + "..."
		}
		return name + "  " + n.Blob.Role
	}
	return n.Name
}
