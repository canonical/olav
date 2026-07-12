package source

import (
	"context"
	"fmt"
	"strings"

	imageTypes "github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
)

func normalizeDockerDaemonSource(ctx context.Context, sourceRef string) (string, error) {
	ref := strings.TrimPrefix(sourceRef, "docker-daemon:")
	if !isNamedDigestRef(ref) {
		return sourceRef, nil
	}
	cli, err := client.New(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", fmt.Errorf("initialize docker client: %w", err)
	}
	defer cli.Close()
	images, err := cli.ImageList(ctx, client.ImageListOptions{All: true})
	if err != nil {
		return "", fmt.Errorf("list docker daemon images: %w", err)
	}
	imageID, ok := findImageIDByRepoDigest(images.Items, ref)
	if !ok {
		return "", fmt.Errorf("docker daemon image with RepoDigest %s was not found; pull it with `docker pull %s` before retrying", ref, ref)
	}
	return "docker-daemon:" + imageID, nil
}

func isNamedDigestRef(ref string) bool {
	name, digest, ok := strings.Cut(ref, "@")
	return ok && name != "" && strings.HasPrefix(digest, "sha256:")
}

func findImageIDByRepoDigest(images []imageTypes.Summary, repoDigest string) (string, bool) {
	for _, img := range images {
		for _, digest := range img.RepoDigests {
			if digest == repoDigest {
				return img.ID, true
			}
		}
	}
	return "", false
}
