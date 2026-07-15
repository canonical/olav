package source

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

type progressCounter struct {
	total    int64
	w        io.Writer
	complete atomic.Int64
	mu       sync.Mutex
	lastPct  int
	rendered bool
}

func newProgressCounter(total int64, w io.Writer) *progressCounter {
	return &progressCounter{total: total, w: w, lastPct: -1}
}

func (c *progressCounter) add(n int64) {
	if n <= 0 {
		return
	}
	complete := c.complete.Add(n)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.total <= 0 {
		if !c.rendered {
			c.rendered = true
			renderProgressLine(c.w, complete, c.total)
		}
		return
	}
	if complete > c.total {
		complete = c.total
	}
	pct := int(float64(complete) / float64(c.total) * 100)
	if pct == c.lastPct {
		return
	}
	c.lastPct = pct
	c.rendered = true
	renderProgressLine(c.w, complete, c.total)
}

func (c *progressCounter) finish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.rendered {
		return
	}
	renderProgressLine(c.w, c.complete.Load(), c.total)
	fmt.Fprintln(c.w)
}

func renderProgressLine(w io.Writer, complete, total int64) {
	const width = 24
	if total <= 0 {
		fmt.Fprintf(w, "\rolav: copying image blobs...")
		return
	}
	if complete > total {
		complete = total
	}
	filled := int(float64(complete) / float64(total) * width)
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("=", filled) + strings.Repeat(" ", width-filled)
	percent := int(float64(complete) / float64(total) * 100)
	fmt.Fprintf(w, "\rolav: [%s] %3d%%  %s / %s", bar, percent, formatBytes(complete), formatBytes(total))
}

func formatBytes(n int64) string {
	const mib = 1 << 20
	if n < mib {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/mib)
}

type progressReader struct {
	io.ReadCloser
	c *progressCounter
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.c.add(int64(n))
	return n, err
}

type progressLayer struct {
	v1.Layer
	c *progressCounter
}

func (l *progressLayer) Compressed() (io.ReadCloser, error) {
	rc, err := l.Layer.Compressed()
	if err != nil {
		return nil, err
	}
	return &progressReader{ReadCloser: rc, c: l.c}, nil
}

type progressImage struct {
	v1.Image
	c *progressCounter
}

func (i *progressImage) Layers() ([]v1.Layer, error) {
	layers, err := i.Image.Layers()
	if err != nil {
		return nil, err
	}
	wrapped := make([]v1.Layer, len(layers))
	for j, l := range layers {
		wrapped[j] = &progressLayer{Layer: l, c: i.c}
	}
	return wrapped, nil
}

type progressIndex struct {
	idx v1.ImageIndex
	c   *progressCounter
}

func (ix *progressIndex) MediaType() (types.MediaType, error) { return ix.idx.MediaType() }
func (ix *progressIndex) Digest() (v1.Hash, error)            { return ix.idx.Digest() }
func (ix *progressIndex) Size() (int64, error)                { return ix.idx.Size() }
func (ix *progressIndex) IndexManifest() (*v1.IndexManifest, error) {
	return ix.idx.IndexManifest()
}
func (ix *progressIndex) RawManifest() ([]byte, error) { return ix.idx.RawManifest() }

func (ix *progressIndex) Image(h v1.Hash) (v1.Image, error) {
	img, err := ix.idx.Image(h)
	if err != nil {
		return nil, err
	}
	return &progressImage{Image: img, c: ix.c}, nil
}

func (ix *progressIndex) ImageIndex(h v1.Hash) (v1.ImageIndex, error) {
	child, err := ix.idx.ImageIndex(h)
	if err != nil {
		return nil, err
	}
	return &progressIndex{idx: child, c: ix.c}, nil
}

// Layer mirrors the optional method go-containerregistry's layout writer uses
// for index children that are neither manifests nor indexes.
func (ix *progressIndex) Layer(h v1.Hash) (v1.Layer, error) {
	wl, ok := ix.idx.(interface {
		Layer(v1.Hash) (v1.Layer, error)
	})
	if !ok {
		return nil, fmt.Errorf("index does not expose blob %s", h)
	}
	l, err := wl.Layer(h)
	if err != nil {
		return nil, err
	}
	return &progressLayer{Layer: l, c: ix.c}, nil
}

// imageBlobTotal sums only the blobs that stream through the wrapped layers;
// config and manifest blobs are written directly by the layout writer and
// never pass the progress reader, so they are left out of the total.
func imageBlobTotal(img v1.Image) int64 {
	m, err := img.Manifest()
	if err != nil {
		return 0
	}
	var total int64
	for _, l := range m.Layers {
		total += l.Size
	}
	return total
}

func indexBlobTotal(idx v1.ImageIndex) int64 {
	m, err := idx.IndexManifest()
	if err != nil {
		return 0
	}
	var (
		total  int64
		failed bool
	)
	for _, desc := range m.Manifests {
		switch {
		case desc.MediaType.IsIndex():
			child, err := idx.ImageIndex(desc.Digest)
			if err != nil {
				failed = true
				continue
			}
			total += indexBlobTotal(child)
		case desc.MediaType.IsImage():
			child, err := idx.Image(desc.Digest)
			if err != nil {
				failed = true
				continue
			}
			total += imageBlobTotal(child)
		default:
			total += desc.Size
		}
	}
	if failed {
		return 0
	}
	return total
}

func withImageProgress(img v1.Image, progress io.Writer) (v1.Image, func()) {
	if progress == nil {
		return img, func() {}
	}
	c := newProgressCounter(imageBlobTotal(img), progress)
	return &progressImage{Image: img, c: c}, c.finish
}

func withIndexProgress(idx v1.ImageIndex, progress io.Writer) (v1.ImageIndex, func()) {
	if progress == nil {
		return idx, func() {}
	}
	c := newProgressCounter(indexBlobTotal(idx), progress)
	return &progressIndex{idx: idx, c: c}, c.finish
}
