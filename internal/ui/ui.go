// Package ui takes care of console output.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

type UI struct {
	Out   io.Writer
	Err   io.Writer
	Color bool
}

func New() *UI {
	color := os.Getenv("NO_COLOR") == "" && term.IsTerminal(int(os.Stdout.Fd()))
	return &UI{Out: os.Stdout, Err: os.Stderr, Color: color}
}

const (
	gray   = "\x1b[90m"
	green  = "\x1b[32m"
	red    = "\x1b[31m"
	orange = "\x1b[38;5;202m"
	bold   = "\x1b[1m"
	reset  = "\x1b[0m"
)

func (u *UI) paint(code, s string) string {
	if !u.Color {
		return s
	}
	return code + s + reset
}

func (u *UI) Printf(format string, args ...any) {
	fmt.Fprintf(u.Out, format, args...)
}

// Title is the heading of an operation, e.g. "v1.4.2 → v1.5.0    7 commits".
func (u *UI) Title(main, extra string) {
	line := "\n  " + u.paint(bold, main)
	if extra != "" {
		line += "    " + u.paint(gray, extra)
	}
	fmt.Fprintln(u.Out, line+"\n")
}

// Note is a gray extra line.
func (u *UI) Note(format string, args ...any) {
	fmt.Fprintln(u.Out, "  "+u.paint(gray, fmt.Sprintf(format, args...)))
}

func (u *UI) Warn(format string, args ...any) {
	fmt.Fprintln(u.Err, u.paint(orange, "warning")+": "+fmt.Sprintf(format, args...))
}

func (u *UI) Error(err error) {
	fmt.Fprintln(u.Err, u.paint(red, "error")+": "+err.Error())
}

// Done is the closing line.
func (u *UI) Done(main, extra string) {
	fmt.Fprintln(u.Out, "\n  "+u.paint(bold, main)+" "+u.paint(gray, extra))
}

// Step writes "  label     detail" without a newline; OK, Skip or Fail end the line.
type Step struct {
	u    *UI
	open bool
}

func (u *UI) Step(label, detail string) *Step {
	fmt.Fprintf(u.Out, "  %s %s ", u.paint(gray, pad(label, 10)), pad(detail, 28))
	return &Step{u: u, open: true}
}

func (s *Step) OK(result string) {
	if !s.open {
		return
	}
	s.open = false
	if result == "" {
		result = "ok"
	}
	fmt.Fprintln(s.u.Out, s.u.paint(green, result))
}

func (s *Step) Skip(result string) {
	if !s.open {
		return
	}
	s.open = false
	fmt.Fprintln(s.u.Out, s.u.paint(gray, result))
}

func (s *Step) Fail(err error) error {
	if s.open {
		s.open = false
		fmt.Fprintln(s.u.Out, s.u.paint(red, "failed"))
	}
	return err
}

// Output shows the end of a command output, indented, e.g. after an error.
func (u *UI) Output(text string, lines int) {
	all := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	for _, l := range all {
		if l != "" {
			fmt.Fprintln(u.Out, "    "+u.paint(gray, "│ ")+l)
		}
	}
}

// pad cuts to n characters; for paths the end matters, so they are cut at the front.
func pad(s string, n int) string {
	if r := []rune(s); len(r) > n {
		if strings.Contains(s, "/") {
			return "…" + string(r[len(r)-n+1:])
		}
		return string(r[:n-1]) + "…"
	}
	return s + strings.Repeat(" ", n-len([]rune(s)))
}
