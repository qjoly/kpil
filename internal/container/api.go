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
// Note: the Docker SDK does not support BuildKit --secret mounts. Since the
// Dockerfile no longer requires any build-time secrets (the gh copilot
// extension is downloaded from the public github/gh-copilot release), the SDK
// and exec-based clients are now equivalent for build purposes.
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
//
// Exit detection strategy: we do NOT use ContainerWait because it races with
// AutoRemove — if the container exits and is removed before the HTTP wait
// request is processed, the daemon returns 404 and the channel may never fire,
// leaving the goroutine (and the caller) stuck.
//
// Instead we rely on the fact that Docker always closes the hijacked attach
// connection when the container's PID 1 exits. That EOF propagates through
// attach.Reader and causes io.Copy to return, closing copyDone. We use
// copyDone as our sole exit signal, exactly like exec.Command.Wait() works
// in the exec fallback.
func (a *apiClient) Run(ctx context.Context, cfg RunConfig) error {
	absKubeconfig, err := filepath.Abs(cfg.Kubeconfig)
	if err != nil {
		return fmt.Errorf("resolving kubeconfig path: %w", err)
	}

	ghToken := os.Getenv("GH_TOKEN")

	binds := []string{absKubeconfig + ":/root/.kube/config:ro"}
	if cfg.Workdir != "" {
		absWorkdir, err := filepath.Abs(cfg.Workdir)
		if err != nil {
			return fmt.Errorf("resolving workdir path: %w", err)
		}
		mount := absWorkdir + ":/workspace"
		if cfg.WorkdirReadOnly {
			mount += ":ro"
		}
		binds = append(binds, mount)
	}
	binds = append(binds, cfg.ExtraBinds...)

	networkMode := cfg.NetworkMode
	if networkMode == "" {
		networkMode = "host"
	}

	ctrCfg := &container.Config{
		Image:        cfg.Image,
		Tty:          true,
		OpenStdin:    true,
		StdinOnce:    true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Env:          []string{"GH_TOKEN=" + ghToken},
	}
	if cfg.Entrypoint != "" {
		ctrCfg.Entrypoint = []string{cfg.Entrypoint}
	}

	// ---- Create (no AutoRemove — we clean up ourselves) -----------------
	createResp, err := a.cli.ContainerCreate(
		ctx,
		ctrCfg,
		&container.HostConfig{
			NetworkMode: container.NetworkMode(networkMode),
			Binds:       binds,
		},
		nil, nil, "",
	)
	if err != nil {
		return fmt.Errorf("creating container: %w", err)
	}
	id := createResp.ID

	// Always remove the container on exit, regardless of how we get there.
	defer func() {
		_ = a.cli.ContainerRemove(context.Background(), id,
			container.RemoveOptions{Force: true})
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
	// attach.Close() is called explicitly once we know the container has
	// exited so that the io.Copy goroutines receive EOF promptly.

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
	// resizeCtx has its own cancel so the goroutine exits as soon as we are
	// done, regardless of whether the parent ctx was cancelled.
	resizeCtx, resizeCancel := context.WithCancel(ctx)
	winchCh := make(chan os.Signal, 1)
	signal.Notify(winchCh, syscall.SIGWINCH)
	resizeDone := make(chan struct{})
	go func() {
		defer close(resizeDone)
		for {
			select {
			case <-resizeCtx.Done():
				return
			case <-winchCh:
				if w, h, err := term.GetSize(inFD); err == nil {
					_ = a.cli.ContainerResize(resizeCtx, id,
						container.ResizeOptions{Width: uint(w), Height: uint(h)})
				}
			}
		}
	}()

	// ---- Copy I/O -------------------------------------------------------
	// In TTY mode the attach stream is raw (no multiplexing header).
	// copyDone closes when Docker closes the connection — which always
	// happens as soon as the container's PID 1 exits.
	copyDone := make(chan struct{})
	go func() {
		defer close(copyDone)
		_, _ = io.Copy(os.Stdout, attach.Reader)
	}()
	go func() {
		// Stdin → container. Leaked after the container exits, but the
		// write will fail immediately on a closed connection and the
		// goroutine will exit on its own.
		_, _ = io.Copy(attach.Conn, os.Stdin)
	}()

	// ---- Wait for exit --------------------------------------------------
	// Block until either:
	//   a) copyDone closes → container's PID 1 exited (natural exit).
	//   b) ctx is cancelled → Ctrl-C / SIGTERM → stop the container, then
	//      wait for Docker to close the attach connection.
	select {
	case <-copyDone:
		// Container exited; attach connection already closed by Docker.
	case <-ctx.Done():
		_ = a.cli.ContainerStop(context.Background(), id, container.StopOptions{})
		<-copyDone // wait for Docker to close the attach connection
	}

	// Unblock secondary goroutines now that I/O is done.
	attach.Close()
	resizeCancel()
	signal.Stop(winchCh)
	<-resizeDone

	// ---- Exit code (best-effort; container may already be gone) ---------
	var runErr error
	if info, err := a.cli.ContainerInspect(context.Background(), id); err == nil {
		if info.State != nil && info.State.ExitCode != 0 {
			runErr = fmt.Errorf("container exited with code %d", info.State.ExitCode)
		}
	}
	// defer ContainerRemove fires here.
	return runErr
}
