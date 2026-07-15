package source

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
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
	if c == nil || c.w == nil || n <= 0 {
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

// sub rolls back bytes that were counted but then discarded, e.g. a resumed
// partial the server declines to honour. The next add re-renders at the
// corrected, lower percentage.
func (c *progressCounter) sub(n int64) {
	if c == nil || c.w == nil || n <= 0 {
		return
	}
	c.complete.Add(-n)
	c.mu.Lock()
	c.lastPct = -1
	c.mu.Unlock()
}

func (c *progressCounter) finish() {
	if c == nil || c.w == nil {
		return
	}
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

type countingReader struct {
	r       io.Reader
	counter *progressCounter
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.counter.add(int64(n))
	return n, err
}
