package health

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vxnsin/werft/internal/manifest"
)

func TestWaitHTTP(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := &manifest.Health{HTTP: srv.URL + "/{{ .path }}", Timeout: "5s"}
	got, err := Wait(h, map[string]string{"path": "health"}, nil)
	if err != nil || got != "200" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestWaitTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := Wait(&manifest.Health{HTTP: srv.URL, Timeout: "1500ms"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Status 500") {
		t.Fatalf("expected a timeout with a reason: %v", err)
	}
}
