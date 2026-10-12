//go:build linux && !android

package install

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An older version's install in ~/.local/bin, with no record of itself,
// moves to where the program goes now as the program starts from it:
// with its start with the session kept, a link where it was, and the
// desktop file starting it from its new place.
func TestMoveInFromWhereAnOlderVersionPutIt(t *testing.T) {
	home, program := testHome(t)
	old := filepath.Join(home, ".local", "bin", "kakel")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("kakel v0.6.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	auto := filepath.Join(home, ".config", "autostart", "kakel.desktop")
	if err := os.MkdirAll(filepath.Dir(auto), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auto, []byte("[Desktop Entry]\nExec="+old+" -tray\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := App{Name: "kakel", Version: "v0.6.0", Autostart: &Autostart{Args: []string{"-tray"}}, Formerly: []string{old}}

	s, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode != Upgrade || s.Have == nil || s.Have.Exe != old || !s.Have.Chose(PickAutostart) || s.Have.Chose(PickDesktop) {
		t.Fatalf("a download found mode %v, have %+v; want an upgrade of the old install, its autostart seen", s.Mode, s.Have)
	}

	moveIn(a, old)
	dir, exe, _ := Where(a)
	if raw, err := os.ReadFile(exe); err != nil || string(raw) != "kakel v0.6.0" {
		t.Fatalf("the program was not moved to %s: %q, %v", exe, raw, err)
	}
	if to, err := os.Readlink(old); err != nil || to != exe {
		t.Fatalf("%s is not a link to the new place: %q, %v", old, to, err)
	}
	m, err := readManifest(dir, a.id())
	if err != nil || m == nil || !m.Picks[PickAutostart] || m.Version != "v0.6.0" {
		t.Fatalf("the moved install kept %+v, %v", m, err)
	}
	if raw, _ := os.ReadFile(auto); !strings.Contains(string(raw), "Exec="+exe+" -tray") {
		t.Fatalf("the autostart still starts the old place:\n%s", raw)
	}

	// Change turns the start with the session off, and keeps it so.
	if err := Change(a, map[string]bool{PickAutostart: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(auto); err == nil {
		t.Error("Change left the autostart file")
	}
	if in, err := Find(a); err != nil || in.Chose(PickAutostart) {
		t.Errorf("Find says %+v, %v after the autostart was turned off", in, err)
	}
}

// An install with no record of itself where the program goes, as an
// older version's installer left it on Windows, is taken on as the
// program starts there.
func TestAdoptAnInstallThatKeptNoRecord(t *testing.T) {
	home, _ := testHome(t)
	a := App{Name: "kakel", Version: "v0.6.0", Autostart: &Autostart{Args: []string{"-tray"}}}
	dir, exe, _ := Where(a)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("kakel"), 0o755); err != nil {
		t.Fatal(err)
	}
	desk := filepath.Join(home, "Desktop", "kakel.desktop")
	if err := os.WriteFile(desk, []byte("[Desktop Entry]\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	startInstalled(a, exe, dir)
	m, err := readManifest(dir, a.id())
	if err != nil || m == nil {
		t.Fatalf("no record after taking the install on: %v", err)
	}
	if !m.Picks[PickDesktop] || m.Picks[PickAutostart] {
		t.Fatalf("taken on with %v; want the desktop shortcut it had, and no autostart", m.Picks)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "share", "applications", "kakel.desktop")); err != nil {
		t.Error("taking the install on wrote no desktop file:", err)
	}
}

// A choice the program's settings also change starts from how they
// stand, not from the last install's answer.
func TestCurrentChoices(t *testing.T) {
	_, program := testHome(t)
	a := App{Name: "kakel", Version: "v0.6.0",
		Choices: []Choice{{Key: "auto", Label: "Update automatically", On: true, Current: true}}}
	s, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(context.Background(), map[string]bool{"auto": true}, nil); err != nil {
		t.Fatal(err)
	}
	a.Choices[0].On = false
	again, _ := sessionFor(a, program, false)
	for _, o := range again.Offers {
		if o.Key == "auto" && o.On {
			t.Error("a current choice started from the last install's answer, not from how it stands")
		}
	}
}

// The offer to keep the program up to date sets the update mode:
// ticked, it installs; unticked, it tells a program that can ask, and
// is off for one that cannot. An install taken on starts from the
// program's own mode, and SetUpdates changes it.
func TestUpdateModes(t *testing.T) {
	_, program := testHome(t)
	src := GitHub{Repo: "marrasen/kakel"}
	install := func(a App, picks map[string]bool) Installation {
		t.Helper()
		s, err := sessionFor(a, program, false)
		if err != nil {
			t.Fatal(err)
		}
		in, err := s.Install(context.Background(), picks, nil)
		if err != nil {
			t.Fatal(err)
		}
		found, err := Find(a)
		if err != nil || found.Updates != in.Updates {
			t.Fatalf("Find says %+v, %v; the install said %s", found, err, in.Updates)
		}
		return in
	}
	plain := App{Name: "studio", Version: "v1.0.0", Updates: src, UpdateKey: testKey}
	if in := install(plain, nil); in.Updates != UpdatesInstall || !in.Chose(PickUpdates) {
		t.Fatalf("by default the mode is %s", in.Updates)
	}
	if in := install(plain, map[string]bool{PickUpdates: false}); in.Updates != UpdatesOff {
		t.Fatalf("unticked, with no way to ask, the mode is %s", in.Updates)
	}
	asks := plain
	asks.Available = func(Release) {}
	if in := install(asks, map[string]bool{PickUpdates: true}); in.Updates != UpdatesInstall {
		t.Fatalf("ticked, the mode is %s", in.Updates)
	}
	if in := install(asks, map[string]bool{PickUpdates: false}); in.Updates != UpdatesNotify {
		t.Fatalf("unticked, with a way to ask, the mode is %s", in.Updates)
	}
	if err := SetUpdates(asks, UpdatesOff); err != nil {
		t.Fatal(err)
	}
	// Unticked again, an install keeps off as off.
	if in := install(asks, map[string]bool{PickUpdates: false}); in.Updates != UpdatesOff {
		t.Fatalf("set off and installed again unticked, the mode is %s", in.Updates)
	}
	// Taken on with no picks, it keeps what it had.
	if in := install(asks, nil); in.Updates != UpdatesOff {
		t.Fatalf("installed again with no picks, the mode is %s", in.Updates)
	}
}

// An install taken on with no record of itself starts from the mode the
// program gives, as kakel gives the setting its users chose.
func TestAdoptKeepsTheProgramsMode(t *testing.T) {
	testHome(t)
	a := App{Name: "kakel", Version: "v0.6.0", Updates: GitHub{Repo: "marrasen/kakel"}, UpdateKey: testKey, UpdateMode: UpdatesNotify,
		Available: func(Release) {}}
	dir, exe, _ := Where(a)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("kakel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := adopt(a, exe); err != nil {
		t.Fatal(err)
	}
	in, err := Find(a)
	if err != nil || in.Updates != UpdatesNotify || in.Chose(PickUpdates) {
		t.Fatalf("taken on as %+v, %v; want notify", in, err)
	}
}

func TestPseudoVersionsAreNoReleases(t *testing.T) {
	for v, want := range map[string]bool{
		"v0.5.1-0.20261005120000-0123456789ab":        false,
		"v0.5.1-beta.1.0.20261005120000-0123456789ab": false,
		"v0.5.0+dirty":  false,
		"v0.6.0-beta.2": true,
		"v0.6.0":        true,
	} {
		if IsRelease(v) != want {
			t.Errorf("IsRelease(%q) = %v", v, !want)
		}
	}
}
