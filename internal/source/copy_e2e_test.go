package source

import (
	"context"
	"io"
	"log"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func testRegistry(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func TestResolveRemotePullsCompleteLayout(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	host := testRegistry(t)

	img, err := random.Image(4096, 3)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := name.ParseReference(host + "/test/img:latest")
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatal(err)
	}

	var progress strings.Builder
	resolved, err := Resolve(context.Background(), Options{Input: "docker://" + host + "/test/img:latest", Progress: &progress})
	if err != nil {
		t.Fatal(err)
	}

	manifest, err := img.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	blobs := []string{digest.Hex, manifest.Config.Digest.Hex}
	for _, layer := range manifest.Layers {
		blobs = append(blobs, layer.Digest.Hex)
	}
	for _, hex := range blobs {
		if _, err := os.Stat(filepath.Join(resolved.LocalPath, "blobs", "sha256", hex)); err != nil {
			t.Fatalf("missing blob %s: %v", hex, err)
		}
	}
	if !strings.Contains(progress.String(), "100%") {
		t.Fatalf("progress output missing 100%%: %q", progress.String())
	}

	// Second resolve must hit the cache without touching blobs again.
	progress.Reset()
	cached, err := Resolve(context.Background(), Options{Input: "docker://" + host + "/test/img:latest", Progress: &progress})
	if err != nil {
		t.Fatal(err)
	}
	if cached.LocalPath != resolved.LocalPath {
		t.Fatalf("cache miss on second resolve: %q vs %q", cached.LocalPath, resolved.LocalPath)
	}
	if !strings.Contains(progress.String(), "using cached") {
		t.Fatalf("expected cached resolve, got %q", progress.String())
	}
}

func TestResolveRemotePlatformAllPullsIndex(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	host := testRegistry(t)

	idx, err := random.Index(2048, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := name.ParseReference(host + "/test/multi:latest")
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.WriteIndex(ref, idx); err != nil {
		t.Fatal(err)
	}

	resolved, err := Resolve(context.Background(), Options{Input: "docker://" + host + "/test/multi:latest", Platform: "all"})
	if err != nil {
		t.Fatal(err)
	}

	manifest, err := idx.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range manifest.Manifests {
		childImg, err := idx.Image(child.Digest)
		if err != nil {
			t.Fatal(err)
		}
		childManifest, err := childImg.Manifest()
		if err != nil {
			t.Fatal(err)
		}
		for _, layer := range childManifest.Layers {
			if _, err := os.Stat(filepath.Join(resolved.LocalPath, "blobs", "sha256", layer.Digest.Hex)); err != nil {
				t.Fatalf("missing child layer %s: %v", layer.Digest.Hex, err)
			}
		}
	}
}

func TestResolveRemoteResumesInterruptedPull(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	host := testRegistry(t)

	img, err := random.Image(8192, 1)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := name.ParseReference(host + "/test/resume:latest")
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatal(err)
	}

	// Simulate an interrupted earlier run: half the layer already sits in the
	// partials dir under its digest name.
	manifest, err := img.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	layerDigest := manifest.Layers[0].Digest
	layer, err := img.LayerByDigest(layerDigest)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := layer.Compressed()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	full := make([]byte, manifest.Layers[0].Size)
	if _, err := io.ReadFull(rc, full); err != nil {
		t.Fatal(err)
	}
	partialDir := filepath.Join(cacheDir, "olav", "tmp", "partials")
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(partialDir, "sha256-"+layerDigest.Hex+".partial")
	if err := os.WriteFile(partial, full[:len(full)/2], 0o600); err != nil {
		t.Fatal(err)
	}

	var progress strings.Builder
	resolved, err := Resolve(context.Background(), Options{Input: "docker://" + host + "/test/resume:latest", Progress: &progress})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(progress.String(), "resuming blob") {
		t.Fatalf("expected resume message, got %q", progress.String())
	}
	if _, err := os.Stat(filepath.Join(resolved.LocalPath, "blobs", "sha256", layerDigest.Hex)); err != nil {
		t.Fatalf("missing resumed layer: %v", err)
	}
}
