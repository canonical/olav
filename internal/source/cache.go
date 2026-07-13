package source

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type cacheMetadata struct {
	Source    string `json:"source"`
	Platform  string `json:"platform,omitempty"`
	Digest    string `json:"digest"`
	CreatedAt string `json:"createdAt"`
}

func cacheRoot() (string, error) {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "olav"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "olav"), nil
}

func cacheKeyFromLayout(layoutDir string) (string, string, error) {
	data, err := os.ReadFile(filepath.Join(layoutDir, "index.json"))
	if err != nil {
		return "", "", err
	}
	var idx struct {
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return "", "", err
	}
	if len(idx.Manifests) == 1 && idx.Manifests[0].Digest != "" {
		digest := idx.Manifests[0].Digest
		return safeDigest(digest), digest, nil
	}
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	return safeDigest(digest), digest, nil
}

func safeDigest(digest string) string {
	return strings.ReplaceAll(digest, ":", "-")
}

func cachePathForDigest(digest string) (string, error) {
	root, err := cacheRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "images", safeDigest(digest)), nil
}

func cachedLayoutForDigest(digest string) (string, bool, error) {
	dir, err := cachePathForDigest(digest)
	if err != nil {
		return "", false, err
	}
	if _, err := os.Stat(filepath.Join(dir, "oci-layout")); err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return dir, true, nil
}

func writeMetadata(dir string, metadata cacheMetadata) error {
	metadata.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, "olav-cache.json"), data, 0o644)
}

func moveIntoCache(tmpDir, sourceRef string, platform Platform) (string, string, error) {
	root, err := cacheRoot()
	if err != nil {
		return "", "", err
	}
	key, digest, err := cacheKeyFromLayout(tmpDir)
	if err != nil {
		return "", "", err
	}
	finalDir := filepath.Join(root, "images", key)
	if _, err := os.Stat(finalDir); err == nil {
		_ = os.RemoveAll(tmpDir)
		return finalDir, digest, nil
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(finalDir), 0o755); err != nil {
		return "", "", err
	}
	if err := writeMetadata(tmpDir, cacheMetadata{Source: sourceRef, Platform: platform.String(), Digest: digest}); err != nil {
		return "", "", err
	}
	if err := os.Rename(tmpDir, finalDir); err != nil {
		return "", "", fmt.Errorf("move image into cache: %w", err)
	}
	return finalDir, digest, nil
}
