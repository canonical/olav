package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

const fetchAttempts = 4

// blobFetcher downloads registry blobs with HTTP range requests. Partial
// downloads persist in the cache tmp area named by digest, so an interrupted
// pull resumes from the last received byte on the next run.
type blobFetcher struct {
	blobURL    func(v1.Hash) string
	client     *http.Client
	partialDir string
	counter    *progressCounter
	progress   io.Writer
	consumed   []string
}

func newBlobFetcher(ctx context.Context, ref name.Reference, counter *progressCounter, progress io.Writer) (*blobFetcher, error) {
	repo := ref.Context()
	auth, err := authKeychain().Resolve(repo.Registry)
	if err != nil {
		return nil, err
	}
	rt, err := transport.NewWithContext(ctx, repo.Registry, auth, http.DefaultTransport, []string{repo.Scope(transport.PullScope)})
	if err != nil {
		return nil, err
	}
	root, err := cacheRoot()
	if err != nil {
		return nil, err
	}
	partialDir := filepath.Join(root, "tmp", "partials")
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		return nil, err
	}
	base := fmt.Sprintf("%s://%s/v2/%s/blobs/", repo.Registry.Scheme(), repo.RegistryStr(), repo.RepositoryStr())
	return &blobFetcher{
		blobURL:    func(h v1.Hash) string { return base + h.String() },
		client:     &http.Client{Transport: rt},
		partialDir: partialDir,
		counter:    counter,
		progress:   progress,
	}, nil
}

func (f *blobFetcher) fetchBlob(ctx context.Context, digest v1.Hash, size int64, dst string) error {
	if digest.Algorithm != "sha256" {
		return fmt.Errorf("unsupported digest algorithm %q", digest.Algorithm)
	}
	if info, err := os.Stat(dst); err == nil && info.Size() == size {
		f.counter.add(size)
		return nil
	}
	partial := filepath.Join(f.partialDir, digest.Algorithm+"-"+digest.Hex+".partial")
	if info, err := os.Stat(partial); err == nil && info.Size() > 0 {
		if f.progress != nil {
			fmt.Fprintf(f.progress, "olav: resuming blob %s at %s\n", shortHex(digest), formatBytes(info.Size()))
		}
		f.counter.add(info.Size())
	}
	var lastErr error
	for attempt := 0; attempt < fetchAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = f.fetchOnce(ctx, digest, size, partial)
		if lastErr == nil {
			break
		}
		if !isRetryableFetchError(lastErr) {
			return lastErr
		}
	}
	if lastErr != nil {
		return lastErr
	}
	if err := verifyBlobDigest(partial, digest); err != nil {
		_ = os.Remove(partial)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// Hardlink instead of rename: the partial keeps the bytes reachable for
	// resume until the whole layout lands in the cache, in case a later blob
	// of the same pull is interrupted.
	if err := os.Link(partial, dst); err != nil {
		return err
	}
	f.consumed = append(f.consumed, partial)
	return nil
}

func (f *blobFetcher) fetchOnce(ctx context.Context, digest v1.Hash, size int64, partial string) error {
	offset := int64(0)
	if info, err := os.Stat(partial); err == nil {
		offset = info.Size()
	}
	if size > 0 && offset > size {
		if err := os.Truncate(partial, 0); err != nil {
			return err
		}
		offset = 0
	}
	if size > 0 && offset == size {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.blobURL(digest), nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	switch {
	case offset > 0 && resp.StatusCode == http.StatusPartialContent:
	case resp.StatusCode == http.StatusOK:
		// Server ignored the range request; start over with the full body.
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	case offset > 0 && resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		// Server refuses ranges outright; drop the partial and fetch in full.
		if err := os.Truncate(partial, 0); err != nil {
			return err
		}
		return f.fetchOnce(ctx, digest, size, partial)
	default:
		return &fetchStatusError{status: resp.StatusCode, url: req.URL.String()}
	}
	file, err := os.OpenFile(partial, flags, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, &countingReader{r: resp.Body, counter: f.counter})
	return err
}

// cleanupPartials removes partial files whose bytes are fully linked into the
// written layout. Call only once the layout is complete.
func (f *blobFetcher) cleanupPartials() {
	for _, partial := range f.consumed {
		_ = os.Remove(partial)
	}
	f.consumed = nil
}

func verifyBlobDigest(path string, digest v1.Hash) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != digest.Hex {
		return fmt.Errorf("digest mismatch for blob %s: downloaded sha256:%s", digest, got)
	}
	return nil
}

type fetchStatusError struct {
	status int
	url    string
}

func (e *fetchStatusError) Error() string {
	return fmt.Sprintf("fetch %s: unexpected status %d", e.url, e.status)
}

func isRetryableFetchError(err error) bool {
	var statusErr *fetchStatusError
	if errors.As(err, &statusErr) {
		return statusErr.status >= 500 || statusErr.status == http.StatusTooManyRequests
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// Network-level errors: the partial keeps what already arrived, retry
	// resumes from there.
	return true
}

func shortHex(digest v1.Hash) string {
	if len(digest.Hex) > 12 {
		return digest.Hex[:12]
	}
	return digest.Hex
}

type countingReader struct {
	r       io.Reader
	counter *progressCounter
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.counter.add(int64(n))
	return n, err
}

type progressCounter struct {
	total    int64
	w        io.Writer
	complete atomic.Int64
	lastPct  atomic.Int64
}

func newProgressCounter(total int64, w io.Writer) *progressCounter {
	c := &progressCounter{total: total, w: w}
	c.lastPct.Store(-1)
	return c
}

func (c *progressCounter) add(n int64) {
	if c == nil || c.w == nil || n <= 0 {
		return
	}
	complete := c.complete.Add(n)
	if c.total <= 0 {
		return
	}
	pct := complete * 100 / c.total
	if pct > 100 {
		pct = 100
	}
	if c.lastPct.Swap(pct) == pct {
		return
	}
	renderProgressLine(c.w, complete, c.total)
}

func (c *progressCounter) finish() {
	if c == nil || c.w == nil || c.lastPct.Load() < 0 {
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

type rawBlob struct {
	digest v1.Hash
	data   []byte
}

type fetchBlobRef struct {
	digest v1.Hash
	size   int64
}

// pullPlan splits an image or index into blobs already in memory (manifests,
// configs) and blobs worth fetching with the resumable fetcher (layers and
// other large artifacts).
type pullPlan struct {
	raw   []rawBlob
	fetch []fetchBlobRef
	seen  map[string]bool
}

func (p *pullPlan) mark(digest v1.Hash) bool {
	if p.seen == nil {
		p.seen = map[string]bool{}
	}
	if p.seen[digest.String()] {
		return false
	}
	p.seen[digest.String()] = true
	return true
}

func (p *pullPlan) addRaw(digest v1.Hash, data []byte) {
	if p.mark(digest) {
		p.raw = append(p.raw, rawBlob{digest: digest, data: data})
	}
}

func (p *pullPlan) addFetch(digest v1.Hash, size int64) {
	if p.mark(digest) {
		p.fetch = append(p.fetch, fetchBlobRef{digest: digest, size: size})
	}
}

func (p *pullPlan) fetchTotal() int64 {
	var total int64
	for _, blob := range p.fetch {
		total += blob.size
	}
	return total
}

func (p *pullPlan) addImage(img v1.Image) (v1.Descriptor, error) {
	rawManifest, err := img.RawManifest()
	if err != nil {
		return v1.Descriptor{}, err
	}
	digest, err := img.Digest()
	if err != nil {
		return v1.Descriptor{}, err
	}
	mediaType, err := img.MediaType()
	if err != nil {
		return v1.Descriptor{}, err
	}
	manifest, err := img.Manifest()
	if err != nil {
		return v1.Descriptor{}, err
	}
	rawConfig, err := img.RawConfigFile()
	if err != nil {
		return v1.Descriptor{}, err
	}
	p.addRaw(digest, rawManifest)
	p.addRaw(manifest.Config.Digest, rawConfig)
	for _, layer := range manifest.Layers {
		p.addFetch(layer.Digest, layer.Size)
	}
	return v1.Descriptor{MediaType: mediaType, Digest: digest, Size: int64(len(rawManifest))}, nil
}

func (p *pullPlan) addIndex(idx v1.ImageIndex) (v1.Descriptor, error) {
	rawManifest, err := idx.RawManifest()
	if err != nil {
		return v1.Descriptor{}, err
	}
	digest, err := idx.Digest()
	if err != nil {
		return v1.Descriptor{}, err
	}
	mediaType, err := idx.MediaType()
	if err != nil {
		return v1.Descriptor{}, err
	}
	p.addRaw(digest, rawManifest)
	manifest, err := idx.IndexManifest()
	if err != nil {
		return v1.Descriptor{}, err
	}
	for _, child := range manifest.Manifests {
		switch {
		case child.MediaType.IsIndex():
			childIdx, err := idx.ImageIndex(child.Digest)
			if err != nil {
				return v1.Descriptor{}, err
			}
			if _, err := p.addIndex(childIdx); err != nil {
				return v1.Descriptor{}, err
			}
		case child.MediaType.IsImage():
			childImg, err := idx.Image(child.Digest)
			if err != nil {
				return v1.Descriptor{}, err
			}
			if _, err := p.addImage(childImg); err != nil {
				return v1.Descriptor{}, err
			}
		default:
			p.addFetch(child.Digest, child.Size)
		}
	}
	return v1.Descriptor{MediaType: mediaType, Digest: digest, Size: int64(len(rawManifest))}, nil
}
