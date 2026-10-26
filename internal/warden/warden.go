// Package warden talks to a warden (https://github.com/vxnsin/warden) when
// one runs on the machine: it hands out ports by name and remembers them.
// werft registers an app's port there without a lease, so it stays reserved
// until werft releases it on uninstall.
package warden

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

const DefaultURL = "http://127.0.0.1:7010"

type Client struct {
	URL   string
	Token string
	HTTP  *http.Client
}

var offRe = regexp.MustCompile(`(?i)^(0|false|off|no)$`)

// FromEnv reads WARDEN, WARDEN_URL and WARDEN_TOKEN. Nil means: do not ask.
func FromEnv() *Client {
	if offRe.MatchString(os.Getenv("WARDEN")) {
		return nil
	}
	url := strings.TrimRight(os.Getenv("WARDEN_URL"), "/")
	if url == "" {
		url = DefaultURL
	}
	return &Client{URL: url, Token: os.Getenv("WARDEN_TOKEN"), HTTP: &http.Client{Timeout: 1500 * time.Millisecond}}
}

func (c *Client) do(method, path string, body, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequest(method, c.URL+path, &buf)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	var raw json.RawMessage
	json.NewDecoder(resp.Body).Decode(&raw)
	if resp.StatusCode >= 400 {
		var e struct {
			Detail string `json:"detail"`
		}
		json.Unmarshal(raw, &e)
		if e.Detail == "" {
			e.Detail = resp.Status
		}
		return fmt.Errorf("warden: %s", e.Detail)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Reachable reports whether a warden answers at the address.
func (c *Client) Reachable() bool {
	var h struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	return c.do(http.MethodGet, "/health", nil, &h) == nil && h.Status != "" && h.Version != ""
}

// Claim registers name and returns its port. The same name gets the same port
// again; preferred is a wish for the first time, 0 means any.
func (c *Client) Claim(name, kind string, preferred int) (int, error) {
	body := map[string]any{"name": name, "kind": kind, "host": "127.0.0.1", "meta": map[string]string{"by": "werft"}}
	if preferred > 0 {
		body["preferred_port"] = preferred
	}
	var reg struct {
		Port int `json:"port"`
	}
	if err := c.do(http.MethodPost, "/v1/services", body, &reg); err != nil {
		return 0, err
	}
	if reg.Port == 0 {
		return 0, fmt.Errorf("warden: no port in the answer")
	}
	return reg.Port, nil
}

// Release gives the port of name back.
func (c *Client) Release(name string) error {
	return c.do(http.MethodDelete, "/v1/services/"+name, nil, nil)
}
