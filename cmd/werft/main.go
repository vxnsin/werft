// werft installs your own projects from git and keeps them up to date.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vxnsin/werft/internal/app"
	"github.com/vxnsin/werft/internal/manifest"
	"github.com/vxnsin/werft/internal/prompt"
	"github.com/vxnsin/werft/internal/ui"
)

// version is set by goreleaser through -ldflags.
var version = "dev"

const usage = `werft installs your own projects from git and keeps them up to date

  werft install <git-url> [--ref v1.2.0] [--name n] [--answers file.yml]
  werft update <name> | --all [--check] [--ref r] [--policy patch|minor|major]
  werft rollback <name> [--to version]
  werft config <name> [--set id=value ...]
  werft list
  werft status <name>
  werft start | stop | restart <name>
  werft logs <name> [-f] [-n 100]
  werft backups <name>
  werft uninstall <name> [--keep-data]

  werft init [dir]             create a werft.yml
  werft validate [file]        check a werft.yml
  werft version

  -y, --yes       answer confirmations with yes
  -v, --verbose   show the output of steps live

More: https://github.com/vxnsin/werft
`

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func main() {
	out := ui.New()
	if err := run(os.Args[1:]); err != nil {
		out.Error(err)
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprint(os.Stderr, "\n"+usage)
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	cmd, args := args[0], args[1:]

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.Usage = func() {}
	w := app.FromEnv()
	fs.BoolVar(&w.Yes, "y", false, "")
	fs.BoolVar(&w.Yes, "yes", false, "")
	fs.BoolVar(&w.Verbose, "v", false, "")
	fs.BoolVar(&w.Verbose, "verbose", false, "")

	switch cmd {
	case "install":
		ref := fs.String("ref", "", "")
		name := fs.String("name", "", "")
		answers := fs.String("answers", "", "")
		pos, err := parse(fs, args, 1)
		if err != nil {
			return err
		}
		preset, err := loadAnswers(*answers)
		if err != nil {
			return err
		}
		return w.Install(app.InstallOptions{Source: pos[0], Ref: *ref, Name: *name, Answers: preset})

	case "update":
		all := fs.Bool("all", false, "")
		check := fs.Bool("check", false, "")
		auto := fs.Bool("auto", false, "")
		ref := fs.String("ref", "", "")
		policy := fs.String("policy", "", "")
		answers := fs.String("answers", "", "")
		pos, err := parse(fs, args, -1)
		if err != nil {
			return err
		}
		switch *policy {
		case "", "patch", "minor", "major":
		default:
			return usageError{"--policy: patch, minor or major"}
		}
		preset, err := loadAnswers(*answers)
		if err != nil {
			return err
		}
		o := app.UpdateOptions{Ref: *ref, Check: *check, Auto: *auto, Policy: *policy, Answers: preset}
		if *auto {
			w.Interactive = false
		}
		switch {
		case *all && len(pos) == 0:
			if *ref != "" {
				return usageError{"--ref only works with a single app"}
			}
			return w.UpdateAll(o)
		case !*all && len(pos) == 1:
			err := w.Update(pos[0], o)
			if errors.Is(err, app.ErrAutoSkipped) {
				// For the timer this is not a failure, only a note in the journal.
				w.UI.Warn("%v", err)
				return nil
			}
			return err
		}
		return usageError{"which app? werft update <name> or werft update --all"}

	case "rollback":
		to := fs.String("to", "", "")
		pos, err := parse(fs, args, 1)
		if err != nil {
			return err
		}
		return w.Rollback(pos[0], *to)

	case "config":
		var sets setFlag
		fs.Var(&sets, "set", "")
		pos, err := parse(fs, args, 1)
		if err != nil {
			return err
		}
		return w.Configure(pos[0], sets.m)

	case "list", "ls":
		if _, err := parse(fs, args, 0); err != nil {
			return err
		}
		return w.List()

	case "status":
		pos, err := parse(fs, args, 1)
		if err != nil {
			return err
		}
		return w.Status(pos[0])

	case "start", "stop", "restart":
		pos, err := parse(fs, args, 1)
		if err != nil {
			return err
		}
		return w.Service(pos[0], cmd)

	case "logs":
		follow := fs.Bool("f", false, "")
		lines := fs.Int("n", 100, "")
		pos, err := parse(fs, args, 1)
		if err != nil {
			return err
		}
		return w.Logs(pos[0], *follow, *lines)

	case "backups":
		pos, err := parse(fs, args, 1)
		if err != nil {
			return err
		}
		files, err := w.Backups(pos[0])
		if err != nil {
			return err
		}
		if len(files) == 0 {
			fmt.Println("No backups.")
		}
		for _, f := range files {
			fmt.Println(f)
		}
		return nil

	case "uninstall", "remove":
		keep := fs.Bool("keep-data", false, "")
		pos, err := parse(fs, args, 1)
		if err != nil {
			return err
		}
		return w.Uninstall(pos[0], *keep)

	case "init":
		pos, err := parse(fs, args, -1)
		if err != nil {
			return err
		}
		dir := "."
		if len(pos) > 0 {
			dir = pos[0]
		}
		path, err := app.Init(dir)
		if err != nil {
			return err
		}
		fmt.Printf("Created %s. Check it with: werft validate %s\n", path, path)
		return nil

	case "validate", "check":
		pos, err := parse(fs, args, -1)
		if err != nil {
			return err
		}
		path := manifest.FileName
		if len(pos) > 0 {
			path = pos[0]
		}
		return validate(path)

	case "version", "--version", "-V":
		fmt.Println("werft", version)
		return nil

	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil
	}
	return usageError{fmt.Sprintf("unknown command %q", cmd)}
}

func validate(path string) error {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, manifest.FileName)
	}
	m, err := manifest.Load(path)
	if err != nil {
		return err
	}
	ps := m.Validate()
	if len(ps) == 0 {
		fmt.Printf("%s is fine.\n", path)
		return nil
	}
	for _, p := range ps {
		fmt.Printf("%s: %s\n", path, p)
	}
	return fmt.Errorf("%d mistakes", len(ps))
}

func loadAnswers(path string) (map[string]string, error) {
	if path == "" {
		return nil, nil
	}
	return prompt.LoadAnswers(path)
}

// parse allows flags after the arguments too (werft install URL --ref v1)
// and checks the number of arguments; want < 0 means any number.
func parse(fs *flag.FlagSet, args []string, want int) ([]string, error) {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	if err := fs.Parse(flags); err != nil {
		return nil, usageError{err.Error()}
	}
	if want >= 0 && len(pos) != want {
		if want == 1 {
			return nil, usageError{"werft " + fs.Name() + " needs exactly one argument"}
		}
		return nil, usageError{"werft " + fs.Name() + ": too many arguments"}
	}
	return pos, nil
}

// setFlag collects --set id=value, may be repeated.
type setFlag struct{ m map[string]string }

func (s *setFlag) String() string { return "" }

func (s *setFlag) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok || k == "" {
		return fmt.Errorf("--set expects id=value, not %q", v)
	}
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[k] = val
	return nil
}
