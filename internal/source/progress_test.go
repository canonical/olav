package source

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestProgressCounterReachesTotal(t *testing.T) {
	var out bytes.Buffer
	c := newProgressCounter(100, &out)
	c.add(40)
	c.add(60)
	c.finish()
	if !strings.Contains(out.String(), "100%") {
		t.Fatalf("counter never reached 100%%: %q", out.String())
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("finish should end with a newline: %q", out.String())
	}
}

func TestProgressCounterClampsOvershoot(t *testing.T) {
	var out bytes.Buffer
	c := newProgressCounter(100, &out)
	c.add(250)
	// The rendered line is clamped to the total; the raw count is left intact.
	if strings.Contains(out.String(), "250") {
		t.Fatalf("render should clamp to total, got %q", out.String())
	}
	if !strings.Contains(out.String(), "100%") {
		t.Fatalf("render should show 100%%, got %q", out.String())
	}
}

func TestProgressCounterSubRollsBack(t *testing.T) {
	var out bytes.Buffer
	c := newProgressCounter(100, &out)
	c.add(30)
	c.sub(30)
	c.add(100)
	if got := c.complete.Load(); got != 100 {
		t.Fatalf("complete = %d, want 100 after rollback", got)
	}
}

func TestProgressCounterUnknownTotal(t *testing.T) {
	var out bytes.Buffer
	c := newProgressCounter(0, &out)
	c.add(10)
	c.add(10)
	if !strings.Contains(out.String(), "copying image blobs") {
		t.Fatalf("unknown total should render the fallback line, got %q", out.String())
	}
}

func TestProgressCounterNilWriterIsNoop(t *testing.T) {
	c := newProgressCounter(100, nil)
	c.add(50)
	c.sub(10)
	c.finish() // must not panic writing to a nil writer
}

func TestCountingReaderCountsBytes(t *testing.T) {
	var out bytes.Buffer
	c := newProgressCounter(4, &out)
	r := &countingReader{r: strings.NewReader("data"), counter: c}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	if got := c.complete.Load(); got != 4 {
		t.Fatalf("counter = %d, want 4", got)
	}
}
