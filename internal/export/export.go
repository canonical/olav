package export

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/olav/internal/layer"
	"github.com/canonical/olav/internal/oci"
)

const DefaultDir = "olav-export"

func Node(n *oci.Node) (string, error) {
	if n == nil || n.IsDir {
		return "", fmt.Errorf("selected OCI item is not an exportable file")
	}
	clean := strings.TrimPrefix(n.Path, "/")
	dest := filepath.Join(DefaultDir, "oci-layout", filepath.FromSlash(clean))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(dest, n.Data, modePerm(n.Mode)); err != nil {
		return "", err
	}
	return dest, nil
}

func LayerEntry(layerTitle string, e *layer.Entry) (string, error) {
	if e == nil || !e.IsRegular() {
		return "", fmt.Errorf("selected layer item is not an exportable regular file")
	}
	cleanTitle := strings.NewReplacer("/", "_", ":", "_", " ", "_").Replace(strings.Trim(layerTitle, "/"))
	if cleanTitle == "" {
		cleanTitle = "layer"
	}
	cleanPath := strings.TrimPrefix(e.Path, "/")
	dest := filepath.Join(DefaultDir, "layers", cleanTitle, filepath.FromSlash(cleanPath))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(dest, e.Data, modePerm(e.Mode)); err != nil {
		return "", err
	}
	return dest, nil
}

func modePerm(mode os.FileMode) os.FileMode {
	perm := mode.Perm()
	if perm == 0 {
		return 0o644
	}
	return perm
}
