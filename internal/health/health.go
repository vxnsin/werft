// Package health waits after a start until the app answers.
package health

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/vxnsin/werft/internal/manifest"
)

// Wait checks until the app is healthy or the timeout runs out.
// The result is the HTTP status or "ok".
func Wait(h *manifest.Health, data map[string]string, run func(cmd string) error) (string, error) {
	timeout, err := time.ParseDuration(h.Timeout)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if h.HTTP != "" {
		url, err := manifest.Render(h.HTTP, data)
		if err != nil {
			return "", fmt.Errorf("health.http: %w", err)
		}
		return waitHTTP(ctx, url)
	}
	cmd, err := manifest.Render(h.Cmd, data)
	if err != nil {
		return "", fmt.Errorf("health.cmd: %w", err)
	}
	var last error
	for {
		if last = run(cmd); last == nil {
			return "ok", nil
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("not healthy after %s: %w", h.Timeout, last)
		case <-time.After(time.Second):
		}
	}
}

func waitHTTP(ctx context.Context, url string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	last := "no answer"
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 400 {
				return strconv.Itoa(resp.StatusCode), nil
			}
			last = "Status " + strconv.Itoa(resp.StatusCode)
		} else if ctx.Err() == nil {
			last = err.Error()
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("%s does not answer (%s)", url, last)
		case <-time.After(time.Second):
		}
	}
}
