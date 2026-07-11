package export

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/olav/internal/layer"
	"github.com/canonical/olav/internal/oci"
)

func TestExportNodePreservesOCIPath(t *testing.T) {
	t.Chdir(t.TempDir())
	node := &oci.Node{Path: "/blobs/sha256/abc", Data: []byte("blob"), Mode: 0o600}
	dest, err := Node(node)
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(DefaultDir, "oci-layout", "blobs", "sha256", "abc")
	if dest != expected {
		t.Fatalf("dest = %q, want %q", dest, expected)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "blob" {
		t.Fatalf("unexpected export data: %q", data)
	}
}

func TestExportLayerEntryPreservesPath(t *testing.T) {
	t.Chdir(t.TempDir())
	entry := &layer.Entry{Path: "/etc/os-release", Data: []byte("NAME=test\n")}
	dest, err := LayerEntry("/blobs/sha256/abc", entry)
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(DefaultDir, "layers", "blobs_sha256_abc", "etc", "os-release")
	if dest != expected {
		t.Fatalf("dest = %q, want %q", dest, expected)
	}
}

func TestExportRejectsNonFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := Node(&oci.Node{Path: "/dir", IsDir: true}); err == nil {
		t.Fatal("expected directory node export error")
	}
	if _, err := LayerEntry("layer", &layer.Entry{Path: "/dir", Type: '5'}); err == nil {
		t.Fatal("expected non-regular layer entry export error")
	}
}
