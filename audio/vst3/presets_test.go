package vst3

import (
	"bytes"
	"encoding/binary"
	"path/filepath"
	"testing"
)

func TestAPresetFileKeepsTheState(t *testing.T) {
	c := Class{ID: iid(0x01020304, 0x05060708, 0x090A0B0C, 0x0D0E0F10), Name: "Thing: big", Vendor: "Maker/Co"}
	if got := c.FUID(); got != "0102030405060708090A0B0C0D0E0F10" {
		t.Fatalf("the FUID reads %s, want the numbers in order", got)
	}
	state := []byte(stateMagic)
	state = binary.LittleEndian.AppendUint32(state, 3)
	state = append(state, "abc"...)
	state = binary.LittleEndian.AppendUint32(state, 2)
	state = append(state, "de"...)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	path := PresetPath(c, "Warm pad")
	if filepath.Base(filepath.Dir(path)) != "Thing_ big" || filepath.Base(filepath.Dir(filepath.Dir(path))) != "Maker_Co" {
		t.Fatalf("the preset goes to %s, want the vendor's and the class's folders, named as files may be", path)
	}
	if err := WritePreset(path, c, state); err != nil {
		t.Fatal(err)
	}
	class, got, err := ReadPreset(path)
	if err != nil {
		t.Fatal(err)
	}
	if class != c.FUID() || !bytes.Equal(got, state) {
		t.Fatalf("read back, the preset is of %s with %q, want %s with %q", class, got, c.FUID(), state)
	}
	other := c
	other.ID[15]++
	if err := WritePreset(filepath.Join(filepath.Dir(path), "Bright", "Other.vstpreset"), other, state); err != nil {
		t.Fatal(err)
	}
	if err := WritePreset(filepath.Join(filepath.Dir(path), "Bright", "Lead.vstpreset"), c, state); err != nil {
		t.Fatal(err)
	}
	ps := ClassPresets(c)
	if len(ps) != 2 {
		t.Fatalf("the class has %d presets, want 2, the other class's passed over: %+v", len(ps), ps)
	}
	if ps[0].Bank != UserBank || ps[0].Group != c.Name || ps[0].Name != "Warm pad" || ps[1].Group != "Bright" || ps[1].Name != "Lead" {
		t.Fatalf("the presets are %+v, want the class's folder's first, then Bright's", ps)
	}
	if st, err := ps[1].State(); err != nil || !bytes.Equal(st, state) {
		t.Fatalf("a preset listed reads %q, %v", st, err)
	}
}

// A real plugin's state goes to a preset file and back, and the plugin
// takes it.
func TestAPluginsStateGoesThroughAPresetFile(t *testing.T) {
	m := lsp(t)
	cl := class(t, m, "Parametric Equalizer x16 Stereo")
	p, err := m.New(cl, Config{Rate: 44100})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	st, err := p.State()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "eq.vstpreset")
	if err := WritePreset(path, cl, st); err != nil {
		t.Fatal(err)
	}
	fuid, back, err := ReadPreset(path)
	if err != nil {
		t.Fatal(err)
	}
	if fuid != cl.FUID() || !bytes.Equal(back, st) {
		t.Fatal("the plugin's state came back changed from its preset file")
	}
	if err := p.SetState(back); err != nil {
		t.Fatal(err)
	}
}
