package warden

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Fake answers like a warden: health, register, release.
func Fake(t *testing.T) (*httptest.Server, map[string]int) {
	t.Helper()
	held := map[string]int{}
	next := 8000
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "test"})
	})
	mux.HandleFunc("POST /v1/services", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name          string `json:"name"`
			PreferredPort int    `json:"preferred_port"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		port, ok := held[req.Name]
		if !ok {
			port = req.PreferredPort
			if port == 0 {
				port = next
				next++
			}
			held[req.Name] = port
			w.WriteHeader(http.StatusCreated)
		}
		json.NewEncoder(w).Encode(map[string]any{"name": req.Name, "port": port})
	})
	mux.HandleFunc("DELETE /v1/services/{name}", func(w http.ResponseWriter, r *http.Request) {
		delete(held, r.PathValue("name"))
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, held
}

func TestClaimAndRelease(t *testing.T) {
	srv, held := Fake(t)
	t.Setenv("WARDEN_URL", srv.URL)
	t.Setenv("WARDEN", "")
	c := FromEnv()
	if c == nil || !c.Reachable() {
		t.Fatal("fake warden should be reachable")
	}
	port, err := c.Claim("demo", "service", 3100)
	if err != nil || port != 3100 {
		t.Fatalf("%d %v", port, err)
	}
	again, _ := c.Claim("demo", "service", 0)
	if again != 3100 {
		t.Errorf("same name, same port: %d", again)
	}
	if err := c.Release("demo"); err != nil || len(held) != 0 {
		t.Errorf("release: %v %v", err, held)
	}
	t.Setenv("WARDEN", "0")
	if FromEnv() != nil {
		t.Error("WARDEN=0 must switch it off")
	}
}
