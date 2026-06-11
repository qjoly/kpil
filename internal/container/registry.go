package container

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// acceptManifests lists every media type a v2 registry might serve for a tag.
// Sent on every manifest request so the registry returns the index/list digest
// rather than redirecting to a per-platform manifest.
var acceptManifests = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

// fetchRegistryDigest issues a HEAD on the registry's manifest endpoint and
// returns the Docker-Content-Digest header value — i.e. the same digest that
// `docker pull` records in RepoDigests. Handles anonymous bearer-token auth
// for registries like ghcr.io and docker.io.
//
// Returns "" with no error when the registry omits the digest header (some
// older or non-conformant registries do), so the caller can skip silently.
func fetchRegistryDigest(ctx context.Context, img string) (string, error) {
	registry, repo, ref := parseImageRef(img)
	url := fmt.Sprintf("https://%s/v2/%s/manifests/%s", registry, repo, ref)

	digest, err := manifestHead(ctx, url, "")
	if err == nil {
		return digest, nil
	}

	// 401 → try anonymous token, then retry once.
	authErr, ok := err.(*authChallenge)
	if !ok {
		return "", err
	}
	token, err := fetchBearerToken(ctx, authErr.header, repo)
	if err != nil {
		return "", fmt.Errorf("registry auth failed: %w", err)
	}
	return manifestHead(ctx, url, token)
}

type authChallenge struct {
	header string
}

func (e *authChallenge) Error() string { return "registry requires auth: " + e.header }

func manifestHead(ctx context.Context, url, bearer string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", acceptManifests)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", &authChallenge{header: resp.Header.Get("Www-Authenticate")}
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry returned %s for %s", resp.Status, url)
	}
	return resp.Header.Get("Docker-Content-Digest"), nil
}

// parseImageRef splits "registry/repo:tag" or "registry/repo@digest" into its
// three components. Docker Hub short forms ("alpine", "library/alpine:3.18")
// are normalized to registry-1.docker.io with the library/ prefix.
func parseImageRef(img string) (registry, repo, ref string) {
	if i := strings.Index(img, "@"); i >= 0 {
		ref = img[i+1:]
		img = img[:i]
	}

	tag := "latest"
	if i := strings.LastIndex(img, ":"); i > strings.LastIndex(img, "/") {
		tag = img[i+1:]
		img = img[:i]
	}
	if ref == "" {
		ref = tag
	}

	parts := strings.SplitN(img, "/", 2)
	first := parts[0]
	hasRegistry := len(parts) == 2 && (strings.ContainsAny(first, ".:") || first == "localhost")
	if !hasRegistry {
		registry = "registry-1.docker.io"
		repo = img
		if !strings.Contains(repo, "/") {
			repo = "library/" + repo
		}
		return
	}
	return first, parts[1], ref
}

func fetchBearerToken(ctx context.Context, authHeader, repo string) (string, error) {
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", fmt.Errorf("unsupported auth scheme: %q", authHeader)
	}
	params := map[string]string{}
	for _, kv := range strings.Split(authHeader[len("Bearer "):], ",") {
		kv = strings.TrimSpace(kv)
		i := strings.Index(kv, "=")
		if i < 0 {
			continue
		}
		params[kv[:i]] = strings.Trim(kv[i+1:], `"`)
	}
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("no realm in WWW-Authenticate")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm, nil)
	if err != nil {
		return "", err
	}
	q := req.URL.Query()
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	// The scope param in the challenge is often a placeholder (GHCR returns
	// "repository:user/image:pull"). Always pin it to the repo we actually
	// want to read so the returned token authorizes our request.
	q.Set("scope", "repository:"+repo+":pull")
	req.URL.RawQuery = q.Encode()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %s", resp.Status)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Token != "" {
		return body.Token, nil
	}
	return body.AccessToken, nil
}
