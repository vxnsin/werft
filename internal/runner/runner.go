// Package runner runs steps, hooks and migrations with sh.
package runner

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Runner struct {
	Env     []string  // KEY=value, on top of the own environment
	Log     io.Writer // full output (log file), may be nil
	Verbose io.Writer // show output live, may be nil
}

// Error carries the end of the output so you can see what went wrong.
type Error struct {
	What   string
	Output string
	Err    error
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %v", e.What, e.Err) }
func (e *Error) Unwrap() error { return e.Err }

// tail keeps only the last max bytes.
type tail struct {
	buf bytes.Buffer
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf.Write(p)
	if over := t.buf.Len() - t.max; over > 0 {
		t.buf.Next(over)
	}
	return len(p), nil
}

// Run runs cmd with sh -c in dir.
func (r *Runner) Run(dir, cmd string) error {
	return r.exec(cmd, dir, "sh", "-c", cmd)
}

// Script runs a file from the project with sh.
func (r *Runner) Script(dir, path string) error {
	full := filepath.Join(dir, filepath.FromSlash(path))
	if _, err := os.Stat(full); err != nil {
		return fmt.Errorf("%s is missing", path)
	}
	return r.exec(path, dir, "sh", full)
}

func (r *Runner) exec(what, dir string, name string, args ...string) error {
	t := &tail{max: 16 << 10}
	writers := []io.Writer{t}
	if r.Log != nil {
		fmt.Fprintf(r.Log, "\n$ %s\n", what)
		writers = append(writers, r.Log)
	}
	if r.Verbose != nil {
		writers = append(writers, r.Verbose)
	}
	out := io.MultiWriter(writers...)
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), r.Env...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return &Error{What: firstLine(what), Output: t.buf.String(), Err: err}
	}
	return nil
}

func firstLine(s string) string {
	return strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
}
