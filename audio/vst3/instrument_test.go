package vst3

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugtest"
)

// sdkModule loads one of the VST3 SDK's example plugins, built: the
// bundle name in the folder VST3_SDK_PLUGINS names, or
// /tmp/vst3sdk-plugins. The SDK is MIT-licensed, and its examples
// build with cmake; Note Expression Synth's needs its editor left out
// where VSTGUI's libraries are missing.
func sdkModule(t *testing.T, name string) *Module {
	t.Helper()
	dir := os.Getenv("VST3_SDK_PLUGINS")
	if dir == "" {
		dir = "/tmp/vst3sdk-plugins"
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		t.Skip("no", name, "in", dir)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// describeNamed returns the plugin of m named name, as package plug
// describes it.
func describeNamed(t *testing.T, m *Module, name string) plug.Descriptor {
	t.Helper()
	for _, c := range m.Classes {
		if c.Name == name && c.Audio() {
			d, err := m.Describe(c)
			if err != nil {
				t.Fatal(err)
			}
			return d
		}
	}
	t.Fatalf("no %q in %s", name, m.Path)
	return plug.Descriptor{}
}

// pitch returns the frequency of ch's sound from frame a on, the
// strongest from 100 Hz to 2 kHz, to the hertz, at rate.
func pitch(ch []float32, a int, rate float64) float64 {
	x := ch[a : a+4096]
	best, at := 0.0, 0.0
	for f := 100.0; f < 2000; f++ {
		var re, im float64
		for i, v := range x {
			s, c := math.Sincos(2 * math.Pi * f * float64(i) / rate)
			re += float64(v) * c
			im += float64(v) * s
		}
		if m := math.Hypot(re, im); m > best {
			best, at = m, f
		}
	}
	return at
}

// level is the RMS level of ch from frame a to b.
func level(ch []float32, a, b int) float64 { return rms(ch[a:b]) }

// TestAnInstrumentPlaysAndBendsANote plays A, 440 Hz, on the SDK's Note
// Expression Synth through package plug, as a host does: it sounds at
// 440 Hz, and bent up an octave, by its own note expression, at 880.
func TestAnInstrumentPlaysAndBendsANote(t *testing.T) {
	m := sdkModule(t, "note-expression-synth.vst3")
	d := describeNamed(t, m, "Note Expression Synth")
	if !d.Instrument() || !d.Notes {
		t.Fatalf("the synth is described as %q, notes %v", d.Kind, d.Notes)
	}
	pl := d.New(nil)
	defer pl.Close()
	s := plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2}
	const second = 48000
	// Its sine alone: no noise, no triangle.
	res := plugtest.Drive(t, &d, pl.Processor(), s, second, plugtest.Ports{
		Changes: []plug.Change{{ID: 1, Value: 0}, {ID: 3, Value: 0}},
		Events: []plug.Event{
			{Frame: 0, Kind: plug.NoteOn, Note: 1, Pitch: 69, Velocity: 1},
			{Frame: second / 2, Kind: plug.Expression, Note: 1, Expr: plug.Bend, Value: 12},
		}})
	if !pl.(*plugged).Plugin().Bends() {
		t.Error("the synth bends notes of their own, and the host thinks not")
	}
	// A value is written as the synth playing writes it.
	if prm, ok := d.Param(0); !ok || prm.Text(0.5) == "0.50 sec" || prm.Text(0.5) == "" {
		t.Errorf("the release time at 0.5 is written %q", prm.Text(0.5))
	} else {
		t.Logf("the release time at 0.5 is written %q", prm.Text(0.5))
	}
	for c, ch := range res.Out {
		for i, v := range ch {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("channel %d frame %d is %v", c, i, v)
			}
		}
	}
	at, bent := pitch(res.Out[0], second/4, s.Rate), pitch(res.Out[0], second*3/4, s.Rate)
	t.Logf("level %.3f, %.1f Hz, bent %.1f Hz", level(res.Out[0], 0, second/2), at, bent)
	if level(res.Out[0], second/8, second/2) < 0.01 {
		t.Fatal("the note made no sound")
	}
	if math.Abs(at-440) > 10 || math.Abs(bent-880) > 20 {
		t.Fatalf("the note sounds at %.1f Hz and bent at %.1f, want 440 and 880", at, bent)
	}
}

// TestAPedalHoldsAnInstrumentsNotes plays a note on mda Piano, which
// takes the sustain pedal as a parameter it maps MIDI's controller 64
// to, and lets it go: held by the pedal, it rings on louder than
// without.
func TestAPedalHoldsAnInstrumentsNotes(t *testing.T) {
	m := sdkModule(t, "mda-vst3.vst3")
	d := describeNamed(t, m, "mda Piano")
	s := plug.Setup{Rate: 48000, MaxBlock: 256, Channels: 2}
	const n = 48000
	play := func(pedal bool) float64 {
		pl := d.New(nil)
		defer pl.Close()
		ev := []plug.Event{
			{Frame: 100, Kind: plug.NoteOn, Note: 1, Pitch: 60, Velocity: 0.8},
			// Off a multiple of 16 frames: mda's examples, which
			// process in slices of 16, drop an event at a slice's end.
			{Frame: n/10 + 5, Kind: plug.NoteOff, Note: 1},
		}
		if pedal {
			ev = append([]plug.Event{{Frame: 0, Kind: plug.Pedal, Value: 1}}, ev...)
		}
		res := plugtest.Drive(t, &d, pl.Processor(), s, n, plugtest.Ports{Events: ev})
		return level(res.Out[0], n/2, n)
	}
	held, let := play(true), play(false)
	t.Logf("half a second after the key lifts: %.5f held by the pedal, %.5f without", held, let)
	if held < 0.001 || held < 4*let {
		t.Fatalf("the pedal held the note at %.5f, without it %.5f", held, let)
	}
}

// TestAnInstrumentsOutputsAreAuxPorts describes LSP's multi-sampler of
// an output for each sampler, and its sidechain compressor: the outputs
// beyond the main one are aux outputs, the sidechain an aux input, and
// both run as a host runs them.
func TestAnInstrumentsOutputsAreAuxPorts(t *testing.T) {
	m := lsp(t)
	d := describeNamed(t, m, "Multi-Sampler x12 DirectOut")
	if !d.Instrument() || len(d.AuxOut) < 12 {
		t.Fatalf("the sampler is %q, with %d aux outputs: %+v", d.Kind, len(d.AuxOut), d.AuxOut)
	}
	s := plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2}
	pl := d.New(nil)
	defer pl.Close()
	res := plugtest.Drive(t, &d, pl.Processor(), s, 4096, plugtest.Ports{Events: []plug.Event{
		{Frame: 0, Kind: plug.NoteOn, Note: 1, Pitch: 36, Velocity: 1}}})
	for i, port := range res.AuxOut {
		for _, ch := range port {
			if r := rms(ch); math.IsNaN(r) {
				t.Fatalf("aux output %d played %v", i, r)
			}
		}
	}
	sc := describeNamed(t, m, "Sidechain Compressor Stereo")
	if len(sc.AuxIn) != 1 || sc.Instrument() {
		t.Fatalf("the compressor is %q, with aux inputs %+v", sc.Kind, sc.AuxIn)
	}
	t.Logf("%s: aux outputs %v; %s: aux inputs %v", d.Name, d.AuxOut[:2], sc.Name, sc.AuxIn)
}

// TestAnyInstrument plays the first instrument of the plugin VST3_PLUGIN
// names, a .vst3 bundle or file, through package plug, as a host does:
// a chord sounds, and a second copy, of the first's state, sounds as
// loud. With VST3_INSTRUMENT set, it plays the instrument of that name.
func TestAnyInstrument(t *testing.T) {
	path := os.Getenv("VST3_PLUGIN")
	if path == "" {
		t.Skip("set VST3_PLUGIN to a plugin to run")
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var c Class
	for _, ic := range m.Instruments() {
		if name := os.Getenv("VST3_INSTRUMENT"); name == "" || ic.Name == name {
			c = ic
			break
		}
	}
	if c.Name == "" {
		t.Skipf("%s makes no instrument: %+v", path, m.Classes)
	}
	d, err := m.Describe(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s by %s, %s: %d parameters, aux in %v, aux out %v", d.Name, d.Vendor, d.Kind, len(d.Params), d.AuxIn, d.AuxOut)
	s := plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2}
	const n = 48000
	chord := []plug.Event{
		{Frame: 0, Kind: plug.NoteOn, Note: 1, Pitch: 60, Velocity: 0.8},
		{Frame: 0, Kind: plug.NoteOn, Note: 2, Pitch: 64, Velocity: 0.8},
		{Frame: 0, Kind: plug.NoteOn, Note: 3, Pitch: 67, Velocity: 0.8},
		{Frame: n / 2, Kind: plug.Expression, Note: 1, Expr: plug.Bend, Value: 2},
		{Frame: n * 3 / 4, Kind: plug.NoteOff, Note: 1},
		{Frame: n * 3 / 4, Kind: plug.NoteOff, Note: 2},
		{Frame: n * 3 / 4, Kind: plug.NoteOff, Note: 3},
	}
	pl := d.New(nil)
	defer pl.Close()
	res := plugtest.Drive(t, &d, pl.Processor(), s, n, plugtest.Ports{Events: chord})
	if err := pl.(*plugged).Err(); err != nil {
		t.Fatal(err)
	}
	first := level(res.Out[0], 0, n*3/4)
	if math.IsNaN(first) || math.IsInf(first, 0) || first == 0 {
		t.Fatalf("the chord played at %v", first)
	}
	p := pl.(*plugged).Plugin()
	t.Logf("level %.4f, latency %d, tail %d, bends notes %v, editor %v", first, p.Latency(), p.Tail(), p.Bends(), hasEditor(p))
	for _, prm := range d.Params[:min(len(d.Params), 3)] {
		t.Logf("%s starts at %.3f, written %q", prm.Name, prm.Default, prm.Text(prm.Default))
	}
	again := d.New(nil)
	defer again.Close()
	if err := again.SetState(pl.State()); err != nil {
		t.Fatal(err)
	}
	res = plugtest.Drive(t, &d, again.Processor(), s, n, plugtest.Ports{Events: chord})
	if second := level(res.Out[0], 0, n*3/4); math.Abs(second-first) > 0.2*first {
		t.Errorf("a copy of its state plays at %.4f, the first at %.4f", second, first)
	}
}

// TestALibraryFindsPluginsAsPlugLooksThemUp scans LSP's folder, and
// hands its Find to package plug: an instrument found is looked up by
// its ID, loaded as it is first asked for, and an ID found nowhere stays
// unknown, with the reason kept.
func TestALibraryFindsPluginsAsPlugLooksThemUp(t *testing.T) {
	lib := NewLibraryAt("", t.TempDir())
	missing := "vst3." + strings.Repeat("0", 31) + "1"
	if lib.Find(missing) || lib.Err(missing) == nil {
		t.Fatalf("an ID of no plugin was found, or its reason lost: %v", lib.Err(missing))
	}
	if lib.Find("marrasen.braid") {
		t.Fatal("a Go plugin's ID was found as a VST3 plugin's")
	}
	m := lsp(t)
	lib = NewLibraryAt("", filepath.Dir(m.Path))
	var sampler Found
	for _, f := range lib.Plugins() {
		if f.Name == "Sampler Stereo" {
			sampler = f
		}
	}
	if !sampler.Instrument() || sampler.Path != m.Path {
		t.Fatalf("the library found the sampler as %+v", sampler)
	}
	plug.AddFinder(lib.Find)
	d, ok := plug.Lookup(sampler.ID)
	if !ok || d.Name != "Sampler Stereo" || !d.Instrument() {
		t.Fatalf("looked up, the sampler is %+v, %v: %v", d, ok, lib.Err(sampler.ID))
	}
}

// TestAnInstrumentsEditorOpensBehindPlug opens the editor of LSP's
// sampler, made as package plug makes it, for a second: run with
// VST3_EDITOR set.
func TestAnInstrumentsEditorOpensBehindPlug(t *testing.T) {
	if os.Getenv("VST3_EDITOR") == "" {
		t.Skip("set VST3_EDITOR to show an editor")
	}
	m := lsp(t)
	d := describeNamed(t, m, "Sampler Stereo")
	pl := d.New(nil)
	defer pl.Close()
	ed := pl.(plug.Editor)
	if ed.OpenEditor("x") == nil {
		t.Fatal("one not yet prepared has an editor")
	}
	pl.Processor().Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	if err := ed.OpenEditor("gunim vst3 test: LSP Sampler"); err != nil {
		t.Fatal(err)
	}
	b := &plug.Block{Frames: 512, Out: [][]float32{make([]float32, 512), make([]float32, 512)}}
	for range 100 {
		pl.Processor().Process(b)
		time.Sleep(10 * time.Millisecond)
	}
	if !ed.EditorOpen() {
		t.Fatal("the editor closed by itself")
	}
	t.Logf("%d edits, %d values", ed.Edits(), len(ed.Values()))
	ed.CloseEditor()
	if ed.EditorOpen() {
		t.Fatal("the editor stayed open")
	}
}
