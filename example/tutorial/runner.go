package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"go/scanner"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/marrasen/gunim/widget"
)

// The runner builds the tutorial with one lesson's file as edited, and
// runs it, open on that lesson. It belongs to the application half: it
// is work, and it runs on goroutines of its own, reporting to serve on
// a channel, so serve stays the only goroutine that touches the app.

// tutorialPackage is the tutorial's import path, which go list finds
// the source folder from.
const tutorialPackage = "github.com/marrasen/gunim/example/tutorial"

// A runEvent is what a run reports: how it is going, more output, and
// marks for the lesson's file. ID says which run, so serve can drop
// what a run it has moved on from still says.
type runEvent struct {
	ID      int
	Status  string
	Running bool
	Failed  bool
	// Output is more of what the build or the program wrote.
	Output string
	// Marks replace the marks shown when Marked is set.
	Marks  []widget.CodeMark
	Marked bool
}

// startRun builds the tutorial with lesson's file as source and runs
// it, reporting on events until it ends. Cancelling ctx stops it; done
// is serve's context, which sends wait on.
func startRun(ctx, done context.Context, id, lesson int, source string, events chan<- runEvent) {
	send := func(ev runEvent) {
		ev.ID = id
		select {
		case events <- ev:
		case <-done.Done():
		}
	}
	send(runEvent{Status: "Building", Running: true, Marked: true})

	dir, err := packageDir(ctx)
	if err != nil {
		send(runEvent{Status: "Cannot build here", Failed: true, Output: err.Error() + "\n"})
		return
	}
	tmp, err := os.MkdirTemp("", "gunim-tutorial-run-")
	if err != nil {
		send(runEvent{Status: "Cannot build", Failed: true, Output: err.Error() + "\n"})
		return
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	bin, out, err := build(ctx, dir, tmp, lessons[lesson].File, source)
	switch {
	case ctx.Err() != nil:
		send(runEvent{Status: "Stopped", Output: out})
		return
	case err != nil:
		send(runEvent{Status: "Build failed", Failed: true, Output: out, Marks: buildMarks(out, lessons[lesson].File), Marked: true})
		return
	}
	send(runEvent{Status: "Running", Running: true, Output: out})

	cmd := exec.CommandContext(ctx, bin, "-lesson", strconv.Itoa(lesson+1))
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		send(runEvent{Status: "Cannot run", Failed: true, Output: err.Error() + "\n"})
		return
	}
	waited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = pw.Close()
		waited <- err
	}()
	lines := bufio.NewScanner(pr)
	for lines.Scan() {
		send(runEvent{Status: "Running", Running: true, Output: lines.Text() + "\n"})
	}
	err = <-waited
	switch {
	case ctx.Err() != nil:
		send(runEvent{Status: "Stopped"})
	case err != nil:
		send(runEvent{Status: "Exited: " + err.Error(), Failed: true})
	default:
		send(runEvent{Status: "Exited"})
	}
}

// packageDir returns the folder the tutorial's source is in, as the go
// command sees it from the folder the tutorial runs in.
func packageDir(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "go", "list", "-f", "{{.Dir}}", tutorialPackage).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("building needs the Go toolchain, and the gunim repository to run from.\n"+
			"Start the tutorial there with go run ./example/tutorial.\n\n%s", strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// build builds the package in dir into tmp with file's source replaced
// by source, through an overlay, so the files on disk stay as they are.
// It returns the program and what the build wrote.
func build(ctx context.Context, dir, tmp, file, source string) (bin, out string, err error) {
	edited := filepath.Join(tmp, file)
	if err := os.WriteFile(edited, []byte(source), 0o644); err != nil {
		return "", "", err
	}
	overlay, err := json.Marshal(map[string]map[string]string{
		"Replace": {filepath.Join(dir, file): edited},
	})
	if err != nil {
		return "", "", err
	}
	overlayFile := filepath.Join(tmp, "overlay.json")
	if err := os.WriteFile(overlayFile, overlay, 0o644); err != nil {
		return "", "", err
	}
	bin = filepath.Join(tmp, "tutorial")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-overlay", overlayFile, "-o", bin, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	b, err := cmd.CombinedOutput()
	// The compiler names the edited file where the overlay put it; name
	// it as the reader knows it.
	out = strings.ReplaceAll(string(b), filepath.Join(tmp, file), "./"+file)
	return bin, out, err
}

// buildError matches a compiler's error: a file, a line, a column and
// a message.
var buildError = regexp.MustCompile(`(?m)^(?:.*[/\\])?([^/\\\s:]+\.go):(\d+):(\d+): (.+)$`)

// buildMarks returns the marks for file among a build's errors.
func buildMarks(out, file string) []widget.CodeMark {
	var marks []widget.CodeMark
	for _, m := range buildError.FindAllStringSubmatch(out, -1) {
		if m[1] != file {
			continue
		}
		line, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		marks = append(marks, widget.CodeMark{Line: line, Col: col, Message: m[4]})
	}
	return marks
}

// formatSource formats Go source as gofmt does. Source that does not
// parse comes back with marks where the parser stopped.
func formatSource(src string) (string, []widget.CodeMark, error) {
	out, err := format.Source([]byte(src))
	if err == nil {
		return string(out), nil, nil
	}
	var list scanner.ErrorList
	var marks []widget.CodeMark
	if errors.As(err, &list) {
		for _, e := range list {
			marks = append(marks, widget.CodeMark{Line: e.Pos.Line, Col: e.Pos.Column, Message: e.Msg})
		}
	}
	return src, marks, err
}
