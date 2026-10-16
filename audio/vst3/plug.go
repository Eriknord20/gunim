package vst3

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"math"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/marrasen/gunim/audio/plug"
)

// ProcessBlock runs a block of package plug's through the plugin, as a
// plug host runs one: its main input into the main input bus, each aux
// input into an aux input bus in turn, and the buses out into Out and
// each aux output connected. An aux input left unconnected takes the
// main input, as a sidechain keys on what it changes. Changes and
// events go in at their frames, with their values from 0 to 1, as
// VST3 carries them; a note's group plays on a channel of its own.
// It makes nothing, and takes no lock.
func (p *Plugin) ProcessBlock(b *plug.Block) {
	k := min(b.Frames, p.cfg.Block)
	if p.closed || !p.active || k <= 0 {
		silence(b, 0)
		return
	}
	for i := range p.ins {
		src := b.In
		if aux := i - btoi(p.mainIn); aux >= 0 {
			if in := b.AuxInput(aux); in != nil {
				src = in
			}
		}
		fill(p.ins[i].chans, src, k)
	}
	p.inChanges.clear()
	p.events.n = 0
	p.takeEdits()
	p.keys.lift(p)
	for _, c := range b.Changes {
		if i, ok := p.edits.at[c.ID]; ok {
			if math.Float64frombits(p.values[i].Load()) == c.Value {
				continue
			}
			p.values[i].Store(math.Float64bits(c.Value))
			p.outputs.put(c.ID, c.Value)
		}
		p.change(c.ID, int32(min(c.Frame, k-1)), c.Value)
	}
	if p.notes {
		for i := range b.Events {
			p.keys.play(p, &b.Events[i])
		}
	}
	pos := b.Position
	p.ctx.projectTime = pos.Frame
	p.ctx.state = ctxTimeSig | ctxContTime
	// A song standing still is stopped, to the plugin, though the
	// sound plays on.
	if pos.Playing && !pos.Still {
		p.ctx.state |= ctxPlaying
	}
	if pos.Tempo > 0 {
		p.ctx.state |= ctxTempo | ctxMusicTime
		p.ctx.tempo, p.ctx.musicTime = pos.Tempo, pos.Beat
		p.ctx.barPosition = math.Floor(pos.Beat/4) * 4
	}
	p.run(k)
	spill(b.Out, p.outs[0].chans, k)
	for i := 1; i < len(p.outs); i++ {
		if out := b.AuxOutput(i - 1); out != nil {
			spill(out, p.outs[i].chans, k)
		}
	}
	silence(b, k)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// fill fills the first k frames of each of dst from src, channel by
// channel, src's last channels again where it has fewer, or silence
// where it has none.
func fill(dst, src [][]float32, k int) {
	for c, ch := range dst {
		if len(src) == 0 || len(src[c%len(src)]) == 0 {
			clear(ch[:k])
			continue
		}
		copy(ch[:k], src[c%len(src)][:k])
	}
}

// spill copies the first k frames of each of src to dst, src's first
// channels again where it has fewer.
func spill(dst, src [][]float32, k int) {
	for c, ch := range dst {
		copy(ch[:k], src[c%len(src)][:k])
	}
}

// silence writes silence to b's outputs from frame k on.
func silence(b *plug.Block, k int) {
	zero := func(chans [][]float32) {
		for _, ch := range chans {
			if len(ch) > k {
				clear(ch[k:])
			}
		}
	}
	zero(b.Out)
	for _, port := range b.AuxOut {
		zero(port)
	}
}

// PlugID returns the ID package plug knows class c by: "vst3." and its
// ID, as [Class.IDString] writes it.
func PlugID(c Class) string { return plugPrefix + c.IDString() }

const plugPrefix = "vst3."

// ClassID returns the class ID within id, an ID PlugID gave, and whether
// it is one.
func ClassID(id string) (string, bool) {
	c, ok := strings.CutPrefix(id, plugPrefix)
	return c, ok && len(c) == 32
}

// Describe returns class c of m as package plug describes a plugin: its
// parameters, from 0 to 1 as VST3 carries them, and its buses beyond the
// main ones as aux ports. It makes one of the plugin to read them, and
// lets it go. The descriptor's New makes plugins that run behind
// plug's interfaces, each made to sound as its processor is first
// prepared: one made for a host's views alone loads nothing.
func (m *Module) Describe(c Class) (plug.Descriptor, error) {
	if !c.Audio() {
		return plug.Descriptor{}, fmt.Errorf("vst3: %s is no effect or instrument, of %q", c.Name, c.Category)
	}
	return describe(c, func(cfg Config) (*Plugin, error) { return m.New(c, cfg) })
}

// describe is Describe, for plugins of class c that open makes.
func describe(c Class, open func(cfg Config) (*Plugin, error)) (plug.Descriptor, error) {
	p, err := open(Config{Rate: 48000, Block: 512})
	if err != nil {
		return plug.Descriptor{}, err
	}
	defer p.Close()
	kind := c.SubCategories
	if kind == "" {
		kind = "Fx"
	}
	cl := &plugClass{open: open, class: c}
	d := plug.Descriptor{ID: PlugID(c), Name: c.Name, Vendor: c.Vendor, Version: c.Version, Kind: kind,
		Notes: p.Notes() || c.Instrument()}
	ins, outs := p.Buses()
	if len(ins) > 0 && ins[0].Main {
		ins = ins[1:]
	}
	d.AuxIn = ports(ins, "Sidechain")
	d.AuxOut = ports(outs[1:], "Output")
	seen := map[uint32]bool{}
	for _, q := range p.Params() {
		if seen[q.ID] {
			continue
		}
		seen[q.ID] = true
		// The value it starts at, which may be other than the default
		// it gives, as a preset loaded as it starts sets it.
		prm := plug.Param{ID: q.ID, Name: q.Title, Unit: q.Units, Min: 0, Max: 1, Default: min(max(p.Value(q.ID), 0), 1),
			Steps: max(q.Steps, 0), Format: cl.formatter(q.ID, q.Units, q.Steps > 0)}
		if prm.Name == "" {
			prm.Name = fmt.Sprintf("Parameter %d", q.ID)
		}
		if q.Automatable {
			prm.Flags |= plug.Automatable
		}
		if q.Bypass {
			prm.Flags |= plug.Bypass
		}
		if q.Hidden {
			prm.Flags |= plug.Hidden
		}
		if q.ReadOnly {
			prm.Flags |= plug.ReadOnly
		}
		d.Params = append(d.Params, prm)
	}
	d.New = func(h plug.Host) plug.Plugin { return cl.newPlugin() }
	return d, nil
}

// ports returns buses as package plug's audio ports, each named apart:
// one unnamed is named from base, and one named as another before it
// takes its number.
func ports(buses []Bus, base string) []plug.AudioPort {
	out := make([]plug.AudioPort, 0, len(buses))
	names := map[string]bool{}
	for i, b := range buses {
		name := b.Name
		if name == "" {
			name = fmt.Sprintf("%s %d", base, i+1)
		}
		for n := 2; names[name]; n++ {
			name = fmt.Sprintf("%s %d", b.Name, n)
		}
		names[name] = true
		out = append(out, plug.AudioPort{Name: name, Channels: b.Channels})
	}
	return out
}

// Register describes class c of m, as Describe does, and registers it
// with package plug, under [PlugID]; a class registered already gives
// the descriptor registered.
func (m *Module) Register(c Class) (plug.Descriptor, error) {
	registerMu.Lock()
	defer registerMu.Unlock()
	if d, ok := registered[PlugID(c)]; ok {
		return d, nil
	}
	d, err := m.Describe(c)
	if err != nil {
		return plug.Descriptor{}, err
	}
	plug.Register(d)
	registered[d.ID] = d
	return d, nil
}

// registered holds the plugins Register registered, by ID: it looks
// there, as plug.Lookup may ask a Library, which registers.
var (
	registerMu sync.Mutex
	registered = map[string]plug.Descriptor{}
)

// registeredAs returns the plugin Register registered as id, and
// whether there is one.
func registeredAs(id string) (plug.Descriptor, bool) {
	registerMu.Lock()
	defer registerMu.Unlock()
	d, ok := registered[id]
	return d, ok
}

// The modules loaded by Load, by path: loaded once, kept until
// Shutdown, as a plugin's library is seldom safe to unload while the
// program runs.
var (
	modulesMu sync.Mutex
	modules   = map[string]*Module{}
)

// Load returns the module at path, loading it the first time it is
// asked for, and keeping it loaded after.
func Load(path string) (*Module, error) {
	modulesMu.Lock()
	defer modulesMu.Unlock()
	if m := modules[path]; m != nil {
		return m, nil
	}
	m, err := Open(path)
	if err != nil {
		return nil, err
	}
	modules[path] = m
	return m, nil
}

// Shutdown closes the modules Load loaded, each running its exit before
// the program's, and ends the plugins' thread. A program calls it as it
// ends, once it has closed the plugins made from them, and their
// editors: some modules crash as they unload with the process instead,
// their exit not run. Load loads a module again after.
func Shutdown() error {
	modulesMu.Lock()
	ms := modules
	modules = map[string]*Module{}
	modulesMu.Unlock()
	var errs []error
	for _, m := range ms {
		if err := m.Close(); err != nil {
			errs = append(errs, fmt.Errorf("vst3: %s: %w", m.Path, err))
		}
	}
	stopUI()
	return errors.Join(errs...)
}

// ErrNotFound is the error of RegisterID for a class none of the
// bundles makes.
var ErrNotFound = errors.New("vst3: no plugin found of that ID")

// RegisterID registers the plugin of plug ID id, as PlugID gives it,
// from whichever of bundles makes it, as Register does: from one that
// lists it, or else, loading each that lists nothing, from one whose
// module makes it. A plugin registered already gives the descriptor
// registered.
func RegisterID(id string, bundles []Bundle) (plug.Descriptor, error) {
	if d, ok := registeredAs(id); ok {
		return d, nil
	}
	cid, ok := ClassID(id)
	if !ok {
		return plug.Descriptor{}, fmt.Errorf("vst3: %q is no VST3 plugin's ID", id)
	}
	listed := func(b Bundle) bool {
		for _, c := range append(b.Effects, b.Instruments...) {
			if c.IDString() == cid {
				return true
			}
		}
		return false
	}
	for _, unlisted := range []bool{false, true} {
		for _, b := range bundles {
			if b.Listed == unlisted || !unlisted && !listed(b) {
				continue
			}
			m, err := Load(b.Path)
			if err != nil {
				if unlisted {
					continue
				}
				return plug.Descriptor{}, err
			}
			if c, ok := m.Find(cid); ok {
				return m.Register(c)
			}
		}
	}
	return plug.Descriptor{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// plugClass is a class as package plug runs it: the plugins of it made
// to sound, which write its parameters' values to show.
type plugClass struct {
	// open makes a plugin of the class, to sound.
	open func(cfg Config) (*Plugin, error)
	// class is the class, for its presets.
	class Class

	mu    sync.Mutex
	live  []*plugged
	texts map[textKey]string
}

type textKey struct {
	id uint32
	v  float64
}

// maxTexts bounds the values written kept, of a class.
const maxTexts = 4096

// formatter returns the Format of parameter id: as a plugin of the
// class playing writes it, after its units where it is smooth, or as a
// number of units where none plays. A value of a parameter of steps,
// as a list's, is its name alone.
func (cl *plugClass) formatter(id uint32, units string, stepped bool) func(v float64) string {
	return func(v float64) string {
		cl.mu.Lock()
		s, ok := cl.texts[textKey{id, v}]
		var p *Plugin
		if len(cl.live) > 0 {
			p = cl.live[0].playing.Load()
		}
		cl.mu.Unlock()
		if ok {
			return s
		}
		if p != nil {
			if s = p.Text(id, v); s != "" {
				if units != "" && !stepped && !strings.HasSuffix(s, units) {
					s += " " + units
				}
				cl.mu.Lock()
				if len(cl.texts) >= maxTexts || cl.texts == nil {
					cl.texts = map[textKey]string{}
				}
				cl.texts[textKey{id, v}] = s
				cl.mu.Unlock()
				return s
			}
		}
		if units == "" {
			return fmt.Sprintf("%.2f", v)
		}
		return fmt.Sprintf("%.2f %s", v, units)
	}
}

func (cl *plugClass) newPlugin() *plugged {
	a := &plugged{cl: cl}
	a.proc.a = a
	return a
}

// plugged is a VST3 plugin behind package plug's interfaces: a
// [plug.Plugin], whose processor is a [plug.Processor] and a
// [plug.Reporter], and a [plug.Editor]. It makes the VST3 plugin as
// its processor is first prepared, so one made for views alone loads
// nothing; until then it keeps the state it is given.
type plugged struct {
	cl   *plugClass
	proc plugProc

	// playing is the plugin made, once prepared.
	playing atomic.Pointer[Plugin]

	mu sync.Mutex
	// state is the state given before the plugin is made, given the
	// state State last gave, or SetState last set, and initial the
	// state the plugin had as it was made.
	state, given, initial []byte
	err                   error
}

var (
	_ plug.Plugin       = (*plugged)(nil)
	_ plug.Editor       = (*plugged)(nil)
	_ plug.Embedder     = (*plugged)(nil)
	_ plug.PresetLister = (*plugged)(nil)
	_ plug.Reporter     = (*plugProc)(nil)
)

// Processor implements [plug.Plugin].
func (a *plugged) Processor() plug.Processor { return &a.proc }

// State implements [plug.Plugin]: the plugin's state, or the one it was
// given before it was made.
func (a *plugged) State() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.playing.Load()
	if p == nil {
		return a.state
	}
	st, err := p.State()
	if err != nil {
		a.err = err
		return a.given
	}
	a.given = st
	return st
}

// SetState implements [plug.Plugin]. The state State last gave, or
// SetState last set, the plugin has already: setting it again sets
// nothing, so a host that keeps the state an editor changed and hands
// it back leaves the plugin playing on undisturbed. No state sets the
// state the plugin started with.
func (a *plugged) SetState(b []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.playing.Load()
	if p == nil {
		a.state = bytes.Clone(b)
		return nil
	}
	if len(b) == 0 {
		// No state is the state it started with, as a Go plugin takes
		// none as its defaults: an undo back past a plugin's first edit
		// sets none.
		b = a.initial
	}
	if len(b) == 0 || bytes.Equal(b, a.given) {
		return nil
	}
	if err := p.SetState(b); err != nil {
		return err
	}
	a.given = bytes.Clone(b)
	return nil
}

// Close implements [plug.Plugin].
func (a *plugged) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.playing.Swap(nil)
	if p == nil {
		return
	}
	cl := a.cl
	cl.mu.Lock()
	for i, x := range cl.live {
		if x == a {
			cl.live = append(cl.live[:i], cl.live[i+1:]...)
			break
		}
	}
	cl.mu.Unlock()
	p.Close()
}

// Err returns why the plugin would not load as its processor was
// prepared, or its state would not read, or that it failed to process
// blocks, played as silence; or nil.
func (a *plugged) Err() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	if p := a.playing.Load(); p != nil && p.Failures() > 0 {
		return fmt.Errorf("vst3: %s failed to process %d blocks, played as silence", p.Class.Name, p.Failures())
	}
	return nil
}

// Plugin returns the VST3 plugin playing, or nil before the processor
// is prepared.
func (a *plugged) Plugin() *Plugin { return a.playing.Load() }

// HasEditor implements [plug.Editor].
func (a *plugged) HasEditor() bool {
	p := a.playing.Load()
	return p != nil && p.HasEditor()
}

// errNotPlaying is the error of an editor opened on a plugin that plays
// nowhere yet.
var errNotPlaying = errors.New("vst3: the plugin plays nowhere yet, so its editor has nothing to edit")

// OpenEditor implements [plug.Editor].
func (a *plugged) OpenEditor(title string) error {
	p := a.playing.Load()
	if p == nil {
		return errNotPlaying
	}
	return p.OpenEditor(title)
}

// Presets implements [plug.PresetLister]: the class's preset files.
func (a *plugged) Presets() []plug.Preset { return ClassPresets(a.cl.class) }

// PresetOf implements [plug.PresetLister]: a preset file names no state
// it is cheap to know again, so none.
func (a *plugged) PresetOf([]byte) (plug.Preset, bool) { return plug.Preset{}, false }

// Class returns the VST3 class the plugin is of.
func (a *plugged) Class() Class { return a.cl.class }

// EmbedEditor implements [plug.Embedder].
func (a *plugged) EmbedEditor(parent uintptr, sized func(w, h int)) (w, h int, err error) {
	p := a.playing.Load()
	if p == nil {
		return 0, 0, errNotPlaying
	}
	return p.EmbedEditor(parent, sized)
}

// EditorEmbedded implements [plug.Embedder].
func (a *plugged) EditorEmbedded() bool {
	p := a.playing.Load()
	return p != nil && p.EditorEmbedded()
}

// EditorSize implements [plug.Embedder].
func (a *plugged) EditorSize() (w, h int, ok bool) {
	if p := a.playing.Load(); p != nil {
		return p.EditorSize()
	}
	return 0, 0, false
}

// PlaceEditor implements [plug.Embedder].
func (a *plugged) PlaceEditor(r image.Rectangle) (w, h int) {
	if p := a.playing.Load(); p != nil {
		return p.PlaceEditor(r)
	}
	return 0, 0
}

// CloseEditor implements [plug.Editor].
func (a *plugged) CloseEditor() {
	if p := a.playing.Load(); p != nil {
		p.CloseEditor()
	}
}

// EditorOpen implements [plug.Editor].
func (a *plugged) EditorOpen() bool {
	p := a.playing.Load()
	return p != nil && p.EditorOpen()
}

// Edits implements [plug.Editor].
func (a *plugged) Edits() uint64 {
	if p := a.playing.Load(); p != nil {
		return p.Edits()
	}
	return 0
}

// Editing implements [plug.Editor].
func (a *plugged) Editing() bool {
	p := a.playing.Load()
	return p != nil && p.Editing()
}

// Values implements [plug.Editor].
func (a *plugged) Values() map[uint32]float64 {
	if p := a.playing.Load(); p != nil {
		return p.Values()
	}
	return nil
}

// plugProc is a plugged's processor.
type plugProc struct{ a *plugged }

// Prepare implements [plug.Processor]: the first makes the VST3 plugin,
// of the state given before; a later one of another setup sets it up
// again.
func (pr *plugProc) Prepare(s plug.Setup) {
	a := pr.a
	cfg := Config{Rate: int(math.Round(s.Rate)), Block: s.MaxBlock, Channels: min(max(s.Channels, 1), 2), Offline: s.Offline}
	a.mu.Lock()
	defer a.mu.Unlock()
	if p := a.playing.Load(); p != nil {
		if err := p.Configure(cfg); err != nil {
			a.err = err
		}
		return
	}
	p, err := a.cl.open(cfg)
	if err != nil {
		a.err = err
		return
	}
	if st, err := p.State(); err == nil {
		a.initial, a.given = st, st
	}
	if len(a.state) > 0 {
		if err := p.SetState(a.state); err != nil {
			a.err = err
		} else {
			a.given = a.state
		}
	}
	a.state = nil
	a.playing.Store(p)
	cl := a.cl
	cl.mu.Lock()
	cl.live = append(cl.live, a)
	cl.mu.Unlock()
}

// Process implements [plug.Processor]: silence before the plugin is
// made.
func (pr *plugProc) Process(b *plug.Block) {
	p := pr.a.playing.Load()
	if p == nil {
		silence(b, 0)
		return
	}
	p.ProcessBlock(b)
}

// Latency implements [plug.Processor].
func (pr *plugProc) Latency() int {
	if p := pr.a.playing.Load(); p != nil {
		return p.Latency()
	}
	return 0
}

// Tail implements [plug.Processor].
func (pr *plugProc) Tail() int {
	if p := pr.a.playing.Load(); p != nil {
		return p.Tail()
	}
	return 0
}

// Reset implements [plug.Processor], on the audio's goroutine.
func (pr *plugProc) Reset() {
	if p := pr.a.playing.Load(); p != nil {
		p.resetNow()
	}
}

// Report implements [plug.Reporter]: a ReadOnly parameter's value as the
// plugin last reported it.
func (pr *plugProc) Report(id uint32) float64 {
	if p := pr.a.playing.Load(); p != nil {
		return p.Value(id)
	}
	return 0
}
