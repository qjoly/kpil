package container

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mockRegistry serves a minimal subset of the v2 distribution spec sufficient
// to exercise fetchRegistryDigest, fetchConfigLabels, fetchAttestationSBOM,
// SameImage, and FetchAgentVersions against an httptest.Server.
type mockRegistry struct {
	t          *testing.T
	indexDigest string
	indexBody   []byte
	manifests  map[string][]byte // digest → raw manifest body
	blobs      map[string][]byte // digest → raw blob body
	mediaTypes map[string]string // digest → Content-Type for HEAD/GET responses
	requestLog []string
}

func newMockRegistry(t *testing.T) *mockRegistry {
	return &mockRegistry{
		t:          t,
		manifests:  map[string][]byte{},
		blobs:      map[string][]byte{},
		mediaTypes: map[string]string{},
	}
}

func (m *mockRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.requestLog = append(m.requestLog, r.Method+" "+r.URL.Path)
	switch {
	case r.URL.Path == "/v2/":
		w.WriteHeader(http.StatusOK)
	case strings.HasPrefix(r.URL.Path, "/v2/") && strings.Contains(r.URL.Path, "/manifests/"):
		ref := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		body, ok := m.manifests[ref]
		if !ok && ref == "latest" {
			body = m.indexBody
			ok = body != nil
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", m.mediaTypes[ref])
		w.Header().Set("Docker-Content-Digest", m.digestFor(ref))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	case strings.HasPrefix(r.URL.Path, "/v2/") && strings.Contains(r.URL.Path, "/blobs/"):
		ref := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		body, ok := m.blobs[ref]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	default:
		http.NotFound(w, r)
	}
}

func (m *mockRegistry) digestFor(ref string) string {
	if ref == "latest" {
		return m.indexDigest
	}
	return ref
}

// buildBuildxStyleImage wires a multi-arch image with an in-toto/SPDX SBOM
// attestation-manifest, mirroring what buildx push --sbom=true produces on
// GHCR. Returns the index digest.
func (m *mockRegistry) buildBuildxStyleImage(copilotVersion string, sbomPackages map[string]string) string {
	// Image config blob (carries the copilot LABEL).
	cfg := map[string]any{
		"config": map[string]any{
			"Labels": map[string]string{
				"org.kpil.copilot.version": copilotVersion,
			},
		},
	}
	cfgBlob, _ := json.Marshal(cfg)
	const cfgDigest = "sha256:config000"
	m.blobs[cfgDigest] = cfgBlob

	// linux/amd64 image manifest.
	imageManifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest":    cfgDigest,
			"size":      len(cfgBlob),
		},
	}
	imageBody, _ := json.Marshal(imageManifest)
	const imageDigest = "sha256:image0000amd64"
	m.manifests[imageDigest] = imageBody
	m.mediaTypes[imageDigest] = "application/vnd.oci.image.manifest.v1+json"

	// Build an SPDX in-toto statement carrying the requested packages.
	var spdxPkgs []map[string]string
	for name, ver := range sbomPackages {
		spdxPkgs = append(spdxPkgs, map[string]string{
			"name":        name,
			"versionInfo": ver,
		})
	}
	statement := map[string]any{
		"_type":         "https://in-toto.io/Statement/v0.1",
		"predicateType": "https://spdx.dev/Document",
		"predicate": map[string]any{
			"spdxVersion": "SPDX-2.3",
			"packages":    spdxPkgs,
		},
	}
	statementBlob, _ := json.Marshal(statement)
	const statementDigest = "sha256:spdx0000statement"
	m.blobs[statementDigest] = statementBlob

	// Attestation manifest referencing the linux/amd64 image.
	attestManifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"layers": []map[string]any{
			{
				"mediaType": "application/vnd.in-toto+json",
				"digest":    statementDigest,
				"size":      len(statementBlob),
				"annotations": map[string]string{
					"in-toto.io/predicate-type": "https://spdx.dev/Document",
				},
			},
		},
	}
	attestBody, _ := json.Marshal(attestManifest)
	const attestDigest = "sha256:attest0000amd64"
	m.manifests[attestDigest] = attestBody
	m.mediaTypes[attestDigest] = "application/vnd.oci.image.manifest.v1+json"

	// Top-level OCI image index that ties them together.
	index := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests": []map[string]any{
			{
				"mediaType": "application/vnd.oci.image.manifest.v1+json",
				"digest":    imageDigest,
				"platform":  map[string]string{"os": "linux", "architecture": "amd64"},
			},
			{
				"mediaType": "application/vnd.oci.image.manifest.v1+json",
				"digest":    attestDigest,
				"platform":  map[string]string{"os": "unknown", "architecture": "unknown"},
				"annotations": map[string]string{
					"vnd.docker.reference.type":   "attestation-manifest",
					"vnd.docker.reference.digest": imageDigest,
				},
			},
		},
	}
	indexBody, _ := json.Marshal(index)
	indexDigest := "sha256:index00000nightly"
	m.indexBody = indexBody
	m.indexDigest = indexDigest
	m.manifests[indexDigest] = indexBody
	m.mediaTypes[indexDigest] = "application/vnd.oci.image.index.v1+json"
	m.mediaTypes["latest"] = "application/vnd.oci.image.index.v1+json"
	return indexDigest
}

func startMockRegistry(t *testing.T) (*mockRegistry, string) {
	t.Helper()
	reg := newMockRegistry(t)
	srv := httptest.NewTLSServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "https://")
	// Allow the test's TLS roundtripper to skip cert verification.
	old := http.DefaultTransport
	http.DefaultTransport = srv.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = old })
	return reg, host
}

func TestFetchAgentVersions_AgainstMockRegistry(t *testing.T) {
	reg, host := startMockRegistry(t)
	indexDigest := reg.buildBuildxStyleImage(
		"1.0.35",
		map[string]string{
			"@anthropic-ai/claude-code": "2.1.173",
			"opencode-ai":               "1.17.3",
		},
	)
	img := host + "/qjoly/kpil:latest"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	versions := FetchAgentVersions(ctx, img, indexDigest)
	if versions["copilot"] != "1.0.35" {
		t.Errorf("copilot version: got %q, want %q", versions["copilot"], "1.0.35")
	}
	if versions["claude"] != "2.1.173" {
		t.Errorf("claude version: got %q, want %q", versions["claude"], "2.1.173")
	}
	if versions["opencode"] != "1.17.3" {
		t.Errorf("opencode version: got %q, want %q", versions["opencode"], "1.17.3")
	}
}

func TestSameImage_IndexAndPerPlatformChild(t *testing.T) {
	reg, host := startMockRegistry(t)
	indexDigest := reg.buildBuildxStyleImage("1.0.35", map[string]string{
		"@anthropic-ai/claude-code": "2.1.173",
	})
	img := host + "/qjoly/kpil:latest"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The platform child of the index — should be reported as equivalent.
	const platformChild = "sha256:image0000amd64"
	if !SameImage(ctx, img, platformChild, indexDigest) {
		t.Errorf("SameImage(local=child, remote=index) returned false; want true")
	}
	if !SameImage(ctx, img, indexDigest, platformChild) {
		t.Errorf("SameImage(local=index, remote=child) returned false; want true")
	}

	// An unrelated digest must not be reported as equivalent.
	if SameImage(ctx, img, "sha256:unrelatedxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", indexDigest) {
		t.Errorf("SameImage with unrelated local digest returned true; want false")
	}
}

func TestFetchRegistryDigest_404IsSilent(t *testing.T) {
	// A registry that returns 404 for the manifest should produce ("", nil)
	// — kpil treats this as "skip the staleness check silently" rather than
	// emitting "could not check for a newer image" noise on every startup.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	old := http.DefaultTransport
	http.DefaultTransport = srv.Client().Transport
	defer func() { http.DefaultTransport = old }()

	host := strings.TrimPrefix(srv.URL, "https://")
	img := host + "/some/missing:tag"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	digest, err := fetchRegistryDigest(ctx, img)
	if err != nil {
		t.Fatalf("fetchRegistryDigest on 404: unexpected error: %v", err)
	}
	if digest != "" {
		t.Fatalf("fetchRegistryDigest on 404: got %q, want \"\"", digest)
	}
}

func TestFetchRegistryDigest_RespectsContextTimeout(t *testing.T) {
	// Server that hangs forever — fetchRegistryDigest must respect the ctx
	// timeout so a flaky registry can't wedge CLI startup.
	done := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-done
	}))
	defer func() { close(done); srv.Close() }()
	old := http.DefaultTransport
	http.DefaultTransport = srv.Client().Transport
	defer func() { http.DefaultTransport = old }()

	host := strings.TrimPrefix(srv.URL, "https://")
	img := host + "/some/hung:tag"

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := fetchRegistryDigest(ctx, img)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("fetchRegistryDigest against hung server returned nil error, want context.DeadlineExceeded")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("fetchRegistryDigest blocked %v before honoring ctx timeout; want under 1s", elapsed)
	}
}
