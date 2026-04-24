package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// promptInteractive presents a series of prompts on stderr and merges the
// user's answers into c.  It is only called when --interactive / -i is set.
// The current values in c are shown as defaults; pressing Enter keeps them.
func promptInteractive(c *Config) error {
	s := bufio.NewScanner(os.Stdin)

	fmt.Fprintln(os.Stderr, "\n=== Interactive Setup ===")
	fmt.Fprintln(os.Stderr, "Press Enter to accept the value shown in [brackets].")

	// ---- Image override ------------------------------------------------
	fmt.Fprintf(os.Stderr, "Image [%s]: ", c.Image)
	if s.Scan() {
		if v := strings.TrimSpace(s.Text()); v != "" {
			c.Image = v
		}
	}

	// ---- Network mode --------------------------------------------------
	defaultNetwork := c.NetworkMode
	if defaultNetwork == "" {
		defaultNetwork = "host"
	}
	fmt.Fprintf(os.Stderr, "Network mode (host/bridge/none) [%s]: ", defaultNetwork)
	if s.Scan() {
		if v := strings.TrimSpace(s.Text()); v != "" {
			switch v {
			case "host", "bridge", "none":
				c.NetworkMode = v
			default:
				fmt.Fprintf(os.Stderr, "  Unknown network mode %q — keeping %q\n", v, defaultNetwork)
				c.NetworkMode = defaultNetwork
			}
		} else {
			c.NetworkMode = defaultNetwork
		}
	}

	// ---- Volume mounts -------------------------------------------------
	fmt.Fprintln(os.Stderr, "Volume mounts (format: host_path:container_path, e.g. $PWD:/workspace):")
	for {
		label := "  Add volume mount"
		if len(c.ExtraBinds) > 0 {
			label = "  Add another volume mount"
		}
		fmt.Fprintf(os.Stderr, "%s (empty to skip): ", label)
		if !s.Scan() {
			break
		}
		v := strings.TrimSpace(s.Text())
		if v == "" {
			break
		}
		c.ExtraBinds = append(c.ExtraBinds, v)
	}

	// ---- Entrypoint override -------------------------------------------
	fmt.Fprintf(os.Stderr, "Entrypoint override (empty to use container default): ")
	if s.Scan() {
		if v := strings.TrimSpace(s.Text()); v != "" {
			c.Entrypoint = v
		}
	}

	if err := s.Err(); err != nil {
		return fmt.Errorf("reading interactive input: %w", err)
	}

	fmt.Fprintln(os.Stderr)
	return nil
}
