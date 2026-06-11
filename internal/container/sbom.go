package container

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// agentSBOMPackages maps the canonical agent identifier surfaced by kpil
// (claude/opencode) to the npm package name that syft records in the SBOM.
// Copilot CLI is excluded on purpose — it's a raw binary download that syft's
// npm/dpkg catalogers do not see, so we surface its version through an OCI
// LABEL on the image config instead.
var agentSBOMPackages = map[string]string{
	"claude":   "@anthropic-ai/claude-code",
	"opencode": "opencode-ai",
}

// agentLabels maps agent identifiers to the OCI label keys kpil bakes into
// the image. Currently only copilot uses this channel.
var agentLabels = map[string]string{
	"copilot": "org.kpil.copilot.version",
}

// FetchAgentVersions returns a map of agent identifier → version string for
// the image identified by img@digest. The lookup is driven entirely by the
// registry: it walks the OCI Referrers index for any attached SPDX SBOM
// artifacts (claude, opencode) and reads the OCI image config blob for the
// copilot LABEL. No image layers are downloaded.
//
// An empty map means the registry returned no usable metadata — callers
// should treat that as "unknown versions, display nothing", not a hard
// failure. The function never returns an error: registry weirdness is
// quietly absorbed so the caller's UX (a Y/n pull prompt) is never blocked
// by metadata lookups.
func FetchAgentVersions(ctx context.Context, img, digest string) map[string]string {
	out := map[string]string{}
	if digest == "" {
		return out
	}
	registry, repo, _ := parseImageRef(img)
	token, err := authForRepo(ctx, registry, repo)
	if err != nil {
		return out
	}

	if labels, err := fetchConfigLabels(ctx, registry, repo, digest, token); err == nil {
		for agent, key := range agentLabels {
			if v := labels[key]; v != "" {
				out[agent] = v
			}
		}
	}

	if versions, err := fetchSBOMPackageVersions(ctx, registry, repo, digest, token); err == nil {
		for agent, pkg := range agentSBOMPackages {
			if v := versions[pkg]; v != "" {
				out[agent] = v
			}
		}
	}

	return out
}

// authForRepo returns a bearer token suitable for talking to registry/repo, or
// "" if the registry allows anonymous read of public manifests.
func authForRepo(ctx context.Context, registry, repo string) (string, error) {
	probe := fmt.Sprintf("https://%s/v2/", registry)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probe, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return "", nil
	}
	return fetchBearerToken(ctx, resp.Header.Get("Www-Authenticate"), repo)
}

// fetchConfigLabels resolves the manifest at digest, walks through to the
// image config blob, and returns its labels map.
//
// For multi-arch images the top-level manifest is an index — we follow the
// first manifest descriptor in the list (the linux platform our Dockerfile
// builds for) and fetch its config from there.
func fetchConfigLabels(ctx context.Context, registry, repo, digest, token string) (map[string]string, error) {
	manifest, mediaType, err := fetchManifest(ctx, registry, repo, digest, token)
	if err != nil {
		return nil, err
	}

	if isIndexMediaType(mediaType) {
		var idx struct {
			Manifests []struct {
				Digest    string `json:"digest"`
				MediaType string `json:"mediaType"`
				Platform  struct {
					OS           string `json:"os"`
					Architecture string `json:"architecture"`
				} `json:"platform"`
			} `json:"manifests"`
		}
		if err := json.Unmarshal(manifest, &idx); err != nil {
			return nil, fmt.Errorf("parsing image index: %w", err)
		}
		var pick string
		for _, m := range idx.Manifests {
			if m.Platform.OS == "linux" && pick == "" {
				pick = m.Digest
				break
			}
		}
		if pick == "" {
			return nil, fmt.Errorf("no linux manifest in image index")
		}
		return fetchConfigLabels(ctx, registry, repo, pick, token)
	}

	var mf struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
	}
	if err := json.Unmarshal(manifest, &mf); err != nil {
		return nil, fmt.Errorf("parsing image manifest: %w", err)
	}
	if mf.Config.Digest == "" {
		return nil, fmt.Errorf("manifest has no config descriptor")
	}

	blob, err := fetchBlob(ctx, registry, repo, mf.Config.Digest, token)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
	}
	if err := json.Unmarshal(blob, &cfg); err != nil {
		return nil, fmt.Errorf("parsing image config: %w", err)
	}
	return cfg.Config.Labels, nil
}

// fetchSBOMPackageVersions returns a map of npm-style package name → version
// extracted from the SPDX SBOM attached to the image at digest.
//
// Buildx with `sbom: true` does not push the SBOM via the OCI Referrers API
// (which GHCR doesn't fully support — it 303-redirects). Instead, buildx
// embeds an "attestation-manifest" inside the image index, one per platform,
// referencing back to the platform image via the
// `vnd.docker.reference.digest` annotation. Each attestation-manifest's
// layers are in-toto statements; the SPDX SBOM lives in the layer whose
// `in-toto.io/predicate-type` annotation is `https://spdx.dev/Document`,
// with the SPDX document carried inline in the statement's `predicate`
// field.
//
// We optimistically try the OCI Referrers API first (works on registries
// that support it) and fall back to scanning the image index for the
// attestation-manifest.
func fetchSBOMPackageVersions(ctx context.Context, registry, repo, digest, token string) (map[string]string, error) {
	if out := tryReferrerSBOM(ctx, registry, repo, digest, token); len(out) > 0 {
		return out, nil
	}
	return fetchAttestationSBOM(ctx, registry, repo, digest, token)
}

// tryReferrerSBOM probes the OCI Referrers API. Returns nil silently when
// the registry doesn't expose referrers or none are SBOMs.
func tryReferrerSBOM(ctx context.Context, registry, repo, digest, token string) map[string]string {
	descriptors, err := fetchReferrers(ctx, registry, repo, digest, token)
	if err != nil || len(descriptors) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, ref := range descriptors {
		sbomManifest, _, err := fetchManifest(ctx, registry, repo, ref.Digest, token)
		if err != nil {
			continue
		}
		var mf struct {
			ArtifactType string `json:"artifactType"`
			Layers       []struct {
				Digest    string `json:"digest"`
				MediaType string `json:"mediaType"`
			} `json:"layers"`
		}
		if err := json.Unmarshal(sbomManifest, &mf); err != nil {
			continue
		}
		if !isSBOMArtifactType(ref.ArtifactType) && !isSBOMArtifactType(mf.ArtifactType) {
			continue
		}
		for _, layer := range mf.Layers {
			if !isSPDXMediaType(layer.MediaType) {
				continue
			}
			if blob, err := fetchBlob(ctx, registry, repo, layer.Digest, token); err == nil {
				extractSPDXVersions(blob, out)
			}
		}
	}
	return out
}

// fetchAttestationSBOM extracts the SPDX SBOM that buildx embeds inside the
// image index as an attestation-manifest (in-toto statement wrapping SPDX).
func fetchAttestationSBOM(ctx context.Context, registry, repo, digest, token string) (map[string]string, error) {
	indexBody, mediaType, err := fetchManifest(ctx, registry, repo, digest, token)
	if err != nil {
		return nil, err
	}
	if !isIndexMediaType(mediaType) {
		// A single-arch image with no index: buildx attaches no SBOM here.
		return nil, nil
	}

	var idx struct {
		Manifests []struct {
			Digest      string            `json:"digest"`
			Annotations map[string]string `json:"annotations"`
			Platform    struct {
				OS string `json:"os"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(indexBody, &idx); err != nil {
		return nil, fmt.Errorf("parsing image index: %w", err)
	}

	var linuxImageDigest string
	for _, m := range idx.Manifests {
		if m.Platform.OS == "linux" && m.Annotations["vnd.docker.reference.type"] == "" {
			linuxImageDigest = m.Digest
			break
		}
	}
	if linuxImageDigest == "" {
		return nil, nil
	}
	var attestDigest string
	for _, m := range idx.Manifests {
		if m.Annotations["vnd.docker.reference.type"] == "attestation-manifest" &&
			m.Annotations["vnd.docker.reference.digest"] == linuxImageDigest {
			attestDigest = m.Digest
			break
		}
	}
	if attestDigest == "" {
		return nil, nil
	}

	attestBody, _, err := fetchManifest(ctx, registry, repo, attestDigest, token)
	if err != nil {
		return nil, err
	}
	var attest struct {
		Layers []struct {
			MediaType   string            `json:"mediaType"`
			Digest      string            `json:"digest"`
			Annotations map[string]string `json:"annotations"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(attestBody, &attest); err != nil {
		return nil, fmt.Errorf("parsing attestation manifest: %w", err)
	}

	out := map[string]string{}
	for _, layer := range attest.Layers {
		if layer.Annotations["in-toto.io/predicate-type"] != "https://spdx.dev/Document" {
			continue
		}
		blob, err := fetchBlob(ctx, registry, repo, layer.Digest, token)
		if err != nil {
			continue
		}
		var stmt struct {
			Predicate json.RawMessage `json:"predicate"`
		}
		if err := json.Unmarshal(blob, &stmt); err != nil {
			continue
		}
		extractSPDXVersions(stmt.Predicate, out)
	}
	return out, nil
}

type referrerDescriptor struct {
	Digest       string `json:"digest"`
	MediaType    string `json:"mediaType"`
	ArtifactType string `json:"artifactType"`
}

func fetchReferrers(ctx context.Context, registry, repo, digest, token string) ([]referrerDescriptor, error) {
	url := fmt.Sprintf("https://%s/v2/%s/referrers/%s", registry, repo, digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("referrers %s: %s", url, resp.Status)
	}
	var idx struct {
		Manifests []referrerDescriptor `json:"manifests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&idx); err != nil {
		return nil, fmt.Errorf("parsing referrers: %w", err)
	}
	return idx.Manifests, nil
}

func fetchManifest(ctx context.Context, registry, repo, ref, token string) ([]byte, string, error) {
	url := fmt.Sprintf("https://%s/v2/%s/manifests/%s", registry, repo, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("manifest %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Content-Type"), nil
}

func fetchBlob(ctx context.Context, registry, repo, digest, token string) ([]byte, error) {
	url := fmt.Sprintf("https://%s/v2/%s/blobs/%s", registry, repo, digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("blob %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// extractSPDXVersions populates out with name→version for every package in
// the SPDX JSON document. Only the minimal subset of SPDX is read.
func extractSPDXVersions(spdxJSON []byte, out map[string]string) {
	var doc struct {
		Packages []struct {
			Name        string `json:"name"`
			VersionInfo string `json:"versionInfo"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(spdxJSON, &doc); err != nil {
		return
	}
	for _, p := range doc.Packages {
		if p.Name == "" || p.VersionInfo == "" {
			continue
		}
		out[p.Name] = p.VersionInfo
	}
}

func isIndexMediaType(mt string) bool {
	mt = strings.SplitN(mt, ";", 2)[0]
	return mt == "application/vnd.oci.image.index.v1+json" ||
		mt == "application/vnd.docker.distribution.manifest.list.v2+json"
}

func isSBOMArtifactType(mt string) bool {
	mt = strings.SplitN(mt, ";", 2)[0]
	return strings.Contains(mt, "spdx") || strings.Contains(mt, "cyclonedx") ||
		mt == "application/vnd.in-toto+json"
}

func isSPDXMediaType(mt string) bool {
	mt = strings.SplitN(mt, ";", 2)[0]
	return strings.Contains(mt, "spdx") || mt == "application/json"
}
