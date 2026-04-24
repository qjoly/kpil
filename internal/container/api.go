package container

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/moby/go-archive"
	"golang.org/x/term"
)

// apiClient uses the Docker SDK over a Unix socket.
type apiClient struct {
	cli   *client.Client
	label string // "docker" or "podman"
}

// newAPIClient dials the given socket and verifies the daemon is reachable.
func newAPIClient(socketPath, label string) (*apiClient, error) {
	cli, err := client.NewClientWithOpts(
		client.WithHost("unix://"+socketPath),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	if _, err := cli.Ping(context.Background()); err != nil {
		cli.Close()
		return nil, fmt.Errorf("pinging daemon: %w", err)
	}
	return &apiClient{cli: cli, label: label}, nil
}

func (a *apiClient) Label() string { return a.label + "-api" }

// ImageExists reports whether the image is present in the local store.
func (a *apiClient) ImageExists(ctx context.Context, img string) (bool, error) {
	_, _, err := a.cli.ImageInspectWithRaw(ctx, img)
	if err == nil {
		return true, nil
	}
	if client.IsErrNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("inspecting image %s: %w", img, err)
}

// Pull pulls the image from a remote registry, streaming progress to stdout.
func (a *apiClient) Pull(ctx context.Context, img string) error {
	fmt.Printf("  Pulling image %s…\n", img)
	rc, err := a.cli.ImagePull(ctx, img, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling image %s: %w", img, err)
	}
	defer rc.Close()

	fd := os.Stdout.Fd()
	return jsonmessage.DisplayJSONMessagesStream(rc, os.Stdout, fd, term.IsTerminal(int(fd)), nil)
}

// Build builds the image from the local Dockerfile using the SDK.
//
// Note: the Docker SDK does not support BuildKit --secret mounts, so the
// gh_token secret cannot be forwarded here without leaking it into the image
// history. The exec-based client (exec.go) uses `--secret id=gh_token,env=GH_TOKEN`
// and is preferred when GH_TOKEN is set. The SDK path leaves the secret absent;
// entrypoint.sh will install the gh copilot extension at first run instead.
func (a *apiClient) Build(ctx context.Context, img string, skills []string) error {
	dockerfilePath, err := findDockerfile()
	if err != nil {
		return err
	}
	buildCtxDir := filepath.Dir(dockerfilePath)

	tar, err := archive.TarWithOptions(buildCtxDir, &archive.TarOptions{})
	if err != nil {
		return fmt.Errorf("creating build context archive: %w", err)
	}
	defer tar.Close()

	buildArgs := map[string]*string{}
	if len(skills) > 0 {
		v := strings.Join(skills, " ")
		buildArgs["SKILLS"] = &v
	}

	resp, err := a.cli.ImageBuild(ctx, tar, dockertypes.ImageBuildOptions{
		Tags:       []string{img},
		BuildArgs:  buildArgs,
		Dockerfile: "Dockerfile",
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("building image %s: %w", img, err)
	}
	defer resp.Body.Close()

	fd := os.Stdout.Fd()
	if err := jsonmessage.DisplayJSONMessagesStream(resp.Body, os.Stdout, fd, term.IsTerminal(int(fd)), nil); err != nil {
		return fmt.Errorf("building image %s: %w", img, err)
	}
	return nil
}

// Run creates, attaches to, starts, and waits for an interactive container.
func (a *apiClient) Run(ctx context.Context, cfg RunConfig) error {
	absKubeconfig, err := filepath.Abs(cfg.Kubeconfig)
	if err != nil {
		return fmt.Errorf("resolving kubeconfig path: %w", err)
	}

	ghToken := os.Getenv("GH_TOKEN")

	// ---- Create ---------------------------------------------------------
	createResp, err := a.cli.ContainerCreate(
		ctx,
		&container.Config{
			Image:        cfg.Image,
			Tty:          true,
			OpenStdin:    true,
			StdinOnce:    true,
			AttachStdin:  true,
			AttachStdout: true,
			AttachStderr: true,
			Env:          []string{"GH_TOKEN=" + ghToken},
		},
		&container.HostConfig{
			NetworkMode: "host",
			AutoRemove:  true,
			Binds:       []string{absKubeconfig + ":/root/.kube/config:ro"},
		},
		nil, nil, "",
	)
	if err != nil {
		return fmt.Errorf("creating container: %w", err)
	}
	id := createResp.ID

	// Guard against pre-start failures (AutoRemove only fires after start).
	started := false
	defer func() {
		if !started {
			_ = a.cli.ContainerRemove(context.Background(), id,
				container.RemoveOptions{Force: true})
		}
	}()

	// ---- Attach BEFORE start so no output is missed --------------------
	attach, err := a.cli.ContainerAttach(ctx, id, container.AttachOptions{
		Stream: true,
		Stdin:  true,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		return fmt.Errorf("attaching to container: %w", err)
	}
	// attach.Close() is called explicitly after ContainerWait so that the
	// io.Copy goroutines below receive EOF and return promptly.

	// ---- Raw terminal ---------------------------------------------------
	inFD := int(os.Stdin.Fd())
	if term.IsTerminal(inFD) {
		oldState, err := term.MakeRaw(inFD)
		if err != nil {
			return fmt.Errorf("setting raw terminal: %w", err)
		}
		defer term.Restore(inFD, oldState)
	}

	// ---- Start ----------------------------------------------------------
	started = true
	if err := a.cli.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	// ---- Initial TTY resize ---------------------------------------------
	if term.IsTerminal(inFD) {
		if w, h, err := term.GetSize(inFD); err == nil {
			_ = a.cli.ContainerResize(ctx, id,
				container.ResizeOptions{Width: uint(w), Height: uint(h)})
		}
	}

	// ---- SIGWINCH forwarder goroutine -----------------------------------
	winchCh := make(chan os.Signal, 1)
	signal.Notify(winchCh, syscall.SIGWINCH)
	resizeDone := make(chan struct{})
	go func() {
		defer close(resizeDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-winchCh:
				if w, h, err := term.GetSize(inFD); err == nil {
					_ = a.cli.ContainerResize(ctx, id,
						container.ResizeOptions{Width: uint(w), Height: uint(h)})
				}
			}
		}
	}()

	// ---- Copy I/O -------------------------------------------------------
	// In TTY mode the response is a raw stream (no multiplexing header).
	copyDone := make(chan struct{})
	go func() {
		defer close(copyDone)
		_, _ = io.Copy(os.Stdout, attach.Reader)
	}()
	go func() {
		_, _ = io.Copy(attach.Conn, os.Stdin)
	}()

	// ---- Wait -----------------------------------------------------------
	statusCh, errCh := a.cli.ContainerWait(ctx, id, container.WaitConditionNotRunning)

	var runErr error
	select {
	case err := <-errCh:
		if err != nil {
			runErr = fmt.Errorf("waiting for container: %w", err)
		}
	case status := <-statusCh:
		if status.Error != nil {
			runErr = fmt.Errorf("container error: %s", status.Error.Message)
		} else if status.StatusCode != 0 {
			runErr = fmt.Errorf("container exited with code %d", status.StatusCode)
		}
	case <-ctx.Done():
		_ = a.cli.ContainerStop(context.Background(), id, container.StopOptions{})
	}

	// Close the hijacked connection so the io.Copy goroutines get EOF and
	// return. Without this, <-copyDone would block forever because the
	// Reader's underlying TCP connection stays open even after the container
	// process exits.
	attach.Close()

	signal.Stop(winchCh)
	<-copyDone
	<-resizeDone

	return runErr
}
