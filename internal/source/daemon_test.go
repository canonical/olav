package source

import (
	"testing"

	imageTypes "github.com/moby/moby/api/types/image"
)

func TestIsNamedDigestRef(t *testing.T) {
	if !isNamedDigestRef("repo/image@sha256:abc") {
		t.Fatal("expected named digest ref")
	}
	if isNamedDigestRef("sha256:abc") {
		t.Fatal("image ID digest should not be treated as named digest ref")
	}
	if isNamedDigestRef("repo/image:tag") {
		t.Fatal("tag should not be treated as named digest ref")
	}
}

func TestFindImageIDByRepoDigest(t *testing.T) {
	images := []imageTypes.Summary{
		{ID: "sha256:config-a", RepoDigests: []string{"example.com/a@sha256:aaa"}},
		{ID: "sha256:config-b", RepoDigests: []string{"example.com/b@sha256:bbb", "example.com/b@sha256:ccc"}},
	}
	got, ok := findImageIDByRepoDigest(images, "example.com/b@sha256:ccc")
	if !ok || got != "sha256:config-b" {
		t.Fatalf("got id=%q ok=%v", got, ok)
	}
	if _, ok := findImageIDByRepoDigest(images, "example.com/missing@sha256:ddd"); ok {
		t.Fatal("did not expect missing digest match")
	}
}
