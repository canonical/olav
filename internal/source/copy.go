package source

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/daemon"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func copyToCache(ctx context.Context, sourceRef string, platform Platform, progress io.Writer) (*Resolved, error) {
	if strings.HasPrefix(sourceRef, "docker://") {
		digest, err := resolveRemoteCacheDigest(ctx, sourceRef, platform)
		if err != nil {
			return nil, withAuthHint(sourceRef, err)
		}
		if cached, ok, err := cachedLayoutForDigest(digest); err != nil {
			return nil, err
		} else if ok {
			if progress != nil {
				fmt.Fprintf(progress, "olav: using cached %s from %s\n", sourceRef, cached)
			}
			return &Resolved{DisplayName: sourceRef, LocalPath: cached, Cached: true}, nil
		}
	}
	tmpDir, err := makeTempLayoutDir()
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	if progress != nil {
		fmt.Fprintf(progress, "olav: resolving %s...\n", sourceRef)
	}

	var writeErr error
	switch {
	case strings.HasPrefix(sourceRef, "docker://"):
		writeErr = writeRemoteLayout(ctx, tmpDir, sourceRef, platform, progress)
	case strings.HasPrefix(sourceRef, "docker-daemon:"):
		writeErr = writeDaemonLayout(ctx, tmpDir, sourceRef, progress)
	default:
		writeErr = fmt.Errorf("unsupported image source %q", sourceRef)
	}
	if writeErr != nil {
		return nil, withAuthHint(sourceRef, writeErr)
	}

	finalDir, digest, err := moveIntoCache(tmpDir, sourceRef, platform)
	if err != nil {
		return nil, err
	}
	cleanup = false
	if progress != nil {
		fmt.Fprintf(progress, "olav: cached %s as %s (%s)\n", sourceRef, finalDir, digest)
	}
	return &Resolved{DisplayName: sourceRef, LocalPath: finalDir, Cached: true}, nil
}

func resolveRemoteCacheDigest(ctx context.Context, sourceRef string, platform Platform) (string, error) {
	refString := strings.TrimPrefix(sourceRef, "docker://")
	ref, err := name.ParseReference(refString)
	if err != nil {
		return "", err
	}
	options := []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(authKeychain()),
	}
	if !platform.All {
		options = append(options, remote.WithPlatform(v1.Platform{OS: platform.OS, Architecture: platform.Architecture, Variant: platform.Variant}))
	}
	desc, err := remote.Get(ref, options...)
	if err != nil {
		return "", err
	}
	if platform.All || !desc.MediaType.IsIndex() {
		return desc.Digest.String(), nil
	}
	img, err := desc.Image()
	if err != nil {
		return "", err
	}
	digest, err := img.Digest()
	if err != nil {
		return "", err
	}
	return digest.String(), nil
}

func writeRemoteLayout(ctx context.Context, dir, sourceRef string, platform Platform, progress io.Writer) error {
	refString := strings.TrimPrefix(sourceRef, "docker://")
	ref, err := name.ParseReference(refString)
	if err != nil {
		return err
	}
	options := []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(authKeychain()),
	}
	if !platform.All {
		options = append(options, remote.WithPlatform(v1.Platform{OS: platform.OS, Architecture: platform.Architecture, Variant: platform.Variant}))
	}
	if progress != nil {
		fmt.Fprintf(progress, "olav: pulling %s...\n", sourceRef)
	}
	path, err := layout.Write(dir, empty.Index)
	if err != nil {
		return err
	}
	if platform.All {
		desc, err := remote.Get(ref, options...)
		if err != nil {
			return err
		}
		if desc.MediaType.IsIndex() {
			idx, err := desc.ImageIndex()
			if err != nil {
				return err
			}
			if progress != nil {
				fmt.Fprintln(progress, "olav: writing OCI image index...")
			}
			return path.AppendIndex(idx, layout.WithAnnotations(map[string]string{"org.opencontainers.image.ref.name": "olav"}))
		}
		img, err := desc.Image()
		if err != nil {
			return err
		}
		if progress != nil {
			fmt.Fprintln(progress, "olav: writing OCI image...")
		}
		return path.AppendImage(img, layout.WithAnnotations(map[string]string{"org.opencontainers.image.ref.name": "olav"}))
	}
	img, err := remote.Image(ref, options...)
	if err != nil {
		return err
	}
	if progress != nil {
		fmt.Fprintln(progress, "olav: writing OCI image...")
	}
	return path.AppendImage(img, layout.WithAnnotations(map[string]string{"org.opencontainers.image.ref.name": "olav"}))
}

func writeDaemonLayout(ctx context.Context, dir, sourceRef string, progress io.Writer) error {
	refString := strings.TrimPrefix(sourceRef, "docker-daemon:")
	if isNamedDigestRef(refString) {
		imageID, err := resolveDaemonRepoDigest(ctx, refString)
		if err != nil {
			return err
		}
		refString = imageID
	}
	ref, err := name.ParseReference(refString)
	if err != nil {
		return err
	}
	if progress != nil {
		fmt.Fprintf(progress, "olav: reading %s from docker daemon...\n", sourceRef)
	}
	img, err := daemon.Image(ref, daemon.WithContext(ctx))
	if err != nil {
		return err
	}
	path, err := layout.Write(dir, empty.Index)
	if err != nil {
		return err
	}
	if progress != nil {
		fmt.Fprintln(progress, "olav: writing OCI image...")
	}
	return path.AppendImage(img, layout.WithAnnotations(map[string]string{"org.opencontainers.image.ref.name": "olav"}))
}

func withAuthHint(sourceRef string, err error) error {
	if !looksAuthError(err) {
		return err
	}
	return fmt.Errorf("%w\n\nAuthentication hint:\n  olav uses the default go-containerregistry auth locations:\n    ~/.docker/config.json\n    $DOCKER_CONFIG/config.json\n    $REGISTRY_AUTH_FILE\n    ${XDG_RUNTIME_DIR}/containers/auth.json\n    ~/.config/containers/auth.json\n  Login with docker, podman, or skopeo before retrying %s.", err, sourceRef)
}

func looksAuthError(err error) bool {
	lower := strings.ToLower(err.Error())
	needles := []string{"unauthorized", "authentication required", "denied", "invalid username/password", "no basic auth credentials", "forbidden"}
	for _, needle := range needles {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func authKeychain() authn.Keychain {
	return authn.NewMultiKeychain(authn.DefaultKeychain, containersAuthKeychain{})
}

type containersAuthKeychain struct{}

func (containersAuthKeychain) Resolve(target authn.Resource) (authn.Authenticator, error) {
	paths := containersAuthPaths()
	for _, p := range paths {
		auth, ok, err := resolveAuthFromFile(p, target)
		if err != nil {
			return nil, err
		}
		if ok {
			return auth, nil
		}
	}
	return authn.Anonymous, nil
}

func containersAuthPaths() []string {
	var paths []string
	if p := os.Getenv("REGISTRY_AUTH_FILE"); p != "" {
		paths = append(paths, p)
	}
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "containers", "auth.json"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "containers", "auth.json"))
	}
	return paths
}

type authFile struct {
	Auths map[string]authEntry `json:"auths"`
}

type authEntry struct {
	Auth          string `json:"auth"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	IdentityToken string `json:"identitytoken"`
}

func resolveAuthFromFile(path string, target authn.Resource) (authn.Authenticator, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var f authFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, false, err
	}
	for _, key := range authLookupKeys(target) {
		entry, ok := f.Auths[key]
		if !ok {
			continue
		}
		cfg, err := entry.authConfig()
		if err != nil {
			return nil, false, err
		}
		return authn.FromConfig(cfg), true, nil
	}
	return nil, false, nil
}

func authLookupKeys(target authn.Resource) []string {
	registry := target.RegistryStr()
	return []string{registry, "https://" + registry, "http://" + registry, target.String()}
}

func (e authEntry) authConfig() (authn.AuthConfig, error) {
	cfg := authn.AuthConfig{Username: e.Username, Password: e.Password, IdentityToken: e.IdentityToken}
	if e.Auth == "" {
		return cfg, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(e.Auth)
	if err != nil {
		return cfg, err
	}
	username, password, ok := strings.Cut(string(decoded), ":")
	if !ok {
		return cfg, nil
	}
	cfg.Username = username
	cfg.Password = password
	return cfg, nil
}

func renderProgressLine(w io.Writer, complete, total int64) {
	const width = 24
	if total <= 0 {
		fmt.Fprintf(w, "\rolav: copying image blobs...")
		return
	}
	if complete > total {
		complete = total
	}
	filled := int(float64(complete) / float64(total) * width)
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("=", filled) + strings.Repeat(" ", width-filled)
	percent := int(float64(complete) / float64(total) * 100)
	fmt.Fprintf(w, "\rolav: [%s] %3d%%", bar, percent)
}
