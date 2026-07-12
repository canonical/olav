package source

import (
	"context"
	"fmt"
	"strings"

	imageTypes "github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
)

func resolveDaemonRepoDigest(ctx context.Context, ref string) (string, error) {
	cli, err := client.New(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", fmt.Errorf("initialize docker client: %w", err)
	}
	defer cli.Close()
	images, err := cli.ImageList(ctx, client.ImageListOptions{All: true})
	if err != nil {
		return "", fmt.Errorf("list docker daemon images: %w", err)
	}
	imageRef, ok := findImageRefByRepoDigest(images.Items, ref)
	if !ok {
		return "", fmt.Errorf("docker daemon image with RepoDigest %s was not found; pull it with `docker pull %s` before retrying", ref, ref)
	}
	return imageRef, nil
}

func isNamedDigestRef(ref string) bool {
	name, digest, ok := strings.Cut(ref, "@")
	return ok && name != "" && strings.HasPrefix(digest, "sha256:")
}

func findImageIDByRepoDigest(images []imageTypes.Summary, repoDigest string) (string, bool) {
	return findImageRefByRepoDigest(images, repoDigest)
}

func findImageRefByRepoDigest(images []imageTypes.Summary, repoDigest string) (string, bool) {
	for _, img := range images {
		for _, digest := range img.RepoDigests {
			if digest == repoDigest {
				if len(img.RepoTags) > 0 {
					return img.RepoTags[0], true
				}
				return img.ID, true
			}
		}
	}
	return "", false
}
