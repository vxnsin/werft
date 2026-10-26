// Package state stores what werft knows about installed apps.
//
//	/var/lib/werft/apps/<name>/state.json    version, history, answers
//	/var/lib/werft/apps/<name>/secrets.json  secret answers (0600)
//	/var/lib/werft/apps/<name>/repo.git      git mirror
//	/var/lib/werft/apps/<name>/backups/      backups taken before updates
//	/var/lib/werft/apps/<name>/logs/         output of the steps
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Release struct {
	Version string    `json:"version"`
	Commit  string    `json:"commit"`
	At      time.Time `json:"at"`
}

type App struct {
	Name        string            `json:"name"`
	Source      string            `json:"source"`
	Channel     string            `json:"channel"`
	Branch      string            `json:"branch,omitempty"`
	Version     string            `json:"version"`
	Commit      string            `json:"commit"`
	Previous    []Release         `json:"previous"`
	Answers     map[string]string `json:"answers"`
	Migrations  []string          `json:"migrations"`
	Runtime     string            `json:"runtime"`
	InstalledAt time.Time         `json:"installed_at"`
	UpdatedAt   time.Time         `json:"updated_at"`

	// Auto is the schedule for automatic updates, set on the server with werft auto.
	Auto *Auto `json:"auto,omitempty"`

	// Kind is "git" for a plain repository added with werft track; such an app
	// has no werft.yml and is updated in place with git pull.
	Kind    string `json:"kind,omitempty"`
	Dir     string `json:"dir,omitempty"`     // working tree of a tracked repository
	After   string `json:"after,omitempty"`   // command run after a pull
	Restart string `json:"restart,omitempty"` // systemd unit restarted after a pull

	// Warden lists the service names whose ports werft holds at warden.
	Warden []string `json:"warden,omitempty"`

	// Secrets are kept separately in secrets.json.
	Secrets map[string]string `json:"-"`
}

// All returns normal and secret answers together.
func (a *App) All() map[string]string {
	m := make(map[string]string, len(a.Answers)+len(a.Secrets))
	for k, v := range a.Answers {
		m[k] = v
	}
	for k, v := range a.Secrets {
		m[k] = v
	}
	return m
}

// PushPrevious remembers the current version as the previous one.
func (a *App) PushPrevious(keep int) {
	if a.Version == "" {
		return
	}
	a.Previous = append([]Release{{a.Version, a.Commit, a.UpdatedAt}}, a.Previous...)
	if len(a.Previous) > keep {
		a.Previous = a.Previous[:keep]
	}
}

// Auto is when and how far an app is updated on its own.
type Auto struct {
	Schedule string `json:"schedule"` // systemd OnCalendar
	Policy   string `json:"policy"`   // patch, minor or major
}

type Store struct{ Home string }

func (s Store) AppDir(name string) string    { return filepath.Join(s.Home, "apps", name) }
func (s Store) RepoDir(name string) string   { return filepath.Join(s.AppDir(name), "repo.git") }
func (s Store) BackupDir(name string) string { return filepath.Join(s.AppDir(name), "backups") }
func (s Store) LogDir(name string) string    { return filepath.Join(s.AppDir(name), "logs") }

var ErrNotInstalled = errors.New("not installed")

func (s Store) Exists(name string) bool {
	_, err := os.Stat(filepath.Join(s.AppDir(name), "state.json"))
	return err == nil
}

func (s Store) Load(name string) (*App, error) {
	var a App
	if err := readJSON(filepath.Join(s.AppDir(name), "state.json"), &a); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s is %w", name, ErrNotInstalled)
		}
		return nil, err
	}
	a.Secrets = map[string]string{}
	err := readJSON(filepath.Join(s.AppDir(name), "secrets.json"), &a.Secrets)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if a.Answers == nil {
		a.Answers = map[string]string{}
	}
	return &a, nil
}

func (s Store) Save(a *App) error {
	dir := s.AppDir(a.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "state.json"), a); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, "secrets.json"), a.Secrets)
}

func (s Store) List() ([]*App, error) {
	entries, err := os.ReadDir(filepath.Join(s.Home, "apps"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var apps []*App
	for _, e := range entries {
		if !e.IsDir() || !s.Exists(e.Name()) {
			continue
		}
		a, err := s.Load(e.Name())
		if err != nil {
			return nil, err
		}
		apps = append(apps, a)
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
	return apps, nil
}

// Remove deletes the state of an app; backups can be kept.
func (s Store) Remove(name string, keepBackups bool) error {
	if !keepBackups {
		return os.RemoveAll(s.AppDir(name))
	}
	entries, err := os.ReadDir(s.AppDir(name))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() != "backups" {
			if err := os.RemoveAll(filepath.Join(s.AppDir(name), e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, "\n"...), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
