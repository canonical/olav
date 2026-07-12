package layer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestOpenPlainTarIndexesEntries(t *testing.T) {
	l := openTestLayer(t, "application/vnd.oci.image.layer.v1.tar", testTar(t))
	entry := l.Entries["/etc/os-release"]
	if entry == nil {
		t.Fatal("expected /etc/os-release")
	}
	if !entry.IsText() {
		t.Fatal("expected text entry")
	}
	if string(entry.Data) != "NAME=test\n" {
		t.Fatalf("unexpected data: %q", entry.Data)
	}
	if l.Entries["/bin/app"] == nil || l.Entries["/bin/app"].IsText() {
		t.Fatal("expected non-text /bin/app")
	}
}

func TestOpenCompressedLayers(t *testing.T) {
	plain := testTar(t)
	openTestLayer(t, "application/vnd.oci.image.layer.v1.tar+gzip", gzipData(t, plain))
	openTestLayer(t, "application/vnd.oci.image.layer.v1.tar+zstd", zstdData(t, plain))
}

func TestWhiteoutDetails(t *testing.T) {
	l := openTestLayer(t, "application/vnd.oci.image.layer.v1.tar", testTar(t))
	details := strings.Join(l.Entries["/etc/.wh.old"].Details(), "\n")
	if !strings.Contains(details, "OCI whiteout") || !strings.Contains(details, "/etc/old") {
		t.Fatalf("unexpected whiteout details:\n%s", details)
	}
}

func TestChiselManifestDetails(t *testing.T) {
	entry := &Entry{Name: "manifest.wall", Path: "/some/path/manifest.wall", Type: tar.TypeReg, Data: []byte{0x28, 0xb5, 0x2f, 0xfd}}
	if !entry.IsChiselManifest() {
		t.Fatal("expected chisel manifest detection")
	}
	details := strings.Join(entry.Details(), "\n")
	if !strings.Contains(details, "Chisel manifest preview") {
		t.Fatalf("unexpected details:\n%s", details)
	}
}

func openTestLayer(t *testing.T, mediaType string, data []byte) *Layer {
	t.Helper()
	l, err := Open("test", mediaType, data)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func testTar(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	entries := []struct {
		name string
		mode int64
		data []byte
	}{
		{name: "etc/os-release", mode: 0o644, data: []byte("NAME=test\n")},
		{name: "bin/app", mode: 0o755, data: []byte{0x00, 0x01, 0x02}},
		{name: "etc/.wh.old", mode: 0o644, data: nil},
	}
	for _, entry := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func gzipData(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zstdData(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	return buf.Bytes()
}
