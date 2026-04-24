package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// execClient is the fallback backend that shells out to the docker/podman CLI.
type execClient struct {
	runtime string // "docker" or "podman"
}

func newExecClient(runtime string) *execClient {
	return &execClient{runtime: runtime}
}

func (e *execClient) Label() string { return e.runtime + "-exec" }

// ImageExists reports whether the image is present in the local store.
func (e *execClient) ImageExists(_ context.Context, img string) (bool, error) {
	cmd := exec.Command(e.runtime, "image", "inspect", "--format", "{{.Id}}", img)
	cmd.Stdout = nil
	cmd.Stderr = nil
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 0 {
		return false, nil
	}
	return false, fmt.Errorf("inspecting image %s: %w", img, err)
}

// Pull pulls the image from a remote registry.
func (e *execClient) Pull(_ context.Context, img string) error {
	fmt.Printf("  Pulling image %s…\n", img)
	cmd := exec.Command(e.runtime, "pull", img)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pulling image %s: %w", img, err)
	}
	return nil
}

// Build builds the image from the local Dockerfile.
func (e *execClient) Build(_ context.Context, img string, skills []string) error {
	dockerfilePath, err := findDockerfile()
	if err != nil {
		return err
	}
	buildCtx := filepath.Dir(dockerfilePath)

	args := []string{"build", "-t", img}

	if len(skills) > 0 {
		args = append(args, "--build-arg", "SKILLS="+strings.Join(skills, " "))
	}
	args = append(args, buildCtx)

	fmt.Printf("  Running: %s %s\n", e.runtime, strings.Join(args, " "))

	cmd := exec.Command(e.runtime, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building image %s: %w", img, err)
	}
	return nil
}

// Run starts the container interactively and blocks until it exits.
func (e *execClient) Run(ctx context.Context, cfg RunConfig) error {
	absKubeconfig, err := filepath.Abs(cfg.Kubeconfig)
	if err != nil {
		return fmt.Errorf("resolving kubeconfig path: %w", err)
	}

	networkMode := cfg.NetworkMode
	if networkMode == "" {
		networkMode = "host"
	}

	args := []string{
		"run",
		"--rm",
		"-it",
		"--network=" + networkMode,
		"-v", fmt.Sprintf("%s:/root/.kube/config:ro", absKubeconfig),
		// Forward GH_TOKEN without reading its value in Go — both Docker and
		// Podman resolve the value from the calling process env when no
		// '=value' is appended.
		"-e", "GH_TOKEN",
	}
	if cfg.Platform != "" {
		args = append(args, "--platform="+cfg.Platform)
	}

	if cfg.Workdir != "" {
		absWorkdir, err := filepath.Abs(cfg.Workdir)
		if err != nil {
			return fmt.Errorf("resolving workdir path: %w", err)
		}
		mount := fmt.Sprintf("%s:/workspace", absWorkdir)
		if cfg.WorkdirReadOnly {
			mount += ":ro"
		}
		args = append(args, "-v", mount)
	}

	for _, bind := range cfg.ExtraBinds {
		args = append(args, "-v", bind)
	}

	if cfg.Entrypoint != "" {
		args = append(args, "--entrypoint", cfg.Entrypoint)
	}

	args = append(args, cfg.Image)

	cmd := exec.Command(e.runtime, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	// containerExited is closed once cmd.Wait() returns so the watcher
	// goroutine knows it no longer needs to stop the container.
	containerExited := make(chan struct{})

	go func() {
		select {
		case <-ctx.Done():
			stopContainerByPID(e.runtime, cmd.Process.Pid)
		case <-containerExited:
		}
	}()

	waitErr := cmd.Wait()
	close(containerExited)
	return waitErr
}

// stopContainerByPID finds the running container whose host PID matches pid
// and sends it a stop command.
func stopContainerByPID(runtime string, pid int) {
	out, err := exec.Command(runtime, "ps", "-q").Output()
	if err != nil {
		return
	}
	for _, id := range strings.Fields(string(out)) {
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
