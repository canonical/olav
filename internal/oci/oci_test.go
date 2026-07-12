package oci

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLoadDirectoryAnnotatesBlobs(t *testing.T) {
	dir := makeOCILayout(t)
	l, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l.Files["/index.json"] == nil {
		t.Fatal("expected index.json")
	}
	var foundLayer bool
	for _, blob := range l.Blobs {
		if strings.Contains(blob.Role, "layer") && blob.MediaType == "application/vnd.oci.image.layer.v1.tar+gzip" {
			foundLayer = true
		}
	}
	if !foundLayer {
		t.Fatalf("expected annotated layer blob, got %#v", l.Blobs)
	}
}

func TestLoadTarLayout(t *testing.T) {
	dir := makeOCILayout(t)
	tarPath := filepath.Join(t.TempDir(), "layout.tar")
	writeTarFromDir(t, tarPath, dir)
	if _, err := Load(tarPath); err != nil {
		t.Fatal(err)
	}
}

func TestRejectDockerArchive(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "docker archive") {
		t.Fatalf("expected docker archive error, got %v", err)
	}
}

func TestCompressionAndLayerMediaHelpers(t *testing.T) {
	if !IsLayerMediaType("application/vnd.oci.image.layer.v1.tar+zstd") {
		t.Fatal("expected layer media type")
	}
	if !IsGzip([]byte{0x1f, 0x8b}, "") {
		t.Fatal("expected gzip magic detection")
	}
	if !IsZstd([]byte{0x28, 0xb5, 0x2f, 0xfd}, "") {
		t.Fatal("expected zstd magic detection")
	}
}

func TestNestedIndexAnnotatesLayers(t *testing.T) {
	dir, layerDigest := makeNestedIndexLayout(t)
	l, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	node := l.nodeByDigest(layerDigest)
	if node == nil || node.Blob == nil || node.Blob.MediaType != "application/vnd.oci.image.layer.v1.tar+gzip" {
		t.Fatalf("expected nested layer annotation, got %#v", node)
	}
}

func TestGraphContainsPlatformManifestAndLayer(t *testing.T) {
	dir, layerDigest := makeNestedIndexLayout(t)
	l, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l.GraphRoot == nil || len(l.GraphRoot.Children) == 0 {
		t.Fatal("expected graph root with children")
	}
	if !graphContains(l.GraphRoot, "linux/amd64") {
		t.Fatalf("expected graph to contain platform node: %#v", l.GraphRoot)
	}
	if !graphContains(l.GraphRoot, shortDigest(layerDigest)) {
		t.Fatalf("expected graph to contain layer digest %s", layerDigest)
	}
}

func makeNestedIndexLayout(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`))
	layerData := []byte("layer")
	layerDigest := writeBlob(t, dir, layerData)
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"` + layerDigest + `","size":` + strconv.Itoa(len(layerData)) + `}]}`)
	manifestDigest := writeBlob(t, dir, manifest)
	nestedIndex := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"` + manifestDigest + `","size":` + strconv.Itoa(len(manifest)) + `,"platform":{"os":"linux","architecture":"amd64"}}]}`)
	nestedDigest := writeBlob(t, dir, nestedIndex)
	index := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.index.v1+json","digest":"` + nestedDigest + `","size":` + strconv.Itoa(len(nestedIndex)) + `}]}`)
	mustWrite(t, filepath.Join(dir, "index.json"), index)
	return dir, layerDigest
}

func graphContains(n *GraphNode, needle string) bool {
	if n == nil {
		return false
	}
	if strings.Contains(n.Label, needle) || strings.Contains(n.Platform, needle) || strings.Contains(n.Digest, needle) {
		return true
	}
	for _, child := range n.Children {
		if graphContains(child, needle) {
			return true
		}
	}
	return false
}

func makeOCILayout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`))

	config := []byte(`{"architecture":"amd64","os":"linux"}`)
	layerData := []byte("not actually compressed for loader annotation")
	configDigest := writeBlob(t, dir, config)
	layerDigest := writeBlob(t, dir, layerData)

	manifest := []byte(`{"schemaVersion":2,"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"` + configDigest + `","size":` + strconv.Itoa(len(config)) + `},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"` + layerDigest + `","size":` + strconv.Itoa(len(layerData)) + `}]}`)
	manifestDigest := writeBlob(t, dir, manifest)
	index := []byte(`{"schemaVersion":2,"manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"` + manifestDigest + `","size":` + strconv.Itoa(len(manifest)) + `}]}`)
	mustWrite(t, filepath.Join(dir, "index.json"), index)
	return dir
}

func writeBlob(t *testing.T, dir string, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	encoded := hex.EncodeToString(sum[:])
	path := filepath.Join(dir, "blobs", "sha256", encoded)
	mustWrite(t, path, data)
	return "sha256:" + encoded
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTarFromDir(t *testing.T, tarPath, dir string) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, rel := range []string{"oci-layout", "index.json"} {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: rel, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	for digest, blob := range mustReadBlobs(t, dir) {
		name := filepath.ToSlash(filepath.Join("blobs", "sha256", digest))
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(blob))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(blob); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tarPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustReadBlobs(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	entries, err := os.ReadDir(filepath.Join(dir, "blobs", "sha256"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, "blobs", "sha256", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[entry.Name()] = data
	}
	return out
}
