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
	Pull            bool
	InsecureImage   bool
	TokenTTL        time.Duration
	NoCleanup       bool
	Skills          []string
	Workdir         string
	WorkdirReadOnly bool
	// Interactive setup fields (populated by -i prompt or future flags)
	Interactive bool
	NetworkMode string   // network mode for the container (default: "host")
	ExtraBinds  []string // additional volume mounts in "host:container[:opts]" format
	Entrypoint  string   // override container entrypoint
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
		"Build the container image from the local Dockerfile instead of pulling it.\n"+
			"Default skills listed in skills.txt are baked in automatically.\n"+
			"Use --skill to inject additional skills at build time.")
	rootCmd.Flags().BoolVar(&cfg.Pull, "pull", false,
		"Always pull the latest image from the registry before running.\n"+
			"Fails if the pull fails. Mutually exclusive with --build.")
	rootCmd.Flags().BoolVar(&cfg.InsecureImage, "insecure-image", false,
		"Skip cosign signature verification (allows unsigned or locally-built OCI images)")
	rootCmd.Flags().DurationVar(&cfg.TokenTTL, "token-ttl", 24*time.Hour,
		"Lifetime of the ServiceAccount token (e.g. 24h, 2h30m)")
	rootCmd.Flags().BoolVar(&cfg.NoCleanup, "no-cleanup", false,
		"Skip deleting the kubeconfig and RBAC resources on exit (useful for debugging)")
	rootCmd.Flags().StringArrayVar(&cfg.Skills, "skill", nil,
		`Agent skill to bake into the image at build time (format: owner/repo/skill-name).
Repeatable; each skill's SKILL.md is fetched from raw.githubusercontent.com and
installed to /root/.copilot/skills/<name>/ inside the container.
Requires --build. Example: --skill lobbi-docs/claude/kubernetes`)
	rootCmd.Flags().StringVar(&cfg.Workdir, "workdir", "",
		"Mount a host directory into the container at /workspace so the AI can read and modify files.\n"+
			"Example: --workdir $PWD  (read-write by default; use --workdir-readonly for read-only)")
	rootCmd.Flags().BoolVar(&cfg.WorkdirReadOnly, "workdir-readonly", false,
		"Mount the --workdir directory as read-only inside the container")
	rootCmd.Flags().BoolVarP(&cfg.Interactive, "interactive", "i", false,
		"Prompt for runtime parameters (image, network mode, volume mounts, entrypoint)\n"+
			"before launching the container.  Non-interactive behaviour is preserved when\n"+
			"this flag is not set.")
}

func run(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ---- validate GH_TOKEN --------------------------------------------
	if os.Getenv("GH_TOKEN") == "" {
		return fmt.Errorf("GH_TOKEN is not set\n"+
			"  A GitHub personal access token with the 'copilot' scope is required.\n"+
			"  See docs/github-pat.md for instructions, then re-run with:\n"+
			"    GH_TOKEN=<your-token> %s", os.Args[0])
	}

	// ---- validate flag combinations ------------------------------------
	if len(cfg.Skills) > 0 && !cfg.Build {
		return fmt.Errorf("--skill requires --build: skills can only be baked into a locally-built image\n" +
			"  Re-run with: --build --skill <owner/repo/skill-name>")
	}
	if cfg.Pull && cfg.Build {
		return fmt.Errorf("--pull and --build are mutually exclusive: choose one")
	}
	if cfg.WorkdirReadOnly && cfg.Workdir == "" {
		return fmt.Errorf("--workdir-readonly requires --workdir to be set")
	}

	// ---- Interactive setup prompt -------------------------------------
	if cfg.Interactive {
		if err := promptInteractive(&cfg); err != nil {
			return fmt.Errorf("interactive setup failed: %w", err)
		}
	}

	// ---- signal handling -----------------------------------------------
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
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

	// ---- Container client ----------------------------------------------
	ctr, err := container.NewClient(cfg.Runtime)
	if err != nil {
		return fmt.Errorf("container runtime error: %w", err)
	}
	fmt.Printf("Using container backend: %s\n", ctr.Label())

	// ---- Resolve image -------------------------------------------------
	if cfg.Build {
		fmt.Printf("Building image %s…\n", cfg.Image)
		if err := ctr.Build(ctx, cfg.Image, cfg.Skills); err != nil {
			return fmt.Errorf("image build failed: %w", err)
		}
	} else if cfg.Pull {
		fmt.Printf("Pulling image %s…\n", cfg.Image)
		if err := ctr.Pull(ctx, cfg.Image); err != nil {
			return fmt.Errorf("image pull failed: %w", err)
		}
	} else {
		exists, err := ctr.ImageExists(ctx, cfg.Image)
		if err != nil {
			return fmt.Errorf("cannot check image existence: %w", err)
		}
		if exists {
			fmt.Printf("Image %s found locally.\n", cfg.Image)
		} else {
			fmt.Printf("Image %s not found locally — attempting to pull…\n", cfg.Image)
			if pullErr := ctr.Pull(ctx, cfg.Image); pullErr != nil {
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
	if cfg.Workdir != "" {
		access := "read-write"
		if cfg.WorkdirReadOnly {
			access = "read-only"
		}
		fmt.Printf("Mounting workdir %s → /workspace (%s)\n", cfg.Workdir, access)
	}
	fmt.Println("Press Ctrl+C to exit and trigger cleanup.")

	runCfg := container.RunConfig{
		Image:           cfg.Image,
		Kubeconfig:      cfg.OutKubeconfig,
		Workdir:         cfg.Workdir,
		WorkdirReadOnly: cfg.WorkdirReadOnly,
		ExtraBinds:      cfg.ExtraBinds,
		NetworkMode:     cfg.NetworkMode,
		Entrypoint:      cfg.Entrypoint,
	}

	if err := ctr.Run(ctx, runCfg); err != nil {
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
