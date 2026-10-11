//go:build linux && !android

package install

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// Two programs pointed at one folder: the second neither installs over
// the first nor takes its files away, and the first stays found.
func TestAnotherProgramsInstallStays(t *testing.T) {
	_, program := testHome(t)
	shared := filepath.Join(t.TempDir(), "apps")
	dir := func() (string, error) { return shared, nil }
	alpha := App{Name: "alpha", Version: "v1.0.0", Dir: dir}
	s, err := sessionFor(alpha, program, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	beta := App{Name: "beta", Version: "v2.0.0", Dir: dir}
	for _, uninstall := range []bool{false, true} {
		if _, err := sessionFor(beta, program, uninstall); !errors.Is(err, errOther) {
			t.Errorf("a session for beta in alpha's folder (uninstall %v): %v", uninstall, err)
		}
	}
	if _, err := Find(beta); !errors.Is(err, errOther) {
		t.Errorf("Find(beta) in alpha's folder: %v", err)
	}
	if err := SetUpdates(beta, UpdatesOff); !errors.Is(err, errOther) {
		t.Errorf("SetUpdates(beta) in alpha's folder: %v", err)
	}
	if in, err := Find(alpha); err != nil || in.Version != "v1.0.0" {
		t.Fatalf("alpha is now %+v, %v", in, err)
	}
	if _, err := os.Stat(filepath.Join(shared, "alpha")); err != nil {
		t.Error("alpha's program went:", err)
	}
}

// An older copy started where an older version put it, as by an old
// shortcut, hands over to the newer version installed, and leaves it as
// it is.
func TestMoveInLeavesANewerInstall(t *testing.T) {
	home, program := testHome(t)
	old := filepath.Join(home, "bin", "kakel")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("kakel v0.5.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := App{Name: "kakel", Version: "v0.6.0", Formerly: []string{old}}
	s, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	_, exe, _ := Where(a)
	a.Version = "v0.5.0"
	if newer := moveIn(a, old); newer != exe {
		t.Fatalf("moveIn handed over to %q, want %s", newer, exe)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "#!/bin/sh\necho studio\n" {
		t.Fatalf("the installed v0.6.0 now holds %q", raw)
	}
	if in, err := Find(a); err != nil || in.Version != "v0.6.0" {
		t.Fatalf("installed %+v, %v; want v0.6.0", in, err)
	}
	if _, err := os.Lstat(old); !errors.Is(err, os.ErrNotExist) {
		t.Error("the old copy stayed:", err)
	}
}

// What another program has under the same names stays: a link of the
// same name in ~/.local/bin, and desktop files that start another
// program, through an install and an uninstall with nothing installed.
func TestOtherProgramsEntriesStay(t *testing.T) {
	home, program := testHome(t)
	a := App{Name: "studio", Version: "v1.0.0"}
	link := filepath.Join(home, ".local", "bin", "studio")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/opt/pipx/venvs/studio/bin/studio", link); err != nil {
		t.Fatal(err)
	}
	theirs := []byte("[Desktop Entry]\nType=Application\nName=Studio\nExec=/usr/bin/studio %F\nIcon=studio\n")
	app := filepath.Join(home, ".local", "share", "applications", "studio.desktop")
	auto := filepath.Join(home, ".config", "autostart", "studio.desktop")
	desk := filepath.Join(home, "Desktop", "studio.desktop")
	icon := filepath.Join(home, ".local", "share", "icons", "hicolor", "256x256", "apps", "studio.png")
	for _, f := range []string{app, auto, desk, icon} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, theirs, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stays := func(when string) {
		t.Helper()
		if to, err := os.Readlink(link); err != nil || to != "/opt/pipx/venvs/studio/bin/studio" {
			t.Errorf("%s, the other program's link is %q, %v", when, to, err)
		}
		for _, f := range []string{app, auto, desk, icon} {
			if raw, err := os.ReadFile(f); err != nil || !bytes.Equal(raw, theirs) {
				t.Errorf("%s, %s is %q, %v", when, f, raw, err)
			}
		}
	}

	s, err := sessionFor(a, program, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Uninstall(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	stays("after an uninstall with nothing installed")

	s, err = sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(context.Background(), map[string]bool{PickDesktop: false}, nil); err == nil {
		t.Error("the install went on over another program's link")
	}
	if err := os.Remove(app); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(context.Background(), map[string]bool{PickDesktop: false}, nil); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{auto, desk} {
		if raw, _ := os.ReadFile(f); !bytes.Equal(raw, theirs) {
			t.Errorf("unticked, the install took away the other program's %s", f)
		}
	}
}

// A folder where the program goes is no program: nothing is found.
func TestAFolderIsNoInstall(t *testing.T) {
	testHome(t)
	a := App{Name: "studio", Version: "v1.0.0"}
	_, exe, _ := Where(a)
	if err := os.MkdirAll(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	if in, err := Find(a); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("a folder at %s was found installed: %+v, %v", exe, in, err)
	}
}

// A copy running from where an older version put it is one to close
// before the install, as one running where the program goes is.
func TestACopyRunningFromAnOldPlaceCounts(t *testing.T) {
	home, program := testHome(t)
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep to run")
	}
	raw, err := os.ReadFile(sleep)
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(home, "bin", "kakel")
	if err = os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(old, raw, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), old, "30")
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	a := App{Name: "kakel", Version: "v0.6.0", Formerly: []string{old}}
	for _, uninstall := range []bool{false, true} {
		s, err := sessionFor(a, program, uninstall)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Running(); !slices.Contains(got, cmd.Process.Pid) {
			t.Errorf("uninstall %v: running %v, want the copy at the old place, %d", uninstall, got, cmd.Process.Pid)
		}
	}
}

// Starts beside a copy already running, as when several files open at
// once, are not counted against a release on trial; and an install of a
// copy by hand ends the trial.
func TestStartsBesideARunningCopyAreNotCounted(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep to run")
	}
	raw, err := os.ReadFile(sleep)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "studio")
	if err = os.WriteFile(exe, raw, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(exe+".old", []byte("one"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = (trial{Version: "v2.0.0"}).write(exe); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), exe, "30")
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	a := App{Name: "studio", Version: "v2.0.0", Dir: func() (string, error) { return dir, nil }}
	for i := range trialStarts + 3 {
		if onTrial(a, exe) {
			t.Fatalf("start %d, beside a running copy, gave way", i+1)
		}
	}
	if tr, _ := readTrial(exe); tr.Starts != 0 {
		t.Errorf("starts beside a running copy were counted: %d", tr.Starts)
	}
}

// Installing a copy by hand over a release on trial ends the trial.
func TestAnInstallByHandEndsTheTrial(t *testing.T) {
	_, program := testHome(t)
	a := App{Name: "studio", Version: "v2.0.0"}
	s, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(s.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = (trial{Version: "v2.0.0", Starts: 2}).write(s.Exe); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Install(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := readTrial(s.Exe); ok {
		t.Error("an install by hand left the trial")
	}
}

// A fresh install stopped after the program was copied takes it back:
// nothing is left to be found, or taken on at a start.
func TestAStoppedFreshInstallLeavesNothing(t *testing.T) {
	_, program := testHome(t)
	a := testApp()
	s, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err = s.Install(ctx, nil, func(p Progress) {
		if p.Step == "Copying its files" {
			cancel()
		}
	})
	if err == nil {
		t.Fatal("an install stopped while copying its files went on")
	}
	if _, serr := os.Stat(s.Exe); serr == nil {
		t.Error("a stopped fresh install left the program")
	}
	if in, ferr := Find(a); !errors.Is(ferr, ErrNotInstalled) {
		t.Errorf("after a stopped fresh install Find says %+v, %v", in, ferr)
	}
}

// The updates' trials, as Windows runs them: the program replaced is
// moved aside to .old, in place of linked there.
func TestTrialsMovingTheProgramAside(t *testing.T) {
	movesAside = true
	t.Cleanup(func() { movesAside = false })
	for _, test := range []func(*testing.T){
		TestABadUpdateGivesWay,
		TestAGoodUpdatePasses,
		TestANewerReleaseOnTrialKeepsTheOneThatPassed,
		TestAFailedDownloadOnTrialChangesNothing,
		TestTheProgramBeingReplacedLeavesTheTrial,
		TestMoveAsidePastABusyOld,
	} {
		test(t)
	}
}
