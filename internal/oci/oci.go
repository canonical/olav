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
	for _, manifest := range idx.Manifests {
		l.mark(manifest, roleForMediaType(manifest.MediaType, "manifest"))
		l.annotateManifest(manifest.Digest)
	}
}

func (l *Layout) annotateManifest(digest string) {
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
		l.mark(blob, roleForMediaType(blob.MediaType, "artifact"))
	}
	if mf.Subject != nil {
		l.mark(*mf.Subject, roleForMediaType(mf.Subject.MediaType, "subject"))
	}
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
