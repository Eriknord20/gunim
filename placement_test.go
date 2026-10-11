package gunim

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

// optionsDriver is a pumpDriver that keeps the options of the last
// window it opened.
type optionsDriver struct {
	pumpDriver
	got driver.Options
}

func (d *optionsDriver) NewWindow(o driver.Options) (driver.Window, error) {
	d.got = o
	return d.pumpDriver.NewWindow(o)
}

func TestWindowOpensAtItsPlacementAndReportsIt(t *testing.T) {
	d := &optionsDriver{}
	saved := &driver.Placement{Bounds: geom.Rc(40, 50, 800, 600), Maximized: true}
	var got driver.Placement
	var ok bool
	err := runApp(context.Background(), d, func(a *App) error {
		w, err := a.NewWindow(WindowOptions{Title: "placed", Place: saved})
		if err != nil {
			return err
		}
		w.mustOffscreen(t).SetOrigin(geom.Pt(40, 50))
		got, ok = w.Placement()
		w.Close()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.got.Place != saved {
		t.Errorf("the driver was asked to place the window at %v, want %v", d.got.Place, saved)
	}
	if want := geom.Rc(40, 50, 800, 600); !ok || got.Bounds != want || got.Maximized {
		t.Errorf("Placement = %v, %v; want %v, not maximized, true", got, ok, want)
	}
}

func TestPlaceFileKeepsWhereTheWindowClosedAndOpensThere(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "window.json")
	// The first run: no file yet, so the window opens at its size, and
	// closes moved, which the file keeps.
	d := &optionsDriver{}
	err := runApp(context.Background(), d, func(a *App) error {
		w, err := a.NewWindow(WindowOptions{Title: "kept", Size: geom.Sz(640, 480), PlaceFile: file})
		if err != nil {
			return err
		}
		if d.got.Place != nil {
			t.Errorf("with no file yet, the window was placed at %v", d.got.Place)
		}
		w.mustOffscreen(t).SetOrigin(geom.Pt(70, 80))
		w.Close()
		for range w.Client().Intents() {
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	kept := readPlace(file)
	if kept == nil || kept.Bounds.Min != geom.Pt(70, 80) || kept.Bounds.Empty() {
		t.Fatalf("the file kept %v, want the window at 70, 80", kept)
	}
	// The next run opens where it closed.
	d = &optionsDriver{}
	err = runApp(context.Background(), d, func(a *App) error {
		w, err := a.NewWindow(WindowOptions{Title: "kept", PlaceFile: file})
		if err != nil {
			return err
		}
		w.Close()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.got.Place == nil || *d.got.Place != *kept {
		t.Errorf("the window opened at %v, want %v", d.got.Place, kept)
	}
}

func TestPlaceWinsOverPlaceFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "window.json")
	if err := os.WriteFile(file, []byte(`{"Bounds":{"Min":{"X":1,"Y":2},"Max":{"X":301,"Y":202}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &optionsDriver{}
	place := &driver.Placement{Bounds: geom.Rc(40, 50, 800, 600)}
	err := runApp(context.Background(), d, func(a *App) error {
		w, err := a.NewWindow(WindowOptions{Title: "placed", Place: place, PlaceFile: file})
		if err != nil {
			return err
		}
		w.Close()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.got.Place != place {
		t.Errorf("the window opened at %v, want Place's %v", d.got.Place, place)
	}
}

func TestAnUnreadablePlaceFileOpensAtSize(t *testing.T) {
	file := filepath.Join(t.TempDir(), "window.json")
	if err := os.WriteFile(file, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := readPlace(file); p != nil {
		t.Errorf("readPlace of a broken file = %v, want nil", p)
	}
}
