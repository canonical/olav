package source

import (
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/random"
)

func TestImageProgressReachesTotal(t *testing.T) {
	img, err := random.Image(2048, 3)
	if err != nil {
		t.Fatal(err)
	}
	total := imageBlobTotal(img)
	if total <= 0 {
		t.Fatalf("imageBlobTotal = %d, want > 0", total)
	}

	var out strings.Builder
	wrapped, finish := withImageProgress(img, &out)
	path, err := layout.Write(t.TempDir(), empty.Index)
	if err != nil {
		t.Fatal(err)
	}
	if err := path.AppendImage(wrapped); err != nil {
		t.Fatal(err)
	}
	finish()

	got := out.String()
	if !strings.Contains(got, "100%") {
		t.Fatalf("progress output missing 100%%: %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("progress output does not end with newline: %q", got)
	}
}

func TestIndexProgressReachesTotal(t *testing.T) {
	idx, err := random.Index(1024, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	total := indexBlobTotal(idx)
	if total <= 0 {
		t.Fatalf("indexBlobTotal = %d, want > 0", total)
	}

	var out strings.Builder
	wrapped, finish := withIndexProgress(idx, &out)
	path, err := layout.Write(t.TempDir(), empty.Index)
	if err != nil {
		t.Fatal(err)
	}
	if err := path.AppendIndex(wrapped); err != nil {
		t.Fatal(err)
	}
	finish()

	if got := out.String(); !strings.Contains(got, "100%") {
		t.Fatalf("progress output missing 100%%: %q", got)
	}
}

func TestNilProgressWriterIsNoop(t *testing.T) {
	img, err := random.Image(256, 1)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, finish := withImageProgress(img, nil)
	if wrapped != img {
		t.Fatal("nil progress writer should return the image unwrapped")
	}
	finish()
}
