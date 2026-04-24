package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/qjoly/kpil/internal/container"
	"github.com/qjoly/kpil/internal/k8s"
	"github.com/spf13/cobra"
)

// Config holds all the CLI configuration.
type Config struct {
	AdminKubeconfig string
	Namespace       string
	SAName          string
	OutKubeconfig   string
	Runtime         string
	Image           string
	Build           bool
	InsecureImage   bool
	TokenTTL        time.Duration
	NoCleanup       bool
}

var cfg Config

var rootCmd = &cobra.Command{
	Use:   "kpil",
	Short: "Spin up a GitHub Copilot CLI container with a read-only (no-secrets) kubeconfig",
	Long: `kpil provisions a read-only Kubernetes ServiceAccount (excluding
secrets access), generates a scoped kubeconfig, and launches an interactive container
running the GitHub Copilot CLI and kubectl.

On exit, all RBAC resources and the generated kubeconfig are removed automatically.`,
	RunE: run,
}

// Execute is the entrypoint called by main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	defaultKubeconfig := os.Getenv("KUBECONFIG")
	if defaultKubeconfig == "" {
		defaultKubeconfig = os.Getenv("HOME") + "/.kube/config"
	}

	rootCmd.Flags().StringVar(&cfg.AdminKubeconfig, "kubeconfig", defaultKubeconfig,
		"Path to the admin kubeconfig (needs cluster-admin privileges)")
	rootCmd.Flags().StringVar(&cfg.Namespace, "namespace", "default",
		"Namespace in which the ServiceAccount is created")
	rootCmd.Flags().StringVar(&cfg.SAName, "sa-name", "copilot-readonly",
		"Name of the ServiceAccount, ClusterRole, and ClusterRoleBinding to create")
	rootCmd.Flags().StringVar(&cfg.OutKubeconfig, "out", "./ro-kubeconfig",
		"Path where the generated read-only kubeconfig is written")
	rootCmd.Flags().StringVar(&cfg.Runtime, "runtime", "",
		"Container runtime to use: docker or podman (default: auto-detect)")
	rootCmd.Flags().StringVar(&cfg.Image, "image", "ghcr.io/qjoly/kpil:latest",
		"Container image name:tag to run (default: published image on ghcr.io)")
	rootCmd.Flags().BoolVar(&cfg.Build, "build", false,
		"Build the container image from the local Dockerfile instead of pulling it (requires GH_TOKEN)")
	rootCmd.Flags().BoolVar(&cfg.InsecureImage, "insecure-image", false,
		"Skip cosign signature verification (allows unsigned or locally-built OCI images)")
	rootCmd.Flags().DurationVar(&cfg.TokenTTL, "token-ttl", 24*time.Hour,
		"Lifetime of the ServiceAccount token (e.g. 24h, 2h30m)")
	rootCmd.Flags().BoolVar(&cfg.NoCleanup, "no-cleanup", false,
		"Skip deleting the kubeconfig and RBAC resources on exit (useful for debugging)")
}

func run(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ---- signal handling -----------------------------------------------
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	// We pass sigCh to the container runner so it can forward the signal
	// before cleanup; cancel() is called in the goroutine to stop ctx.
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\nSignal received — stopping container…")
		cancel()
	}()

	// ---- Kubernetes client ---------------------------------------------
	fmt.Printf("Connecting to cluster using %s\n", cfg.AdminKubeconfig)
	k8sClient, err := k8s.NewClient(cfg.AdminKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// ---- RBAC provisioning ---------------------------------------------
	rbacCfg := k8s.RBACConfig{
		Namespace: cfg.Namespace,
		SAName:    cfg.SAName,
	}

	fmt.Println("Provisioning RBAC resources (ServiceAccount, ClusterRole, ClusterRoleBinding)…")
	provisioned, err := k8sClient.EnsureRBAC(ctx, rbacCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "RBAC provisioning failed: %v\n", err)
		if !cfg.NoCleanup {
			cleanupRBAC(k8sClient, rbacCfg, provisioned)
		}
		return fmt.Errorf("aborting")
	}

	// ---- Generate kubeconfig -------------------------------------------
	fmt.Printf("Generating read-only kubeconfig (token TTL: %s) → %s\n", cfg.TokenTTL, cfg.OutKubeconfig)
	if err := k8sClient.GenerateKubeconfig(ctx, rbacCfg, cfg.TokenTTL, cfg.OutKubeconfig); err != nil {
		fmt.Fprintf(os.Stderr, "Kubeconfig generation failed: %v\n", err)
		if !cfg.NoCleanup {
			cleanupRBAC(k8sClient, rbacCfg, k8s.AllProvisioned)
			_ = os.Remove(cfg.OutKubeconfig)
		}
		return fmt.Errorf("aborting")
	}

	// ---- Cleanup on exit (deferred) ------------------------------------
	if !cfg.NoCleanup {
		defer func() {
			fmt.Println("Cleaning up RBAC resources and kubeconfig…")
			cleanupRBAC(k8sClient, rbacCfg, k8s.AllProvisioned)
			if err := os.Remove(cfg.OutKubeconfig); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "Warning: could not remove kubeconfig %s: %v\n", cfg.OutKubeconfig, err)
			} else {
				fmt.Printf("Removed kubeconfig: %s\n", cfg.OutKubeconfig)
			}
		}()
	}

	// ---- Container runtime ---------------------------------------------
	runtime, err := container.DetectRuntime(cfg.Runtime)
	if err != nil {
		return fmt.Errorf("container runtime error: %w", err)
	}
	fmt.Printf("Using container runtime: %s\n", runtime)

	// ---- Resolve image -------------------------------------------------
	// Priority:
	//   1. --build flag → always (re)build from the local Dockerfile.
	//   2. Image already present locally → use it as-is.
	//   3. Image not present locally → try to pull from the registry.
	//   4. Pull fails → tell the user to build it themselves with --build.
	if cfg.Build {
		fmt.Printf("Building image %s…\n", cfg.Image)
		if err := container.Build(runtime, cfg.Image); err != nil {
			return fmt.Errorf("image build failed: %w", err)
		}
	} else {
		exists, err := container.ImageExists(runtime, cfg.Image)
		if err != nil {
			return fmt.Errorf("cannot check image existence: %w", err)
		}
		if exists {
			fmt.Printf("Image %s found locally.\n", cfg.Image)
		} else {
			fmt.Printf("Image %s not found locally — attempting to pull…\n", cfg.Image)
			if pullErr := container.Pull(runtime, cfg.Image); pullErr != nil {
				return fmt.Errorf(
					"image %s is not available locally and could not be pulled (%v)\n\n"+
						"Build it yourself with:\n\n"+
						"  GH_TOKEN=<your-token> %s --build\n",
					cfg.Image, pullErr, os.Args[0],
				)
			}
		}
	}

	// ---- Verify image signature ----------------------------------------
	// Skip when the image was built locally (no signature exists) or the user
	// has explicitly opted out with --insecure-image.
	switch {
	case cfg.Build:
		// Local builds are never signed — no verification possible.
	case cfg.InsecureImage:
		fmt.Fprintln(os.Stderr, "Warning: --insecure-image set — skipping cosign signature verification.")
	default:
		fmt.Println("Verifying image signature (cosign)…")
		if err := container.VerifyImage(cfg.Image); err != nil {
			return fmt.Errorf("image verification failed:\n  %w", err)
		}
	}

	// ---- Run container -------------------------------------------------
	fmt.Printf("Starting GitHub Copilot CLI (image: %s)…\n", cfg.Image)
	fmt.Println("Press Ctrl+C to exit and trigger cleanup.")

	runCfg := container.RunConfig{
		Runtime:    runtime,
		Image:      cfg.Image,
		Kubeconfig: cfg.OutKubeconfig,
		Context:    ctx,
		SignalCh:   sigCh,
	}

	if err := container.Run(runCfg); err != nil {
		// A non-zero exit from the container shell is not a tool error — just report it.
		fmt.Fprintf(os.Stderr, "Container exited: %v\n", err)
	}

	return nil
}

// cleanupRBAC deletes RBAC resources based on what was actually provisioned.
func cleanupRBAC(client *k8s.Client, cfg k8s.RBACConfig, provisioned k8s.ProvisionedResources) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.DeleteRBAC(ctx, cfg, provisioned); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cleanup error: %v\n", err)
	}
}
