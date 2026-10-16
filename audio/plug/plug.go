// Package plug is a plugin format for audio effects and instruments
// written in Go: a plugin is a Go package, compiled into the app that
// uses it, which registers itself as it starts. An effect changes the
// sound going in; an instrument plays notes, and its sound comes out of
// it alone; a generator makes control alone, as an LFO.
//
// It follows VST3 where VST3 works well, so an adapter can run a VST3
// plugin through the same interfaces: parameters declared up front,
// sound processed in blocks with parameter changes and notes at frames
// within them, ports beyond the main sound as VST3's buses, and a
// latency and a tail. It parts from VST3 in three places.
// The host owns the parameters and the state, so it keeps one undo
// history and one preset browser for every plugin. A plugin's views are
// gunim nodes the host places where it likes, in three sizes. And a
// plugin may read whole tracks, as ARA 2 lets one, through
// [TrackPlugin].
//
// The package is young: its shape still moves as the programs that
// host it grow.
package plug

import (
	"fmt"
	"image/color"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
)

// Descriptor is what a plugin is, as the host lists it before making
// one.
type Descriptor struct {
	// ID tells the plugin from every other, for good: projects and
	// presets name it by its ID. Write it as a reversed domain:
	// "marrasen.braid".
	ID      string
	Name    string
	Vendor  string
	Version string
	// Kind is what it does, in VST3's categories, so a browser sorts
	// both kinds alike: "Fx|EQ", or "Instrument|Synth". One in the
	// Instrument category is an instrument, as [Descriptor.Instrument]
	// reports.
	Kind string
	// Tagline says what the plugin does in a few words, for a browser to
	// show under its name: "Gain & stereo".
	Tagline string
	// Keywords are words someone looking for the plugin might type,
	// past its name and kind: "mono" and "phase" find a gain and stereo
	// tool. A browser searches them, and shows none.
	Keywords []string
	// Notes says the plugin takes notes, in [Block.Events]. An
	// instrument does.
	Notes bool
	// LookAhead is how long before a note starts the plugin asks to hear
	// of it, for an instrument that sounds ahead of its notes: a swell
	// into the beat, a breath, a grace note, a glide that arrives on
	// time. The host tells it of each note it plays from the music, as
	// the note comes within LookAhead, in [Block.Upcoming]. Zero asks
	// for none. It needs Notes.
	LookAhead time.Duration
	// AuxIn and AuxOut are the plugin's audio ports beyond its main
	// input and output, as VST3's aux (auxiliary) buses: a sidechain
	// in, or an output for each drum of a drum synth. A project names a
	// port by its place in the list, as VST3 names a bus by its index,
	// so a plugin adds new ports at the end. [Block.AuxIn] and
	// [Block.AuxOut] carry their sound.
	AuxIn, AuxOut []AudioPort
	// ControlIn and ControlOut are the plugin's control ports: one value
	// a frame, in the blocks the sound comes in, as an LFO puts out.
	// They are named by their place in the list too, and
	// [Block.ControlIn] and [Block.ControlOut] carry their values.
	ControlIn, ControlOut []ControlPort
	// Ports, where set, says which of the ports above a plugin has in
	// a state, as a synth has an output for each part of the drum kit
	// it plays, and only its main output for a melodic sound. The ports
	// keep their places in the lists, so a project's connections keep
	// naming them, and a port a state lacks comes back with a state
	// that has it. The host gives a port the plugin lacks nothing, as it
	// gives a port left unconnected: the plugin mixes such an output
	// into its main one. Ports is a pure function of the state, as
	// [Plugin.State] writes it: the host asks it again whenever the
	// state changes, as SetState sets one or a view tells of an edit with
	// [Host.Changed], and may ask it from any goroutine. Nil gives a
	// plugin every port in every state. [Descriptor.PortsIn] reads it.
	Ports func(state []byte) Ports
	// Params are the plugin's parameters, each with an ID of its own.
	Params []Param
	// Patches are the patches the plugin comes with, as starting points.
	// The host keeps those a user saves beside them.
	Patches []Patch
	// New makes one of the plugin, working for h.
	New func(h Host) Plugin
}

// Instrument reports whether d is an instrument: its Kind is in VST3's
// Instrument category, as "Instrument|Synth". An instrument plays the
// notes it takes, and its sound comes out of it alone: the host gives
// it blocks whose In is nil.
func (d *Descriptor) Instrument() bool {
	category, _, _ := strings.Cut(d.Kind, "|")
	return category == "Instrument"
}

// Generator reports whether d is a generator: its Kind is in the
// Generator category, as "Generator|LFO", one plug adds beside VST3's.
// A generator makes control alone, on its [Descriptor.ControlOut]
// ports: the host gives it blocks whose In and Out are nil, and places
// it in no chain of sound.
func (d *Descriptor) Generator() bool {
	category, _, _ := strings.Cut(d.Kind, "|")
	return category == "Generator"
}

// Param returns the parameter id, and whether d has one.
func (d *Descriptor) Param(id uint32) (Param, bool) {
	for _, p := range d.Params {
		if p.ID == id {
			return p, true
		}
	}
	return Param{}, false
}

// Ports says which ports a plugin has in a state, of those its
// [Descriptor] declares, by their places in its lists: true for each
// port it has. A port past the end of a list here is one it lacks.
type Ports struct {
	AuxIn, AuxOut, ControlIn, ControlOut []bool
}

// PortsIn returns the ports a plugin of d has in state: what d.Ports
// says, within the ports d declares, or every port d declares where
// d.Ports is nil.
func (d *Descriptor) PortsIn(state []byte) Ports {
	all := func(n int) []bool {
		out := make([]bool, n)
		for i := range out {
			out[i] = true
		}
		return out
	}
	if d.Ports == nil {
		return Ports{AuxIn: all(len(d.AuxIn)), AuxOut: all(len(d.AuxOut)), ControlIn: all(len(d.ControlIn)), ControlOut: all(len(d.ControlOut))}
	}
	p := d.Ports(state)
	within := func(has []bool, n int) []bool {
		out := make([]bool, n)
		copy(out, has)
		return out
	}
	return Ports{AuxIn: within(p.AuxIn, len(d.AuxIn)), AuxOut: within(p.AuxOut, len(d.AuxOut)),
		ControlIn: within(p.ControlIn, len(d.ControlIn)), ControlOut: within(p.ControlOut, len(d.ControlOut))}
}

// has reports whether list holds true at i.
func has(list []bool, i int) bool { return i >= 0 && i < len(list) && list[i] }

// HasAuxIn reports whether the plugin has aux input i.
func (p Ports) HasAuxIn(i int) bool { return has(p.AuxIn, i) }

// HasAuxOut reports whether the plugin has aux output i.
func (p Ports) HasAuxOut(i int) bool { return has(p.AuxOut, i) }

// HasControlIn reports whether the plugin has control input i.
func (p Ports) HasControlIn(i int) bool { return has(p.ControlIn, i) }

// HasControlOut reports whether the plugin has control output i.
func (p Ports) HasControlOut(i int) bool { return has(p.ControlOut, i) }

// AudioPort is an audio port of a plugin's, beyond its main input and
// output.
type AudioPort struct {
	// Name is what the host shows it as: "Sidechain", or "Kick".
	Name string
	// Channels is how many channels it has. Zero gives it as many as the
	// main sound, [Setup.Channels].
	Channels int
	// Group puts the port on one row with the plugin's other ports of
	// the same Group, under the Group as a label, where the host draws
	// ports, as a light's Red, Green and Blue on a row named "Colour".
	// Empty gives the port a row of its own.
	Group string
	// Color is the port's colour, and the colour of the wires from it.
	// The host may make it lighter or darker to stand out from what it
	// is drawn on. Zero leaves it to the host.
	Color color.NRGBA
}

// ControlPort is a control port of a plugin's: a value a frame, within
// a range.
type ControlPort struct {
	// Name is what the host shows it as: "Cutoff", or "LFO".
	Name string
	// Unit is what the value counts, as "Hz", or empty for a plain
	// number.
	Unit string
	// Min and Max bound the values: an input's, as the host maps a
	// signal into it, and an output's, as the plugin writes them.
	// Default is what an input left unconnected stands for.
	Min, Max, Default float64
	// Group puts the port on one row with the plugin's other ports of
	// the same Group, under the Group as a label, where the host draws
	// ports, as a light's Red, Green and Blue on a row named "Colour".
	// Empty gives the port a row of its own.
	Group string
	// Color is the port's colour, and the colour of the wires from it.
	// The host may make it lighter or darker to stand out from what it
	// is drawn on. Zero leaves it to the host.
	Color color.NRGBA
}

// Param is one of a plugin's parameters: a value the host keeps, shows,
// automates and undoes.
type Param struct {
	// ID tells the parameter from the plugin's others, for good: a
	// parameter gone keeps its ID unused.
	ID   uint32
	Name string
	// Unit is what the value counts, as "dB" or "Hz".
	Unit string
	// Min and Max bound the value, and Default is where it starts.
	Min, Max, Default float64
	// Steps is how many steps the value takes from Min to Max: one is
	// a switch, and zero is a smooth value.
	Steps int
	// Log runs the value by ratios rather than steps, as a time or a
	// frequency: halfway from Min to Max, in Normal, is the geometric
	// mean of the two. Min must be above zero.
	Log   bool
	Flags ParamFlags
	// Format writes a value to show. Nil writes it with Unit.
	Format func(v float64) string
	// Group and Color are the parameter's input, where the host draws
	// one for wires to drive it, as [ControlPort.Group] and
	// [ControlPort.Color] are a port's.
	Group string
	Color color.NRGBA
}

// ParamFlags say how the host treats a parameter.
type ParamFlags uint8

// The flags.
const (
	// Automatable lets the host change the value over time.
	Automatable ParamFlags = 1 << iota
	// Bypass marks the parameter that passes the sound by the plugin.
	// The host's own bypass sets it, and the plugin fades as it likes.
	Bypass
	// Hidden keeps the parameter off the host's generic views.
	Hidden
	// ReadOnly marks a value the plugin reports, as a meter's, which
	// the host shows but never sets: its processor reports it, as a
	// [Reporter], and a view reads it with [Host.Param] as often as it
	// draws.
	ReadOnly
)

// Clamp returns v within the parameter's range, on a step where it has
// steps. A NaN, as a file or a modulation may bring, is the Default,
// and an infinity the end of the range it points to.
func (p Param) Clamp(v float64) float64 {
	if math.IsNaN(v) {
		v = p.Default
	}
	v = min(max(v, p.Min), p.Max)
	if p.Steps > 0 {
		return p.Plain(p.Normal(v))
	}
	return v
}

// Normal returns v as VST3 carries a value: from 0 at Min to 1 at Max,
// on a step where the parameter has steps.
func (p Param) Normal(v float64) float64 {
	if p.Max <= p.Min {
		return 0
	}
	n := min(max((v-p.Min)/(p.Max-p.Min), 0), 1)
	if p.Log && p.Min > 0 {
		n = min(max(math.Log(max(v, p.Min)/p.Min)/math.Log(p.Max/p.Min), 0), 1)
	}
	if p.Steps > 0 {
		n = math.Round(n*float64(p.Steps)) / float64(p.Steps)
	}
	return n
}

// Plain returns the value at n from 0 to 1, as [Param.Normal] writes it.
func (p Param) Plain(n float64) float64 {
	n = min(max(n, 0), 1)
	if p.Steps > 0 {
		n = math.Round(n*float64(p.Steps)) / float64(p.Steps)
	}
	if p.Log && p.Min > 0 {
		return p.Min * math.Pow(p.Max/p.Min, n)
	}
	return p.Min + n*(p.Max-p.Min)
}

// Text returns v written to show.
func (p Param) Text(v float64) string {
	if p.Format != nil {
		return p.Format(v)
	}
	if p.Unit == "" {
		return fmt.Sprintf("%.2f", v)
	}
	return fmt.Sprintf("%.2f %s", v, p.Unit)
}

// Patch is a plugin's settings, kept by name to load again: its
// parameters' values and its state.
type Patch struct {
	Name     string
	Category string
	Tags     []string
	// Params holds each parameter's value, by ID; one left out is at
	// its default.
	Params map[uint32]float64
	// State is what the plugin's [Plugin.State] gave.
	State []byte
}

// Plugin is one of a plugin, made by its [Descriptor]'s New. Its
// methods run one at a time, on one goroutine: the app's, for one made
// for its sound, or the window's, for one made for its views. Only its
// [Processor]'s Process runs elsewhere.
type Plugin interface {
	// Processor returns the part that runs on the audio thread. It is
	// the same one each time.
	Processor() Processor
	// State returns what the plugin keeps beyond its parameters, as
	// an EQ's bands, for the host to save and to undo. A plugin with
	// nothing more returns nil.
	State() []byte
	// SetState sets what State returned, as a project opens, a preset
	// loads, or an edit is undone. It may run while the processor runs:
	// the plugin hands the change to it safely.
	SetState(b []byte) error
	// Close lets the plugin go. Nothing calls it after.
	Close()
}

// Host is what a plugin's views ask of the app that runs it. Its
// methods run on the window's goroutine, from the views' own: those
// that change something take the [gunim.UI] of the call they are made
// in, to tell the app with.
type Host interface {
	// Param returns a parameter's value now: of a ReadOnly one, what
	// the plugin playing reports, or its default where none plays.
	Param(id uint32) float64
	// Begin, Set and End are one gesture on a parameter, as a knob
	// dragged: Set moves it, and the host passes each move to the
	// processor. The gesture is one step in the host's undo history.
	Begin(id uint32, u *gunim.UI)
	Set(id uint32, v float64, u *gunim.UI)
	End(id uint32, u *gunim.UI)
	// BeginEdit, Changed and EndEdit are one gesture on the plugin's
	// [Plugin.State], as a band dragged: named for the host's history,
	// as "Move band 3", where it is one step. Changed tells the host the
	// state changed, for it to pass on to the sound.
	BeginEdit(name string, u *gunim.UI)
	Changed(u *gunim.UI)
	EndEdit(u *gunim.UI)
	// Sound returns the plugin's sound as it is heard, for views that
	// draw it, as a meter or a spectrum: what is fed into the plugin,
	// and what comes out. Either is nil where the host keeps none.
	Sound() (in, out Sound)
	// Traced returns the value the processor playing keeps for frame f,
	// as a [Tracer], of the ReadOnly parameter id, and whether there is
	// one: for a view to draw along the sound it hears.
	Traced(id uint32, f int64) (v float32, ok bool)
}

// ParamMenuHost is a [Host] with a menu for a parameter, as a DAW's
// "Automate" and "Modulate by". A right click on a control that turns
// a parameter asks for it.
type ParamMenuHost interface {
	// ParamMenu opens the host's menu for parameter id, at pos in the
	// window's logical pixels, and reports whether it opened one.
	ParamMenu(id uint32, pos geom.Point, u *gunim.UI) bool
}

// Modulation is how control moves a parameter as it plays.
type Modulation struct {
	// Low and High are how far the control wired to the parameter
	// reaches below and above its value, along [Param.Normal], each from
	// -1 to 1: an LFO that swings either way of zero reaches both ways,
	// a control from 0 to 1 only one.
	Low, High float64
	// Now is the parameter's value as heard now.
	Now float64
	// Sets says control sets the value, as automation does, so the
	// value the control turns stands aside.
	Sets bool
}

// ModulationHost is a [Host] that says how control moves its
// parameters, for a control to show the value as heard beside its own.
// A view asks it each frame.
type ModulationHost interface {
	// Modulation returns how control moves parameter id, and false
	// where no control does.
	Modulation(id uint32) (Modulation, bool)
}

// Sound is a plugin's sound, as the host keeps it while it plays.
type Sound interface {
	// Heard fills dst, stereo frames interleaved, with the last frames
	// heard, the last of them last, and returns how many it filled, the
	// count of frames heard by then, and their rate. The count moves on
	// as the sound plays and stands still while it does not, so a view
	// that reads as many frames as it moved on reads each frame once.
	// Heard fills none, and returns nothing, where none have been
	// heard. It runs on the window's goroutine, and takes no lock the
	// sound takes.
	Heard(dst []float32) (frames int, at int64, rate int)
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Descriptor{}
)

// Register makes a plugin known, as its package's init does. It panics
// where d is not whole or its ID is already taken, as a mistake in the
// program.
func Register(d Descriptor) {
	if err := check(&d); err != nil {
		panic("plug: " + err.Error())
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := registry[d.ID]; ok {
		panic("plug: Register called twice for " + d.ID)
	}
	registry[d.ID] = d
}

// check returns what is wrong with d, if anything.
func check(d *Descriptor) error {
	if d.ID == "" || d.Name == "" || d.New == nil {
		return fmt.Errorf("plugin %q needs an ID, a name and New", d.ID)
	}
	if d.Instrument() && !d.Notes {
		return fmt.Errorf("%s is an instrument, of kind %q, so it takes notes: set Notes", d.ID, d.Kind)
	}
	if d.LookAhead < 0 || d.LookAhead > 0 && !d.Notes {
		return fmt.Errorf("%s looks ahead %v, which takes Notes and a duration of zero or more", d.ID, d.LookAhead)
	}
	if d.Generator() && len(d.ControlOut) == 0 {
		return fmt.Errorf("%s is a generator, of kind %q, so it makes control: give it a ControlOut port", d.ID, d.Kind)
	}
	if err := checkPorts(d); err != nil {
		return err
	}
	seen := map[uint32]bool{}
	for _, p := range d.Params {
		if seen[p.ID] {
			return fmt.Errorf("%s has two parameters of ID %d", d.ID, p.ID)
		}
		seen[p.ID] = true
		if p.Max <= p.Min || p.Default < p.Min || p.Default > p.Max {
			return fmt.Errorf("%s: parameter %q runs from %g to %g, from %g", d.ID, p.Name, p.Min, p.Max, p.Default)
		}
		if p.Log && p.Min <= 0 {
			return fmt.Errorf("%s: parameter %q runs by ratios from %g, not above zero", d.ID, p.Name, p.Min)
		}
	}
	names := map[string]bool{}
	for _, pt := range d.Patches {
		if pt.Name == "" || names[pt.Name] {
			return fmt.Errorf("%s has a patch named %q twice, or unnamed", d.ID, pt.Name)
		}
		names[pt.Name] = true
		for id := range pt.Params {
			if p, ok := d.Param(id); !ok || p.Flags&ReadOnly != 0 {
				return fmt.Errorf("%s: patch %q sets %d, which is no parameter it may set", d.ID, pt.Name, id)
			}
		}
	}
	return nil
}

// checkPorts returns what is wrong with d's ports, if anything: each
// needs a name of its own among those going its way, an audio port a
// channel count of zero or more, and a control port a range holding its
// default.
func checkPorts(d *Descriptor) error {
	for way, ports := range map[string][]AudioPort{"aux input": d.AuxIn, "aux output": d.AuxOut} {
		names := map[string]bool{}
		for _, p := range ports {
			if p.Name == "" || names[p.Name] {
				return fmt.Errorf("%s has an %s named %q twice, or unnamed", d.ID, way, p.Name)
			}
			names[p.Name] = true
			if p.Channels < 0 {
				return fmt.Errorf("%s: %s %q has %d channels", d.ID, way, p.Name, p.Channels)
			}
		}
	}
	for way, ports := range map[string][]ControlPort{"control input": d.ControlIn, "control output": d.ControlOut} {
		names := map[string]bool{}
		for _, p := range ports {
			if p.Name == "" || names[p.Name] {
				return fmt.Errorf("%s has a %s named %q twice, or unnamed", d.ID, way, p.Name)
			}
			names[p.Name] = true
			if p.Max <= p.Min || p.Default < p.Min || p.Default > p.Max {
				return fmt.Errorf("%s: %s %q runs from %g to %g, from %g", d.ID, way, p.Name, p.Min, p.Max, p.Default)
			}
		}
	}
	return nil
}

// A Finder makes a plugin known by its ID as it is first asked for, as
// a host's VST3 plugins on disk are: it registers the plugin of id, and
// reports whether it did. It remembers an ID it found nothing of, as
// Lookup asks again each time; and it calls neither Lookup nor All.
type Finder func(id string) bool

var (
	findersMu sync.Mutex
	finders   []Finder
)

// AddFinder has Lookup ask f for each plugin none registered, after
// the finders added before.
func AddFinder(f Finder) {
	findersMu.Lock()
	defer findersMu.Unlock()
	finders = append(finders, f)
}

// Lookup returns the plugin registered as id, and whether there is one:
// one registered already, or one a [Finder] registers as it is asked.
func Lookup(id string) (Descriptor, bool) {
	if d, ok := registered(id); ok {
		return d, true
	}
	findersMu.Lock()
	fs := slices.Clone(finders)
	findersMu.Unlock()
	for _, f := range fs {
		if f(id) {
			return registered(id)
		}
	}
	return Descriptor{}, false
}

// registered returns the plugin registered as id, and whether there is
// one.
func registered(id string) (Descriptor, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	d, ok := registry[id]
	return d, ok
}

// All returns every plugin registered, by vendor, then name.
func All() []Descriptor {
	registryMu.RLock()
	out := make([]Descriptor, 0, len(registry))
	for _, d := range registry {
		out = append(out, d)
	}
	registryMu.RUnlock()
	slices.SortFunc(out, func(a, b Descriptor) int {
		if c := strings.Compare(strings.ToLower(a.Vendor), strings.ToLower(b.Vendor)); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}
