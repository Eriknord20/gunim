package vst3

import (
	"os"
	"testing"
	"time"
)

// TestAnEditorOpens shows an editor for a few seconds, for a look: run
// with VST3_EDITOR=1.
func TestAnEditorOpens(t *testing.T) {
	if os.Getenv("VST3_EDITOR") == "" {
		t.Skip("set VST3_EDITOR to show an editor")
	}
	m := lsp(t)
	p, err := m.New(class(t, m, "Parametric Equalizer x16 Stereo"), Config{Rate: 44100})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.OpenEditor("LSP Parametric Equalizer"); err != nil {
		t.Fatal(err)
	}
	stop := time.Now().Add(6 * time.Second)
	in := sine(512, 0.3)
	for time.Now().Before(stop) && p.EditorOpen() {
		p.Process(append([]float32(nil), in...))
		time.Sleep(10 * time.Millisecond)
	}
	p.CloseEditor()
	if p.EditorOpen() {
		t.Fatal("the editor stayed open")
	}
}
