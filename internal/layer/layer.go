package layer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/canonical/olav/internal/oci"
	"github.com/canonical/olav/internal/preview"
	"github.com/klauspost/compress/zstd"
)

type Layer struct {
	Title     string
	MediaType string
	Root      *Entry
	Entries   map[string]*Entry
}

type Entry struct {
	Name     string
	Path     string
	Size     int64
	Mode     fs.FileMode
	Type     byte
	UID      int
	GID      int
	ModTime  time.Time
	LinkName string
	Data     []byte
	Parent   *Entry
	Children []*Entry
}

func Open(title, mediaType string, data []byte) (*Layer, error) {
	r, closeFn, err := readerFor(data, mediaType)
	if err != nil {
		return nil, err
	}
	defer closeFn()

	l := &Layer{Title: title, MediaType: mediaType, Root: &Entry{Name: "/", Path: "/", Type: tar.TypeDir, Mode: fs.ModeDir | 0o755}, Entries: map[string]*Entry{}}
	l.Entries["/"] = l.Root
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		p := cleanPath(h.Name)
		isDir := h.FileInfo().IsDir() || h.Typeflag == tar.TypeDir
		entry := l.ensureEntry(p, isDir)
		entry.Size = h.Size
		entry.Mode = h.FileInfo().Mode()
		entry.Type = h.Typeflag
		entry.UID = h.Uid
		entry.GID = h.Gid
		entry.ModTime = h.ModTime
		entry.LinkName = h.Linkname
		if h.Typeflag == tar.TypeReg {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			entry.Data = data
		}
	}
	sortEntry(l.Root)
	return l, nil
}

func readerFor(data []byte, mediaType string) (io.Reader, func(), error) {
	if oci.IsGzip(data, mediaType) {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, func() {}, err
		}
		return gz, func() { _ = gz.Close() }, nil
	}
	if oci.IsZstd(data, mediaType) {
		zr, err := zstd.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, func() {}, err
		}
		return zr, zr.Close, nil
	}
	return bytes.NewReader(data), func() {}, nil
}

func (l *Layer) ensureEntry(p string, isDir bool) *Entry {
	p = cleanPath(p)
	if entry, ok := l.Entries[p]; ok {
		if isDir {
			entry.Type = tar.TypeDir
		}
		return entry
	}
	parentPath := path.Dir(p)
	if parentPath == "." {
		parentPath = "/"
	}
	parent := l.ensureEntry(parentPath, true)
	entry := &Entry{Name: path.Base(p), Path: p, Parent: parent}
	if isDir {
		entry.Type = tar.TypeDir
		entry.Mode = fs.ModeDir | 0o755
	}
	parent.Children = append(parent.Children, entry)
	l.Entries[p] = entry
	return entry
}

func cleanPath(p string) string {
	p = path.Clean("/" + strings.TrimPrefix(p, "/"))
	if p == "." {
		return "/"
	}
	return p
}

func sortEntry(e *Entry) {
	sort.Slice(e.Children, func(i, j int) bool {
		a, b := e.Children[i], e.Children[j]
		if a.IsDir() != b.IsDir() {
			return a.IsDir()
		}
		return a.Name < b.Name
	})
	for _, child := range e.Children {
		sortEntry(child)
	}
}

func (e *Entry) IsDir() bool {
	return e != nil && e.Type == tar.TypeDir
}

func (e *Entry) IsRegular() bool {
	return e != nil && (e.Type == tar.TypeReg || e.Type == 0)
}

func (e *Entry) IsText() bool {
	return e.IsRegular() && preview.IsText(e.Data)
}

func (e *Entry) Details() []string {
	if e == nil {
		return []string{"No entry selected"}
	}
	typeName := "file"
	switch e.Type {
	case tar.TypeDir:
		typeName = "directory"
	case tar.TypeSymlink:
		typeName = "symlink"
	case tar.TypeLink:
		typeName = "hard link"
	case tar.TypeChar:
		typeName = "char device"
	case tar.TypeBlock:
		typeName = "block device"
	case tar.TypeFifo:
		typeName = "fifo"
	}
	lines := []string{
		"Entry Details",
		"Path: " + e.Path,
		"Type: " + typeName,
	}
	if strings.HasPrefix(e.Name, ".wh.") {
		lines = append(lines, "OCI whiteout: deletes "+path.Join(path.Dir(e.Path), strings.TrimPrefix(e.Name, ".wh.")))
	}
	if e.LinkName != "" {
		lines = append(lines, "Target: "+e.LinkName)
	}
	lines = append(lines,
		fmt.Sprintf("Size: %d bytes", e.Size),
		"Mode: "+e.Mode.String(),
		fmt.Sprintf("UID/GID: %d/%d", e.UID, e.GID),
	)
	if !e.ModTime.IsZero() {
		lines = append(lines, "Modified: "+e.ModTime.Format(time.RFC3339))
	}
	lines = append(lines, "")
	if e.IsText() {
		lines = append(lines, "Text preview is open in the right pane")
	} else if e.IsDir() {
		lines = append(lines, "(directory)")
	} else {
		lines = append(lines, "(no preview for non-text files inside tarball blobs)")
	}
	return lines
}

func DisplayName(e *Entry) string {
	if e == nil {
		return ""
	}
	if strings.HasPrefix(e.Name, ".wh.") {
		return e.Name + "  whiteout"
	}
	return e.Name
}
