package container

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
)

// RunConfig holds what the container runner needs to start the container.
type RunConfig struct {
	Image           string
	Kubeconfig      string
	Workdir         string   // host path to mount as /workspace inside the container (empty = no mount)
	WorkdirReadOnly bool     // if true the workdir bind is mounted :ro
	ExtraBinds      []string // additional volume mounts in "host:container[:opts]" format
	NetworkMode     string   // network mode ("host", "bridge", "none", …); empty defaults to "host"
	Entrypoint      string   // override container entrypoint; empty = use image default
	Platform        string   // OCI platform string e.g. "linux/amd64", "linux/arm64"; empty = daemon default
}

// Client is the interface both the Docker-SDK backend and the exec fallback
// must satisfy.
type Client interface {
	// Label returns a human-readable name for the backend ("docker-api",
	// "podman-api", "docker-exec", "podman-exec", …).
	Label() string

	// ImageExists reports whether the image is present in the local store.
	ImageExists(ctx context.Context, image string) (bool, error)

	// Pull pulls the image from a remote registry.
	Pull(ctx context.Context, image string) error

	// Build builds the image from the local Dockerfile.
	// skills is an optional list of "owner/repo/skill-name" specs.
	Build(ctx context.Context, image string, skills []string) error

	// Run starts the container interactively and blocks until it exits.
	Run(ctx context.Context, cfg RunConfig) error
}

// NewClient returns the best available Client.
//
// Preference order:
//  1. Docker/Podman SDK over the Unix socket (full TTY support).
//  2. exec fallback using the CLI binary.
//
// If preferredRuntime is non-empty it is used as the CLI name for the exec
// fallback (and to choose which socket to try first).
func NewClient(preferredRuntime string) (Client, error) {
	socket, label, err := socketPath(preferredRuntime)
	if err == nil {
		c, err := newAPIClient(socket, label)
		if err == nil {
			return c, nil
		}
		fmt.Fprintf(os.Stderr, "Warning: Docker socket available but SDK init failed (%v) — falling back to exec\n", err)
	}

	// Fall back to the exec client.
	rt, err := detectRuntimeCLI(preferredRuntime)
	if err != nil {
		return nil, err
	}
	return newExecClient(rt), nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// socketPath returns the path to the first usable Unix socket and a label
// ("docker" or "podman"). It respects $DOCKER_HOST and tries well-known
// paths in order.
func socketPath(preferred string) (string, string, error) {
	// $DOCKER_HOST takes priority (handles remote/TLS too, but we only
	// support local unix:// here).
	if dh := os.Getenv("DOCKER_HOST"); dh != "" {
		const unixPrefix = "unix://"
		if len(dh) > len(unixPrefix) && dh[:len(unixPrefix)] == unixPrefix {
			p := dh[len(unixPrefix):]
			if canDial(p) {
				label := "docker"
				if preferred == "podman" {
					label = "podman"
				}
				return p, label, nil
			}
		}
		// Non-unix $DOCKER_HOST — skip socket path.
		return "", "", fmt.Errorf("DOCKER_HOST is set to a non-unix address; socket API unavailable")
	}

	type candidate struct {
		path  string
		label string
	}

	candidates := []candidate{
		{"/var/run/docker.sock", "docker"},
		{podmanSocket(), "podman"},
		{"/run/podman/podman.sock", "podman"},
	}

	// If the caller has a preference, try that label's socket first.
	if preferred == "podman" {
		reordered := make([]candidate, 0, len(candidates))
		for _, c := range candidates {
			if c.label == "podman" {
				reordered = append(reordered, c)
			}
		}
		for _, c := range candidates {
			if c.label != "podman" {
				reordered = append(reordered, c)
			}
		}
		candidates = reordered
	}

	for _, c := range candidates {
		if c.path != "" && canDial(c.path) {
			return c.path, c.label, nil
		}
	}

	return "", "", fmt.Errorf("no usable Docker/Podman socket found")
}

// podmanSocket returns the user-scoped podman socket path (may not exist).
func podmanSocket() string {
	uid := strconv.Itoa(os.Getuid())
	return "/run/user/" + uid + "/podman/podman.sock"
}

// canDial does a quick TCP-less connection attempt to see whether the socket
// file exists and is accepting connections.
func canDial(path string) bool {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// detectRuntimeCLI validates / auto-detects the CLI binary name.
func detectRuntimeCLI(preferred string) (string, error) {
	if preferred != "" {
		if _, err := exec.LookPath(preferred); err != nil {
			return "", fmt.Errorf("requested runtime %q not found in PATH: %w", preferred, err)
		}
		return preferred, nil
	}
	for _, rt := range []string{"docker", "podman"} {
		if _, err := exec.LookPath(rt); err == nil {
			return rt, nil
		}
	}
	return "", fmt.Errorf("no container runtime found: install docker or podman and ensure it is in PATH")
}
