package container

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RunConfig holds everything the container runner needs.
type RunConfig struct {
	Runtime    string
	Image      string
	Kubeconfig string
	Context    context.Context
	SignalCh   chan os.Signal
}

// DetectRuntime returns the runtime to use. If preferred is non-empty it is
// validated; otherwise docker is tried first, then podman.
func DetectRuntime(preferred string) (string, error) {
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

// ImageExists reports whether the image is already present in the local
// daemon / storage.
func ImageExists(runtime, image string) (bool, error) {
	cmd := exec.Command(runtime, "image", "inspect", "--format", "{{.Id}}", image)
	cmd.Stdout = nil
	cmd.Stderr = nil
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	// A non-zero exit code simply means the image was not found.
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 0 {
		return false, nil
	}
	return false, fmt.Errorf("inspecting image %s: %w", image, err)
}

// Pull tries to pull the image from a remote registry.
// It returns an error if the pull fails (image not found, network issue, etc.).
func Pull(runtime, image string) error {
	fmt.Printf("  Pulling image %s…\n", image)
	cmd := exec.Command(runtime, "pull", image)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pulling image %s: %w", image, err)
	}
	return nil
}

// Build runs `<runtime> build` in the directory that contains the Dockerfile.
// It looks for the Dockerfile next to the running binary; if not found it
// falls back to the current working directory.
// No build-time secrets are required: the gh copilot extension is installed
// at container startup via entrypoint.sh using the runtime GH_TOKEN.
func Build(runtime, image string) error {
	dockerfilePath, err := findDockerfile()
	if err != nil {
		return err
	}
	buildContext := filepath.Dir(dockerfilePath)

	args := []string{
		"build",
		"-t", image,
		buildContext,
	}

	fmt.Printf("  Running: %s %s\n", runtime, strings.Join(args, " "))

	cmd := exec.Command(runtime, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building image %s: %w", image, err)
	}
	return nil
}

// Run starts the container interactively, mounting the generated kubeconfig
// and forwarding GH_TOKEN. It blocks until the container exits.
//
// Signal forwarding: the parent process's SIGINT/SIGTERM (already captured in
// RunConfig.SignalCh) causes context cancellation, which triggers a
// `docker/podman stop` call to gracefully stop the container.
func Run(cfg RunConfig) error {
	absKubeconfig, err := filepath.Abs(cfg.Kubeconfig)
	if err != nil {
		return fmt.Errorf("resolving kubeconfig path: %w", err)
	}

	args := []string{
		"run",
		"--rm",           // auto-remove the container on exit
		"-it",            // interactive + allocate TTY
		"--network=host", // allows kubectl to reach cluster APIs
		"-v", fmt.Sprintf("%s:/root/.kube/config:ro", absKubeconfig),
		// Forward GH_TOKEN from the caller's environment without reading its
		// value in Go. Both Docker and Podman resolve the value from the
		// calling process env when no '=value' is provided.
		"-e", "GH_TOKEN",
	}

	args = append(args, cfg.Image)

	cmd := exec.Command(cfg.Runtime, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	// containerExited is closed by the main goroutine after cmd.Wait() returns,
	// signalling the watcher goroutine that no stop is needed.
	containerExited := make(chan struct{})

	// Watch for context cancellation (signal received) in a separate goroutine
	// so we can request a graceful stop while Wait() blocks below.
	go func() {
		select {
		case <-cfg.Context.Done():
			// Ask the runtime to stop the running container gracefully.
			stopContainer(cfg.Runtime, cmd.Process.Pid)
		case <-containerExited:
			// Container already exited on its own — nothing to stop.
		}
	}()

	waitErr := cmd.Wait()
	close(containerExited) // unblock the watcher goroutine

	return waitErr
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// VerifyImage checks the cosign keyless signature of the given image against
// the official GitHub Actions OIDC signatures produced by this project's CI
// and release workflows.
//
// Verification requires cosign to be present in PATH. If it is not installed
// the function returns an actionable error that includes the install URL and a
// hint to use --insecure-image.
//
// The certificate identity regexp matches any workflow inside this repository,
// while the OIDC issuer is pinned to GitHub Actions — ensuring only images
// signed during a legitimate CI/CD run are accepted.
func VerifyImage(image string) error {
	if _, err := exec.LookPath("cosign"); err != nil {
		return fmt.Errorf(
			"cosign is not installed — cannot verify image signature\n" +
				"  Install cosign: https://docs.sigstore.dev/cosign/system_config/installation/\n" +
				"  Or skip verification with: --insecure-image (not recommended)",
		)
	}

	fmt.Printf("  Verifying cosign signature for %s…\n", image)

	var stderr bytes.Buffer
	cmd := exec.Command("cosign", "verify",
		"--certificate-identity-regexp", "https://github.com/qjoly/kpil/",
		"--certificate-oidc-issuer", "https://token.actions.githubusercontent.com",
		image,
	)
	// Suppress verbose JSON stdout; capture stderr so we can include it in the error.
	cmd.Stdout = nil
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf(
			"image signature verification failed for %s: %w\n"+
				"  cosign output: %s\n"+
				"  Use --insecure-image to skip verification (not recommended)",
			image, err, strings.TrimSpace(stderr.String()),
		)
	}

	fmt.Println("  Signature verified.")
	return nil
}

// stopContainer sends a stop request to the container whose host PID matches
// the given pid. We do this by listing all running containers and matching on
// the host PID reported by the runtime.
func stopContainer(runtime string, pid int) {
	// `docker/podman stop $(docker/podman ps -q)` would stop everything;
	// instead we use the inspect approach to target only our container.
	out, err := exec.Command(runtime, "ps", "-q").Output()
	if err != nil {
		return
	}
	ids := strings.Fields(string(out))
	for _, id := range ids {
		pidOut, err := exec.Command(runtime, "inspect",
			"--format", "{{.State.Pid}}", id).Output()
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(pidOut)) == fmt.Sprintf("%d", pid) {
			_ = exec.Command(runtime, "stop", id).Run()
			return
		}
	}
}

// findDockerfile searches for a Dockerfile next to the executable, then in
// the current working directory.
func findDockerfile() (string, error) {
	// 1. Next to the running binary.
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "Dockerfile")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	// 2. Current working directory.
	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, "Dockerfile")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("Dockerfile not found next to the binary or in the current directory; use --build from the project root")
}
