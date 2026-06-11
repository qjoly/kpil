package container

import "testing"

func TestDigestForImage(t *testing.T) {
	const wantedDigest = "sha256:aaaa"
	const otherDigest = "sha256:bbbb"

	tests := []struct {
		name        string
		img         string
		repoDigests []string
		want        string
	}{
		{
			name:        "empty list returns empty",
			img:         "ghcr.io/qjoly/kpil:latest",
			repoDigests: nil,
			want:        "",
		},
		{
			name:        "matches repo prefix",
			img:         "ghcr.io/qjoly/kpil:latest",
			repoDigests: []string{"ghcr.io/qjoly/kpil@" + wantedDigest},
			want:        wantedDigest,
		},
		{
			name:        "ignores tag when matching",
			img:         "ghcr.io/qjoly/kpil:nightly",
			repoDigests: []string{"ghcr.io/qjoly/kpil@" + wantedDigest},
			want:        wantedDigest,
		},
		{
			name:        "strips digest from img when matching",
			img:         "ghcr.io/qjoly/kpil@sha256:cccc",
			repoDigests: []string{"ghcr.io/qjoly/kpil@" + wantedDigest},
			want:        wantedDigest,
		},
		{
			name: "picks the right repo when multiple are present",
			img:  "ghcr.io/qjoly/kpil:latest",
			repoDigests: []string{
				"docker.io/library/other@" + otherDigest,
				"ghcr.io/qjoly/kpil@" + wantedDigest,
			},
			want: wantedDigest,
		},
		{
			name: "returns empty when nothing matches (A5 fix)",
			img:  "internal-mirror.example/kpil:dev",
			repoDigests: []string{
				"ghcr.io/qjoly/kpil@" + otherDigest,
			},
			want: "",
		},
		{
			name:        "handles registry:port in img",
			img:         "localhost:5000/foo:tag",
			repoDigests: []string{"localhost:5000/foo@" + wantedDigest},
			want:        wantedDigest,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := digestForImage(tc.img, tc.repoDigests)
			if got != tc.want {
				t.Fatalf("digestForImage(%q, %v) = %q, want %q", tc.img, tc.repoDigests, got, tc.want)
			}
		})
	}
}

func TestParseImageRef(t *testing.T) {
	tests := []struct {
		img      string
		registry string
		repo     string
		ref      string
	}{
		{"ghcr.io/qjoly/kpil:latest", "ghcr.io", "qjoly/kpil", "latest"},
		{"ghcr.io/qjoly/kpil:nightly", "ghcr.io", "qjoly/kpil", "nightly"},
		{"ghcr.io/qjoly/kpil@sha256:abc", "ghcr.io", "qjoly/kpil", "sha256:abc"},
		{"localhost:5000/foo:tag", "localhost:5000", "foo", "tag"},
		{"localhost:5000/foo", "localhost:5000", "foo", "latest"},
		{"alpine", "registry-1.docker.io", "library/alpine", "latest"},
		{"library/alpine:3.18", "registry-1.docker.io", "library/alpine", "3.18"},
	}
	for _, tc := range tests {
		t.Run(tc.img, func(t *testing.T) {
			registry, repo, ref := parseImageRef(tc.img)
			if registry != tc.registry || repo != tc.repo || ref != tc.ref {
				t.Fatalf("parseImageRef(%q) = (%q, %q, %q), want (%q, %q, %q)",
					tc.img, registry, repo, ref, tc.registry, tc.repo, tc.ref)
			}
		})
	}
}

func TestIsImageNotFoundStderr(t *testing.T) {
	tests := []struct {
		stderr string
		want   bool
	}{
		{"Error: No such image: ghcr.io/qjoly/kpil:nightly", true},
		{"Error response from daemon: no such image: foo", true},
		{"Error: image not known: foo", true},
		{"manifest unknown", true},
		{"Error response from daemon: Get \"http://...\": dial tcp: connect: connection refused", false},
		{"permission denied while trying to connect to the Docker daemon socket", false},
		{"", false},
	}
	for _, tc := range tests {
		t.Run(tc.stderr, func(t *testing.T) {
			got := isImageNotFoundStderr(tc.stderr)
			if got != tc.want {
				t.Fatalf("isImageNotFoundStderr(%q) = %v, want %v", tc.stderr, got, tc.want)
			}
		})
	}
}
