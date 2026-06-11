package container

import (
	"bufio"
	"context"
	"os/exec"
	"regexp"
	"strings"
)

// LocalAgentVersions extracts the baked-in agent versions of a locally
// stored image by running it briefly with a no-op entrypoint and parsing the
// `--version` output of each agent binary.
//
// preferredRuntime should be the same runtime kpil chose elsewhere (the
// --runtime flag, or "" to auto-detect). Threading it through avoids the
// case where the user passes --runtime podman but the host also has a
// docker binary earlier in PATH — without the preference, this probe
// would run against a different runtime than the image was pulled into
// and silently come back empty.
//
// This is the fallback for the on-startup version diff when the local image
// has no SBOM attached (e.g. it was pulled before kpil's CI started
// publishing SBOMs, or it was built with `--build`).
//
// Returns an empty map if no runtime CLI is available or the container
// failed to start — silent failure is fine because the caller already has
// the digest diff to show.
func LocalAgentVersions(ctx context.Context, preferredRuntime, img string) map[string]string {
	rt, err := detectRuntimeCLI(preferredRuntime)
	if err != nil {
		return nil
	}
	// One probe per agent. The "|| true" suffix prevents a missing binary
	// from short-circuiting the whole script.
	script := strings.Join([]string{
		`claude=$(claude --version 2>/dev/null || true)`,
		`opencode=$(opencode --version 2>/dev/null || true)`,
		`copilot=$(copilot --version 2>/dev/null | head -1 || true)`,
		`echo "claude=$claude"`,
		`echo "opencode=$opencode"`,
		`echo "copilot=$copilot"`,
	}, "; ")
	cmd := exec.CommandContext(ctx, rt, "run", "--rm",
		"--entrypoint", "sh",
		"--network=none",
		img, "-c", script)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseAgentVersionLines(out)
}

// agentVersionPattern matches the first semver-like sequence (e.g. "2.1.173",
// "v1.0.35", "0.10.5-rc.2") in a line of arbitrary surrounding text.
var agentVersionPattern = regexp.MustCompile(`v?(\d+\.\d+\.\d+(?:[-+][\w.]+)?)`)

func parseAgentVersionLines(out []byte) map[string]string {
	versions := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		agent := strings.TrimSpace(line[:eq])
		raw := strings.TrimSpace(line[eq+1:])
		if raw == "" {
			continue
		}
		if m := agentVersionPattern.FindStringSubmatch(raw); m != nil {
			versions[agent] = m[1]
		}
	}
	if len(versions) == 0 {
		return nil
	}
	return versions
}
