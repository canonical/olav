package source

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

type blobServer struct {
	data        []byte
	ignoreRange bool
	failures    int
	ranges      []string
}

func (s *blobServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.failures > 0 {
		s.failures--
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	rangeHeader := r.Header.Get("Range")
	s.ranges = append(s.ranges, rangeHeader)
	if rangeHeader == "" || s.ignoreRange {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(s.data)
		return
	}
	offsetStr := strings.TrimSuffix(strings.TrimPrefix(rangeHeader, "bytes="), "-")
	offset, err := strconv.ParseInt(offsetStr, 10, 64)
	if err != nil || offset < 0 || offset >= int64(len(s.data)) {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(s.data)-1, len(s.data)))
	w.WriteHeader(http.StatusPartialContent)
	_, _ = w.Write(s.data[offset:])
}

func testBlob(t *testing.T, size int) ([]byte, v1.Hash) {
	t.Helper()
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return data, v1.Hash{Algorithm: "sha256", Hex: hex.EncodeToString(sum[:])}
}

func testFetcher(t *testing.T, server *httptest.Server) *blobFetcher {
	t.Helper()
	return &blobFetcher{
		blobURL:    func(h v1.Hash) string { return server.URL + "/blobs/" + h.String() },
		client:     server.Client(),
		partialDir: t.TempDir(),
		counter:    newProgressCounter(0, nil),
	}
}

func TestFetchBlobFresh(t *testing.T) {
	data, digest := testBlob(t, 4096)
	backend := &blobServer{data: data}
	server := httptest.NewServer(backend)
	defer server.Close()

	f := testFetcher(t, server)
	dst := filepath.Join(t.TempDir(), "blob")
	if err := f.fetchBlob(context.Background(), digest, int64(len(data)), dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("downloaded blob differs from source")
	}
	f.cleanupPartials()
	entries, err := os.ReadDir(f.partialDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("cleanupPartials left %d files", len(entries))
	}
}

func TestFetchBlobResumesFromPartial(t *testing.T) {
	data, digest := testBlob(t, 4096)
	backend := &blobServer{data: data}
	server := httptest.NewServer(backend)
	defer server.Close()

	f := testFetcher(t, server)
	partial := filepath.Join(f.partialDir, "sha256-"+digest.Hex+".partial")
	if err := os.WriteFile(partial, data[:1000], 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "blob")
	if err := f.fetchBlob(context.Background(), digest, int64(len(data)), dst); err != nil {
		t.Fatal(err)
	}
	if len(backend.ranges) != 1 || backend.ranges[0] != "bytes=1000-" {
		t.Fatalf("expected one resume request from byte 1000, got %v", backend.ranges)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("resumed blob differs from source")
	}
}

func TestFetchBlobRestartsWhenRangeIgnored(t *testing.T) {
	data, digest := testBlob(t, 4096)
	backend := &blobServer{data: data, ignoreRange: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	f := testFetcher(t, server)
	partial := filepath.Join(f.partialDir, "sha256-"+digest.Hex+".partial")
	// Poison the partial: server ignoring ranges must overwrite, not append.
	if err := os.WriteFile(partial, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "blob")
	if err := f.fetchBlob(context.Background(), digest, int64(len(data)), dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("restarted blob differs from source")
	}
}

func TestFetchBlobDigestMismatch(t *testing.T) {
	data, _ := testBlob(t, 1024)
	_, wrongDigest := testBlob(t, 8)
	backend := &blobServer{data: data}
	server := httptest.NewServer(backend)
	defer server.Close()

	f := testFetcher(t, server)
	dst := filepath.Join(t.TempDir(), "blob")
	err := f.fetchBlob(context.Background(), wrongDigest, int64(len(data)), dst)
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected digest mismatch error, got %v", err)
	}
	partial := filepath.Join(f.partialDir, "sha256-"+wrongDigest.Hex+".partial")
	if _, statErr := os.Stat(partial); !os.IsNotExist(statErr) {
		t.Fatal("mismatched partial should be removed")
	}
}

func TestFetchBlobRetriesServerErrors(t *testing.T) {
	data, digest := testBlob(t, 1024)
	backend := &blobServer{data: data, failures: 2}
	server := httptest.NewServer(backend)
	defer server.Close()

	f := testFetcher(t, server)
	dst := filepath.Join(t.TempDir(), "blob")
	if err := f.fetchBlob(context.Background(), digest, int64(len(data)), dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("blob differs after retries")
	}
}

func TestFetchBlobGivesUpAfterAttempts(t *testing.T) {
	data, digest := testBlob(t, 1024)
	backend := &blobServer{data: data, failures: fetchAttempts}
	server := httptest.NewServer(backend)
	defer server.Close()

	f := testFetcher(t, server)
	dst := filepath.Join(t.TempDir(), "blob")
	err := f.fetchBlob(context.Background(), digest, int64(len(data)), dst)
	if err == nil || !strings.Contains(err.Error(), "unexpected status") {
		t.Fatalf("expected status error after exhausted retries, got %v", err)
	}
}
