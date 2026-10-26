package app

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/vxnsin/werft/internal/manifest"
	"github.com/vxnsin/werft/internal/warden"
)

// claimer answers a port question marked warden: true with a port from
// warden, when one runs on the machine. Otherwise the question is asked.
func (w *Werft) claimer(app string) func(q manifest.Question, preferred string) (string, bool) {
	return func(q manifest.Question, preferred string) (string, bool) {
		wd := warden.FromEnv()
		if wd == nil {
			return "", false
		}
		if !w.wardenChecked {
			w.wardenUp = wd.Reachable()
			w.wardenChecked = true
		}
		if !w.wardenUp {
			return "", false
		}
		svc := wardenName(app, q)
		pref, _ := strconv.Atoi(preferred)
		st := w.UI.Step("warden", svc)
		port, err := wd.Claim(svc, "service", pref)
		if err != nil {
			st.Skip("no port, asking instead: " + err.Error())
			return "", false
		}
		st.OK(fmt.Sprintf("port %d", port))
		if !slices.Contains(w.held, svc) {
			w.held = append(w.held, svc)
		}
		return strconv.Itoa(port), true
	}
}

// wardenName is the service name at warden: the app name for the question
// "port", otherwise app-id.
func wardenName(app string, q manifest.Question) string {
	if q.ID == "port" {
		return app
	}
	return app + "-" + strings.ReplaceAll(q.ID, "_", "-")
}

// wardenEnv tells the app which port werft holds for it, so a client inside
// the app (like the one in aniplay) does not register a second time.
func wardenEnv(app string, m *manifest.Manifest, answers map[string]string, held []string) map[string]string {
	for _, q := range m.Questions {
		if !q.Warden {
			continue
		}
		svc := wardenName(app, q)
		if slices.Contains(held, svc) && answers[q.ID] != "" {
			return map[string]string{"WARDEN_SERVICE": svc, "WARDEN_PORT": answers[q.ID]}
		}
	}
	return nil
}

func (w *Werft) releaseWarden(held []string) {
	if len(held) == 0 {
		return
	}
	wd := warden.FromEnv()
	if wd == nil || !wd.Reachable() {
		return
	}
	for _, svc := range held {
		st := w.UI.Step("warden", svc)
		if err := wd.Release(svc); err != nil {
			st.Skip(err.Error())
			continue
		}
		st.OK("released")
	}
}
