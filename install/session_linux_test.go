//go:build linux && !android

package install

import (
	"context"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// home gives the test a home folder of its own, with the desktop's
// folders under it, and a program to install.
func testHome(t *testing.T) (home, program string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := os.MkdirAll(filepath.Join(home, "Desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	program = filepath.Join(t.TempDir(), "Downloads", "studio_1.2.0_linux_amd64")
	if err := os.MkdirAll(filepath.Dir(program), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, []byte("#!/bin/sh\necho studio\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home, program
}

func testApp() App {
	icon := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for i := range icon.Pix {
		icon.Pix[i] = 0xff
	}
	return App{
		Name:        "Studio Deluxe",
		Version:     "v1.2.0",
		Description: "Masters an album.",
		Icon:        icon,
		Files:       fstest.MapFS{"lib/engine.so": {Data: []byte("engine"), Mode: 0o755}, "README.txt": {Data: []byte("read me")}},
		FileTypes: []FileType{
			{Name: "Studio album", Exts: []string{".album"}, Default: true},
			{Name: "WAV sound", Exts: []string{".wav"}, MIME: "audio/x-wav", Off: true},
		},
		Autostart: &Autostart{Args: []string{"-tray"}},
		Choices:   []Choice{{Key: "demo", Label: "Write demo songs", On: true}},
	}
}

// Installing puts the program and its files in the user's folders, with
// a link, a desktop file, an icon and the kinds of file it opens; the
// picks decide the rest; and uninstalling leaves none of it.
func TestInstallAndUninstall(t *testing.T) {
	home, program := testHome(t)
	a := testApp()
	var hooked Installation
	a.Installed = func(_ context.Context, in Installation) error { hooked = in; return nil }
	s, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode != Fresh || s.Have != nil {
		t.Fatalf("mode %v, have %v; want a fresh install", s.Mode, s.Have)
	}
	if want := filepath.Join(home, ".local", "share", "studio-deluxe", "studio-deluxe"); s.Exe != want {
		t.Fatalf("Exe %s, want %s", s.Exe, want)
	}
	if s.Need < int64(len("engine")+len("read me")) || s.Free <= 0 {
		t.Fatalf("need %d, free %d", s.Need, s.Free)
	}
	var steps []Progress
	in, err := s.Install(context.Background(), map[string]bool{PickDesktop: true, PickAutostart: true}, func(p Progress) { steps = append(steps, p) })
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) < 3 || steps[len(steps)-1].Done != 1 {
		t.Fatalf("progress %v, want steps ending at 1", steps)
	}
	for i := 1; i < len(steps); i++ {
		if steps[i].Done < steps[i-1].Done {
			t.Fatalf("progress went back: %v", steps)
		}
	}
	if !hooked.Chose("demo") || hooked.Chose(FileTypeKey(a.FileTypes[1])) || hooked.Version != "v1.2.0" {
		t.Fatalf("hook heard %+v", hooked)
	}
	data := filepath.Join(home, ".local", "share")
	for _, f := range []string{
		in.Exe,
		filepath.Join(in.Dir, "lib", "engine.so"),
		filepath.Join(in.Dir, "README.txt"),
		filepath.Join(in.Dir, manifestName),
		filepath.Join(data, "applications", "studio-deluxe.desktop"),
		filepath.Join(data, "icons", "hicolor", "256x256", "apps", "studio-deluxe.png"),
		filepath.Join(data, "mime", "packages", "studio-deluxe.xml"),
		filepath.Join(home, ".config", "autostart", "studio-deluxe.desktop"),
		filepath.Join(home, "Desktop", "studio-deluxe.desktop"),
	} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("not installed: %v", err)
		}
	}
	if fi, err := os.Stat(filepath.Join(in.Dir, "lib", "engine.so")); err != nil || fi.Mode()&0o111 == 0 {
		t.Errorf("engine.so lost its run bit: %v", fi)
	}
	link := filepath.Join(home, ".local", "bin", "studio-deluxe")
	if to, err := os.Readlink(link); err != nil || to != in.Exe {
		t.Errorf("link %s -> %q, %v; want %s", link, to, err, in.Exe)
	}
	desk, _ := os.ReadFile(filepath.Join(data, "applications", "studio-deluxe.desktop"))
	for _, want := range []string{"Name=Studio Deluxe\n", "Comment=Masters an album.\n", " %F\n", "Icon=studio-deluxe\n",
		"MimeType=application/x-studio-deluxe-album;\n"} {
		if !strings.Contains(string(desk), want) {
			t.Errorf("desktop file lacks %q:\n%s", want, desk)
		}
	}
	auto, _ := os.ReadFile(filepath.Join(home, ".config", "autostart", "studio-deluxe.desktop"))
	if !strings.Contains(string(auto), " -tray\n") {
		t.Errorf("autostart file does not start with -tray:\n%s", auto)
	}
	mime, _ := os.ReadFile(filepath.Join(data, "mime", "packages", "studio-deluxe.xml"))
	if !strings.Contains(string(mime), `<glob pattern="*.album"/>`) || strings.Contains(string(mime), "wav") {
		t.Errorf("mime package:\n%s", mime)
	}

	// The next session finds it, its picks kept.
	again, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Mode != Reinstall || again.Have == nil || !again.Have.Chose(PickDesktop) {
		t.Fatalf("mode %v, have %+v; want a reinstall that kept the picks", again.Mode, again.Have)
	}
	for _, o := range again.Offers {
		if o.Key == PickDesktop && !o.On {
			t.Error("the desktop offer forgot it was ticked")
		}
	}

	// As xdg-mime leaves it, with another program's entry beside ours.
	list := filepath.Join(home, ".config", "mimeapps.list")
	if err := os.WriteFile(list, []byte("[Default Applications]\napplication/x-studio-deluxe-album=studio-deluxe.desktop;\naudio/x-wav=other.desktop;studio-deluxe.desktop;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gone, err := sessionFor(a, program, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := gone.Uninstall(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(list); string(raw) != "[Default Applications]\naudio/x-wav=other.desktop;\n" {
		t.Errorf("mimeapps.list after the uninstall:\n%s", raw)
	}
	for _, f := range []string{in.Dir, link,
		filepath.Join(data, "applications", "studio-deluxe.desktop"),
		filepath.Join(data, "icons", "hicolor", "256x256", "apps", "studio-deluxe.png"),
		filepath.Join(data, "mime", "packages", "studio-deluxe.xml"),
		filepath.Join(home, ".config", "autostart", "studio-deluxe.desktop"),
		filepath.Join(home, "Desktop", "studio-deluxe.desktop"),
	} {
		if _, err := os.Lstat(f); err == nil {
			t.Errorf("%s is still there", f)
		}
	}
}

// An install over an older one replaces it whole, takes the files the
// new version dropped, and takes away what the user unticked.
func TestUpgradeDropsWhatIsGone(t *testing.T) {
	home, program := testHome(t)
	a := testApp()
	a.Version = "v1.1.0"
	s, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(context.Background(), map[string]bool{PickDesktop: true}, nil); err != nil {
		t.Fatal(err)
	}
	a.Version = "v1.2.0"
	a.Files = fstest.MapFS{"lib/engine.so": {Data: []byte("engine 2")}}
	up, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	if up.Mode != Upgrade {
		t.Fatalf("mode %v, want an upgrade", up.Mode)
	}
	in, err := up.Install(context.Background(), map[string]bool{PickDesktop: false}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(in.Dir, "README.txt")); err == nil {
		t.Error("the file the new version dropped is still there")
	}
	if raw, _ := os.ReadFile(filepath.Join(in.Dir, "lib", "engine.so")); string(raw) != "engine 2" {
		t.Errorf("engine.so holds %q", raw)
	}
	if _, err := os.Stat(filepath.Join(home, "Desktop", "studio-deluxe.desktop")); err == nil {
		t.Error("the unticked desktop shortcut is still there")
	}
	m, err := readManifest(in.Dir, a.id())
	if err != nil || m.Version != "v1.2.0" {
		t.Fatalf("manifest %+v, %v", m, err)
	}
}

// A downgrade is said as one.
func TestOlderCopyIsADowngrade(t *testing.T) {
	_, program := testHome(t)
	a := testApp()
	s, _ := sessionFor(a, program, false)
	if _, err := s.Install(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	a.Version = "v1.0.0"
	down, err := sessionFor(a, program, false)
	if err != nil || down.Mode != Downgrade {
		t.Fatalf("mode %v, %v; want a downgrade", down.Mode, err)
	}
}

// No room is said before anything is written.
func TestNoRoom(t *testing.T) {
	_, program := testHome(t)
	a := testApp()
	a.Space = 1 << 62
	s, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(context.Background(), nil, nil); err == nil || !strings.Contains(err.Error(), "needs") {
		t.Fatalf("err %v, want one about room", err)
	}
	if _, err := os.Stat(s.Dir); err == nil {
		t.Error("the folder was made with no room for the program")
	}
}

// The user's data goes only when asked, and never a folder that holds
// more than the program's.
func TestUninstallData(t *testing.T) {
	home, program := testHome(t)
	data := filepath.Join(home, ".config", "studio-deluxe")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	a := testApp()
	a.Data = []string{data}
	s, _ := sessionFor(a, program, false)
	if _, err := s.Install(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	gone, _ := sessionFor(a, program, true)
	if err := gone.Uninstall(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(data); err == nil {
		t.Error("the data stayed though asked to go")
	}
	if err := removeData(home); err == nil {
		t.Error("removeData took the home folder")
	}
	if err := removeData(filepath.Join(home, ".config")); err == nil {
		t.Error("removeData took the config folder")
	}
}

// A path with spaces and characters an Exec line gives a meaning is
// quoted as the desktop entry spec asks.
func TestExecArg(t *testing.T) {
	for in, want := range map[string]string{
		"/home/m/.local/share/studio/studio": "/home/m/.local/share/studio/studio",
		"/home/m/My Apps/studio":             `"/home/m/My Apps/studio"`,
		"/opt/100%/studio":                   `"/opt/100%%/studio"`,
		`/a "quote"`:                         `"/a \\"quote\\""`,
	} {
		if got := execArg(in); got != want {
			t.Errorf("execArg(%q) = %s, want %s", in, got, want)
		}
	}
}

// The accent of an icon is its most vivid colour.
func TestAccentOf(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := range 32 {
			c := color.NRGBA{R: 0x30, G: 0x30, B: 0x30, A: 0xff}
			if x > 20 {
				c = color.NRGBA{R: 0xe0, G: 0x40, B: 0xa0, A: 0xff}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	c, ok := accentOf(img)
	if !ok || c.R < 0xc0 || c.G > 0x60 {
		t.Fatalf("accent %v, %v; want the pink", c, ok)
	}
	if _, ok := accentOf(image.NewNRGBA(image.Rect(0, 0, 8, 8))); ok {
		t.Error("a clear icon gave an accent")
	}
}
