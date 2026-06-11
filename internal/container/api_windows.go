//go:build windows

package container

import (
	"context"
	"fmt"
)

// apiClient is not supported on Windows (no Unix socket).
type apiClient struct {
	label string
}

func newAPIClient(_, label string) (*apiClient, error) {
	return nil, fmt.Errorf("docker API client is not supported on Windows")
}

func (a *apiClient) Label() string { return a.label + "-api" }

func (a *apiClient) ImageExists(_ context.Context, _ string) (bool, error) {
	return false, fmt.Errorf("not supported on Windows")
}

func (a *apiClient) LocalDigest(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("not supported on Windows")
}

func (a *apiClient) RemoteDigest(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("not supported on Windows")
}

func (a *apiClient) Pull(_ context.Context, _ string) error {
	return fmt.Errorf("not supported on Windows")
}

func (a *apiClient) Build(_ context.Context, _ string, _ []string) error {
	return fmt.Errorf("not supported on Windows")
}

func (a *apiClient) Run(_ context.Context, _ RunConfig) error {
	return fmt.Errorf("not supported on Windows")
}
