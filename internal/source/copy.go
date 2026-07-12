package source

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"go.podman.io/image/v5/copy"
	"go.podman.io/image/v5/docker"
	dockerdaemon "go.podman.io/image/v5/docker/daemon"
	ociLayout "go.podman.io/image/v5/oci/layout"
	"go.podman.io/image/v5/signature"
	"go.podman.io/image/v5/types"
)

func copyToCache(ctx context.Context, sourceRef string, platform Platform, progress io.Writer) (*Resolved, error) {
	copySourceRef := sourceRef
	if strings.HasPrefix(sourceRef, "docker-daemon:") {
		var err error
		copySourceRef, err = normalizeDockerDaemonSource(ctx, sourceRef)
		if err != nil {
			return nil, err
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

	src, err := parseSourceReference(copySourceRef)
	if err != nil {
		return nil, err
	}
	dest, err := ociLayout.ParseReference(tmpDir + ":olav")
	if err != nil {
		return nil, err
	}
	policyContext, err := signature.NewPolicyContext(&signature.Policy{Default: []signature.PolicyRequirement{signature.NewPRInsecureAcceptAnything()}})
	if err != nil {
		return nil, err
	}
	defer policyContext.Destroy()

	progressCh := make(chan types.ProgressProperties)
	done := make(chan struct{})
	if progress != nil {
		go renderProgress(progress, progressCh, done)
	}
	copyOptions := &copy.Options{PreserveDigests: true}
	if progress != nil {
		copyOptions.Progress = progressCh
		copyOptions.ProgressInterval = 200 * time.Millisecond
	}
	if platform.All {
		copyOptions.ImageListSelection = copy.CopyAllImages
	} else if platform.OS != "" {
		copyOptions.ImageListSelection = copy.CopySystemImage
		copyOptions.SourceCtx = &types.SystemContext{OSChoice: platform.OS, ArchitectureChoice: platform.Architecture, VariantChoice: platform.Variant}
	}

	if progress != nil {
		fmt.Fprintf(progress, "olav: copying %s into cache...\n", sourceRef)
	}
	if _, err := copy.Image(ctx, policyContext, dest, src, copyOptions); err != nil {
		if progress != nil {
			close(progressCh)
			<-done
		}
		return nil, withAuthHint(sourceRef, err)
	}
	if progress != nil {
		close(progressCh)
		<-done
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

func renderProgress(w io.Writer, events <-chan types.ProgressProperties, done chan<- struct{}) {
	defer close(done)
	total := int64(0)
	seen := map[string]int64{}
	offsets := map[string]int64{}
	doneArtifacts := map[string]bool{}
	for event := range events {
		key := event.Artifact.Digest.String()
		if key == "" {
			key = event.Artifact.MediaType
		}
		size := event.Artifact.Size
		switch event.Event {
		case types.ProgressEventNewArtifact:
			if size > 0 {
				if _, ok := seen[key]; !ok {
					seen[key] = size
					total += size
				}
			}
		case types.ProgressEventRead:
			offsets[key] = int64(event.Offset)
		case types.ProgressEventDone, types.ProgressEventSkipped:
			if !doneArtifacts[key] {
				doneArtifacts[key] = true
			}
			if size > 0 {
				offsets[key] = size
			}
		}
		renderProgressLine(w, sumOffsets(offsets), total)
	}
	renderProgressLine(w, total, total)
	fmt.Fprintln(w)
}

func sumOffsets(offsets map[string]int64) int64 {
	var total int64
	for _, offset := range offsets {
		total += offset
	}
	return total
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

func parseSourceReference(sourceRef string) (types.ImageReference, error) {
	if strings.HasPrefix(sourceRef, "docker://") {
		return docker.ParseReference(strings.TrimPrefix(sourceRef, "docker:"))
	}
	if strings.HasPrefix(sourceRef, "docker-daemon:") {
		return dockerdaemon.ParseReference(strings.TrimPrefix(sourceRef, "docker-daemon:"))
	}
	return nil, fmt.Errorf("unsupported image source %q", sourceRef)
}

func withAuthHint(sourceRef string, err error) error {
	if !looksAuthError(err) {
		return err
	}
	return fmt.Errorf("%w\n\nAuthentication hint:\n  olav uses the default containers/image auth locations:\n    ~/.docker/config.json\n    ${XDG_RUNTIME_DIR}/containers/auth.json\n    ~/.config/containers/auth.json\n  Login with docker, podman, or skopeo before retrying %s.", err, sourceRef)
}

func looksAuthError(err error) bool {
	lower := strings.ToLower(err.Error())
	needles := []string{"unauthorized", "authentication required", "denied", "invalid username/password", "no basic auth credentials"}
	for _, needle := range needles {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}
