package install

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/gunim"
)

// TestShots opens the installer's window on the display and writes
// pictures of its pages, as they arrive and as they settle, to the
// folder GUNIM_INSTALL_SHOTS names. It is for looking at, and skipped
// without that folder.
func TestShots(t *testing.T) {
	dir := os.Getenv("GUNIM_INSTALL_SHOTS")
	if dir == "" {
		t.Skip("set GUNIM_INSTALL_SHOTS to a folder to write pictures of the installer to")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	program := filepath.Join(t.TempDir(), "studio")
	if err := os.WriteFile(program, make([]byte, 18<<20), 0o755); err != nil {
		t.Fatal(err)
	}
	a := App{
		Name: "Marras Mastering Studio", Version: "v0.2.0", Publisher: "Marcus Johansson",
		Description: "Masters an album, an EP or a single, through chains of VST3 plugins.",
		Icon:        testIcon(),
		FileTypes:   []FileType{{Name: "Mastering album", Exts: []string{".mastering"}, Default: true}},
		Updates:     GitHub{Repo: "marrasen/mastering-studio"},
		UpdateKey:   testKey,
		Data:        []string{filepath.Join(home, ".config", "studio")},
	}
	if os.Getenv("GUNIM_INSTALL_NOICON") != "" {
		a.Icon = nil
	}
	s, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err = gunim.Main(ctx, func(app *gunim.App) error {
		r, err := openWindow(app, s)
		if err != nil {
			return err
		}
		go func() { _ = r.serve(ctx) }()
		shot := func(name string, after time.Duration) {
			time.Sleep(after)
			img, err := r.c.Shot(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			f, err := os.Create(filepath.Join(dir, name+".png"))
			if err != nil {
				t.Error(err)
				return
			}
			_ = png.Encode(f, img)
			_ = f.Close()
		}
		do := func(in gunim.Intent) { r.events <- func() { r.handle(ctx, in) } }
		shot("1-arriving", 150*time.Millisecond)
		shot("2-welcome", 1200*time.Millisecond)
		do(started{Picks: map[string]bool{PickDesktop: true}})
		shot("3-working", 500*time.Millisecond)
		shot("4-working-late", 800*time.Millisecond)
		shot("5-done-ring", 500*time.Millisecond)
		shot("6-done-burst", 450*time.Millisecond)
		shot("7-done", 1600*time.Millisecond)
		r.events <- func() {
			r.failed(os.ErrPermission)
		}
		shot("8-failed", 200*time.Millisecond)
		r.events <- func() {
			r.s.Mode = Remove
			r.sc.Page, r.sc.Mode = pageRemove, Remove
			r.show()
		}
		shot("9-remove", 900*time.Millisecond)
		do(removed{Data: true})
		shot("10-removing", 500*time.Millisecond)
		shot("11-removed", 2200*time.Millisecond)
		r.events <- func() {
			r.s.Mode, r.sc.Mode, r.sc.Removing, r.sc.Quit = Upgrade, Upgrade, false, true
			r.s.App.Quit = func(context.Context) error { return nil }
			r.running = func() []int { return []int{1} }
			r.handle(ctx, started{})
		}
		shot("12-running", 900*time.Millisecond)
		do(quitThem{})
		shot("13-closing", 900*time.Millisecond)
		r.c.Close()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// testIcon is an icon to look at: a rounded tile in a magenta to violet
// gradient with a white disc in it.
func testIcon() image.Image {
	const n = 256
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := range n {
		for x := range n {
			// Rounded corners.
			cx, cy := float64(x)-n/2+0.5, float64(y)-n/2+0.5
			ex, ey := max(0, abs(cx)-(n/2-48)), max(0, abs(cy)-(n/2-48))
			if ex*ex+ey*ey > 48*48 {
				continue
			}
			t := float64(y) / n
			c := color.NRGBA{R: uint8(230 - 120*t), G: uint8(60 + 20*t), B: uint8(170 + 70*t), A: 0xff}
			if cx*cx+cy*cy < 60*60 && cx*cx+cy*cy > 36*36 {
				c = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// TestUpdateShots writes pictures of an update's pages, as TestShots
// does of the installer's.
func TestUpdateShots(t *testing.T) {
	dir := os.Getenv("GUNIM_INSTALL_SHOTS")
	if dir == "" {
		t.Skip("set GUNIM_INSTALL_SHOTS to a folder to write pictures of an update to")
	}
	a := App{Name: "kakel", Version: "v0.6.0", Publisher: "Marcus Johansson", Icon: testIcon(),
		Updates: GitHub{Repo: "marrasen/kakel"}, UpdateKey: testKey}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := gunim.Main(ctx, func(app *gunim.App) error {
		sc := updateScene(&a, pageUpdate, a.Version, "v0.7.0")
		sc.Restart = true
		c, err := openStage(app, &a, "Update kakel", updateHeight, sc)
		if err != nil {
			return err
		}
		r := newUpdater(a, c, sc)
		r.ctx = ctx
		go func() { _ = r.serve() }()
		shot := func(name string, after time.Duration) {
			time.Sleep(after)
			img, err := c.Shot(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			f, err := os.Create(filepath.Join(dir, "u"+name+".png"))
			if err != nil {
				t.Error(err)
				return
			}
			_ = png.Encode(f, img)
			_ = f.Close()
		}
		set := func(fn func()) { r.events <- func() { fn(); r.show() } }
		shot("1-loading", 900*time.Millisecond)
		set(func() {
			r.sc.NotesLoading = false
			r.sc.Notes = joinNotes([]ReleaseNotes{
				{Version: "v0.7.0", Notes: "### Changed\n\n**The installer waits for kakel to close.** Updating or uninstalling while kakel runs goes on by itself once it has closed.\n\n**Signed updates.** Each release now carries `SHA256SUMS.sig`, a signature of its checksums.\n\n**Back and Forward cross servers in the file manager.** Going from this computer's files to a server's no longer empties a window's history."},
				{Version: "v0.6.0", Notes: "### Changed\n\n**An installer of its own.** A kakel started from the zip opens the installer.\n\n- one\n- two\n- three"},
			})
		})
		shot("2-notes", 600*time.Millisecond)
		set(func() { r.sc.Page, r.sc.Step, r.sc.Progress = pageWorking, "Downloading 4.1 MB of 11.8 MB", 0.31 })
		shot("3-downloading", 900*time.Millisecond)
		set(func() { r.sc.Page = pageRestarting })
		shot("4-restarting", 900*time.Millisecond)
		set(func() { r.sc.Page, r.sc.Progress = pageUpdated, 1 })
		shot("5-updated", 1800*time.Millisecond)
		set(func() { r.sc.Page, r.sc.Have = pageNotes, "0.6.0" })
		shot("6-whatsnew", 900*time.Millisecond)
		set(func() {
			r.sc.Page, r.sc.Problem = pageFailed, "the SHA256SUMS of v0.7.0 is not signed with App.UpdateKey"
		})
		shot("7-failed", 900*time.Millisecond)
		set(func() {
			r.sc.Page, r.sc.Version, r.sc.Have, r.sc.Problem = pageAbout, "0.9.0", "", ""
			r.sc.Description, r.sc.Status = "Terminals, files and tunnels on your machines", "kakel is up to date."
		})
		shot("8-about", 1200*time.Millisecond)
		c.Close()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
