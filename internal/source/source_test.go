package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
)

func TestResolveLocalInputs(t *testing.T) {
	dir := t.TempDir()
	for _, input := range []string{dir, "oci:" + dir, "oci-archive:" + dir} {
		t.Run(input, func(t *testing.T) {
			got, err := Resolve(context.Background(), Options{Input: input})
			if err != nil {
				t.Fatal(err)
			}
			if got.LocalPath != dir {
				t.Fatalf("LocalPath = %q, want %q", got.LocalPath, dir)
			}
		})
	}
}

func TestResolveRejectsPlatformForLocalAndDaemon(t *testing.T) {
	dir := t.TempDir()
	if _, err := Resolve(context.Background(), Options{Input: dir, Platform: "linux/amd64"}); err == nil {
		t.Fatal("expected local --platform rejection")
	}
	if _, err := Resolve(context.Background(), Options{Input: "docker-daemon:ubuntu:latest", Platform: "linux/amd64"}); err == nil {
		t.Fatal("expected docker-daemon --platform rejection")
	}
}

func TestResolveRejectsBareImageReference(t *testing.T) {
	_, err := Resolve(context.Background(), Options{Input: "ubuntu:24.04"})
	if err == nil || !strings.Contains(err.Error(), "explicit transport prefix") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCacheRootUsesXDG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	root, err := cacheRoot()
	if err != nil {
		t.Fatal(err)
	}
	if root != filepath.Join(dir, "olav") {
		t.Fatalf("cache root = %q", root)
	}
}

func TestCacheKeyFromLayout(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.json"), []byte(`{"schemaVersion":2,"manifests":[{"digest":"sha256:abc"}]}`))
	key, digest, err := cacheKeyFromLayout(dir)
	if err != nil {
		t.Fatal(err)
	}
	if key != "sha256-abc" || digest != "sha256:abc" {
		t.Fatalf("key=%q digest=%q", key, digest)
	}
}

func TestCachedLayoutForDigest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	digest := "sha256:abc"
	if _, ok, err := cachedLayoutForDigest(digest); err != nil || ok {
		t.Fatalf("empty cache got ok=%v err=%v", ok, err)
	}
	cachePath, err := cachePathForDigest(digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(cachePath, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`))
	writeFile(t, filepath.Join(cachePath, "index.json"), []byte(`{"schemaVersion":2,"manifests":[]}`))
	got, ok, err := cachedLayoutForDigest(digest)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != cachePath {
		t.Fatalf("got path=%q ok=%v want path=%q ok=true", got, ok, cachePath)
	}
}

func TestAuthHint(t *testing.T) {
	err := withAuthHint("docker://example.com/private:latest", errors.New("unauthorized: authentication required"))
	if !strings.Contains(err.Error(), "~/.docker/config.json") || !strings.Contains(err.Error(), "podman") {
		t.Fatalf("expected auth hint, got %v", err)
	}
	plain := withAuthHint("docker://example.com/image", errors.New("connection refused"))
	if strings.Contains(plain.Error(), "Authentication hint") {
		t.Fatalf("did not expect auth hint: %v", plain)
	}
}

func TestResolveAuthFromContainersFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	writeFile(t, path, []byte(`{"auths":{"example.com":{"auth":"dXNlcjpwYXNz"}}}`))
	target, err := name.NewRegistry("example.com")
	if err != nil {
		t.Fatal(err)
	}
	auth, ok, err := resolveAuthFromFile(path, target)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected auth match")
	}
	cfg, err := auth.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Username != "user" || cfg.Password != "pass" {
		t.Fatalf("unexpected auth config: %#v", cfg)
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
