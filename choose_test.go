package gunim

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

func TestChooseFilesAsksTheWindowsDialog(t *testing.T) {
	w := NewOffscreen(geom.Sz(400, 300), nil)
	var asked driver.ChooseOptions
	w.Offscreen().SetChooser(func(o driver.ChooseOptions) ([]string, error) {
		asked = o
		return []string{"a.log", "b.log"}, nil
	})
	got, err := w.Client().ChooseFiles(context.Background(), driver.ChooseOptions{Title: "Open", Multiple: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"a.log", "b.log"}) || asked.Title != "Open" || !asked.Multiple {
		t.Fatalf("chose %v, asked with %+v", got, asked)
	}
}

func TestChooseFilesSaysWhenThereIsNoDialog(t *testing.T) {
	w := NewOffscreen(geom.Sz(400, 300), nil)
	if _, err := w.Client().ChooseFiles(context.Background(), driver.ChooseOptions{}); !errors.Is(err, driver.ErrNoChooser) {
		t.Fatalf("err = %v, want ErrNoChooser", err)
	}
}

func TestSaveFileAsksTheWindowsDialog(t *testing.T) {
	w := NewOffscreen(geom.Sz(400, 300), nil)
	var asked driver.SaveOptions
	w.Offscreen().SetSaver(func(o driver.SaveOptions) (string, error) {
		asked = o
		return "out/screenshot.png", nil
	})
	got, err := w.Client().SaveFile(context.Background(), driver.SaveOptions{Title: "Save a copy", Name: "screenshot.png", Folder: "out"})
	want, _ := filepath.Abs("out")
	if err != nil || got != "out/screenshot.png" || asked.Name != "screenshot.png" || asked.Folder != want {
		t.Fatalf("saved to %q, %v, asked with %+v, want the folder %q", got, err, asked, want)
	}
}

func TestChooseFilesOpensInAWholeFolder(t *testing.T) {
	w := NewOffscreen(geom.Sz(400, 300), nil)
	var asked driver.ChooseOptions
	w.Offscreen().SetChooser(func(o driver.ChooseOptions) ([]string, error) {
		asked = o
		return nil, nil
	})
	if _, err := w.Client().ChooseFiles(context.Background(), driver.ChooseOptions{Folder: "logs"}); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs("logs")
	if asked.Folder != want || !filepath.IsAbs(asked.Folder) {
		t.Fatalf("the dialog opens in %q, want %q", asked.Folder, want)
	}
	if _, err := w.Client().ChooseFiles(context.Background(), driver.ChooseOptions{}); err != nil || asked.Folder != "" {
		t.Fatalf("no folder asked for, the dialog opens in %q", asked.Folder)
	}
}
