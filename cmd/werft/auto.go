package main

import (
	"flag"

	"github.com/vxnsin/werft/internal/app"
)

// werft auto: which apps update on their own, and when.
func runAuto(w *app.Werft, fs *flag.FlagSet, args []string) error {
	if len(args) == 0 {
		if !w.Interactive {
			return usageError{"werft auto needs a terminal; use werft auto set, off or list"}
		}
		return w.AutoInteractive()
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list", "ls":
		if _, err := parse(fs, rest, 0); err != nil {
			return err
		}
		return w.AutoList()

	case "off":
		pos, err := parse(fs, rest, -1)
		if err != nil {
			return err
		}
		names, err := expandAll(w, pos)
		if err != nil {
			return err
		}
		return w.AutoOff(names)

	case "set", "on":
		at := fs.String("at", "", "")
		policy := fs.String("policy", "", "")
		pos, err := parse(fs, rest, -1)
		if err != nil {
			return err
		}
		if *at == "" {
			return usageError{"--at is missing, e.g. --at 02:00 or --at \"Mon..Fri 04:00\""}
		}
		schedule, err := app.ParseSchedule(*at)
		if err != nil {
			return err
		}
		switch *policy {
		case "", "patch", "minor", "major":
		default:
			return usageError{"--policy: patch, minor or major"}
		}
		names, err := expandAll(w, pos)
		if err != nil {
			return err
		}
		return w.AutoSet(names, schedule, *policy)
	}
	return usageError{"werft auto: set, off or list"}
}

// expandAll turns "all" into every app and refuses an empty list.
func expandAll(w *app.Werft, names []string) ([]string, error) {
	if len(names) == 1 && names[0] == "all" {
		apps, err := w.Store.List()
		if err != nil {
			return nil, err
		}
		names = names[:0]
		for _, a := range apps {
			names = append(names, a.Name)
		}
	}
	if len(names) == 0 {
		return nil, usageError{"which apps? names, or all"}
	}
	return names, nil
}

// werft track: look after a plain git clone.
func runTrack(w *app.Werft, fs *flag.FlagSet, args []string) error {
	name := fs.String("name", "", "")
	after := fs.String("after", "", "")
	restart := fs.String("restart", "", "")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	return w.Track(app.TrackOptions{Dir: pos[0], Name: *name, After: *after, Restart: *restart})
}
