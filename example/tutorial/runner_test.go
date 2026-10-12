package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/widget"
)

func TestBuildErrorsBecomeMarksOnTheirFile(t *testing.T) {
	out := "# github.com/marrasen/gunim/example/tutorial\n" +
		"./lesson2.go:45:3: undefined: foo\n" +
		"./lesson3.go:10:1: syntax error: unexpected }\n" +
		"/home/x/gunim/example/tutorial/lesson2.go:50:12: too many errors\n"
	got := buildMarks(out, "lesson2.go")
	want := []widget.CodeMark{{Line: 45, Col: 3, Message: "undefined: foo"}, {Line: 50, Col: 12, Message: "too many errors"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("marks %+v, want %+v", got, want)
	}
}

func TestFormatTidiesOrMarksWhereItStopped(t *testing.T) {
	src, marks, err := formatSource("package main\nfunc  f( ) {\nx:=1\n_ = x}\n")
	if err != nil || len(marks) != 0 || src != "package main\n\nfunc f() {\n\tx := 1\n\t_ = x\n}\n" {
		t.Fatalf("formatted to %q, marks %v, err %v", src, marks, err)
	}
	broken := "package main\n\nfunc f() {\n\tx := \n}\n"
	src, marks, err = formatSource(broken)
	if err == nil || src != broken || len(marks) == 0 || marks[0].Line != 5 {
		t.Fatalf("broken source came back %q with marks %+v and err %v", src, marks, err)
	}
}

// pageCode returns the text of the open page's editor.
func pageCode(t *testing.T, w *gunim.Window, run func(int), view string) *page {
	t.Helper()
	return pageOf[paged](t, w, run, view, "page").thePage()
}

// An edit goes to the application, survives a trip to another lesson,
// and Format and Reset put new code in the editor as a step of undo.
func TestEditsSurviveAndFormatAndResetReplaceThem(t *testing.T) {
	w, c, a, run := stage(t)
	edited := strings.Replace(lesson1Source, "Hello from gunim", "Hello, reader", 1)
	a.handle(c, Edited{Lesson: 0, Source: edited})
	a.handle(c, Chose{Lesson: 1})
	run(30)
	a.handle(c, Chose{Lesson: 0})
	run(30)
	p := pageCode(t, w, run, "lesson1")
	if got := p.code.Text(); got != edited {
		t.Fatal("the edit was lost on the way back to lesson 1")
	}

	messy := strings.Replace(edited, "func buildLesson1(", "func  buildLesson1(", 1)
	a.handle(c, Edited{Lesson: 0, Source: messy})
	a.handle(c, FormatAsked{Lesson: 0})
	run(1)
	if got := p.code.Text(); got != edited {
		t.Fatal("Format left the code untidy")
	}
	a.handle(c, ResetAsked{Lesson: 0})
	run(1)
	if got := p.code.Text(); got != lesson1Source {
		t.Fatal("Reset left the edits in")
	}
	if _, ok := a.sources[0]; ok {
		t.Fatal("Reset kept the edited source")
	}
}

// A run's reports reach the open page: its status, output and marks.
// What an older run still says goes nowhere.
func TestRunReportsReachThePage(t *testing.T) {
	w, c, a, run := stage(t)
	a.runID, a.running = 3, 0
	a.ran(c, runEvent{ID: 2, Status: "Old", Output: "old\n"})
	a.ran(c, runEvent{ID: 3, Status: "Build failed", Failed: true, Output: "./lesson1.go:2:1: oops\n",
		Marks: []widget.CodeMark{{Line: 2, Col: 1, Message: "oops"}}, Marked: true})
	run(30)
	p := pageCode(t, w, run, "lesson1")
	if p.status.Text != "Build failed" || p.output.Text() != "./lesson1.go:2:1: oops\n" {
		t.Fatalf("the page shows %q with output %q", p.status.Text, p.output.Text())
	}
	if m := p.code.Marks(); len(m) != 1 || m[0].Line != 2 {
		t.Fatalf("the editor's marks are %+v", m)
	}
	if p.stop.Disabled != true {
		t.Fatal("Stop is on with nothing running")
	}
	// Running, Stop is on, and output piles up to its cap.
	for i := range maxOutput + 50 {
		a.ran(c, runEvent{ID: 3, Status: "Running", Running: true, Output: strings.Repeat("x", i%7) + "\n"})
	}
	run(1)
	if p.stop.Disabled {
		t.Fatal("Stop is off while running")
	}
	if n := strings.Count(a.runs[0].Output, "\n"); n > maxOutput {
		t.Fatalf("the output kept %d lines, over the cap of %d", n, maxOutput)
	}
}

// A real build of a broken lesson fails, and marks the broken line.
func TestABrokenLessonFailsToBuildWithAMark(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the tutorial")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	broken := strings.Replace(lesson2Source, "a.clicks++", "a.clicks += undefinedThing", 1)
	at := strings.Index(broken, "undefinedThing")
	if at < 0 {
		t.Fatal("the broken lesson is not broken")
	}
	line := strings.Count(broken[:at], "\n") + 1
	events := make(chan runEvent, 16)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	go startRun(ctx, ctx, 1, 1, broken, events)
	var last runEvent
	for ev := range events {
		last = ev
		if !ev.Running {
			break
		}
	}
	if last.Status != "Build failed" {
		t.Fatalf("the run ended %q with output %q", last.Status, last.Output)
	}
	if len(last.Marks) == 0 || last.Marks[0].Line != line || !strings.Contains(last.Marks[0].Message, "undefinedThing") {
		t.Fatalf("the marks are %+v, want one on line %d naming undefinedThing", last.Marks, line)
	}
}

// A valid lesson builds and runs, and Stop ends it. It opens a window,
// so it runs only with GUNIM_TUTORIAL_RUN set, on a machine with a
// display.
func TestALessonRunsAndStops(t *testing.T) {
	if os.Getenv("GUNIM_TUTORIAL_RUN") == "" {
		t.Skip("opens a window; set GUNIM_TUTORIAL_RUN to run it")
	}
	events := make(chan runEvent, 16)
	done, cancelDone := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancelDone()
	ctx, stop := context.WithCancel(done)
	defer stop()
	go startRun(ctx, done, 1, 0, lesson1Source, events)
	var statuses []string
	for ev := range events {
		statuses = append(statuses, ev.Status)
		if ev.Status == "Running" && ev.Output == "" && len(statuses) > 1 {
			time.Sleep(2 * time.Second)
			stop()
		}
		if !ev.Running {
			break
		}
	}
	if got := strings.Join(statuses, ", "); got != "Building, Running, Stopped" {
		t.Fatalf("the run went %s", got)
	}
}
