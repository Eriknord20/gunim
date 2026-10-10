// Package theme controls how widgets look and move, and animates every
// value when the theme changes.
//
// A widget declares each value it wants themed as a [Token], with a
// default: a colour, a length, a padding, a font size, even the spring
// its hover animates with. A [Theme] gives tokens values; anything it
// leaves out keeps its default. A window keeps one animated value per
// token, in a [Live], and switching themes retargets them all, so a
// switch from dark to light moves colours, paddings, radii and motion
// together.
//
// For that to work, a widget reads tokens every time it lays out or
// paints, never copying one into a field. The rule of thumb is to
// animate state and look up style: a button animates how hovered it is,
// from 0 to 1, and blends the theme's idle and hover colours by that
// each frame.
//
//	var ButtonFill = theme.Color("button.fill", color.NRGBA{...})
//
//	fill := ButtonFill.Get(f.Theme)
package theme

import (
	"fmt"
	"image/color"
	"reflect"
	"sync"
	"time"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
)

// A Token names one themeable value of type T and gives its default.
// Declare tokens once, as package variables, next to the widget that
// reads them.
type Token[T any] struct {
	key   string
	def   T
	codec anim.Codec[T]
	// blend, when set, draws the value part way through a switch from
	// its progress, in place of animating the value itself.
	blend func(from, to T, p float32) T
}

var (
	keysMu sync.Mutex
	keys   = map[string]declared{}
)

// declared is a token's type, default and kind, kept for [Declared]
// and [Lookup].
type declared struct {
	typ  reflect.Type
	def  any
	kind Kind
}

// New declares a token with its own codec. The key names it within a
// [Theme], and must be unique; two tokens with one key and different
// types panic. Its kind is [KindOther].
func New[T any](key string, def T, c anim.Codec[T]) Token[T] {
	return declare(key, def, c, KindOther)
}

// declare declares a token of kind k.
func declare[T any](key string, def T, c anim.Codec[T], k Kind) Token[T] {
	t := reflect.TypeFor[T]()
	keysMu.Lock()
	defer keysMu.Unlock()
	if prev, ok := keys[key]; ok && prev.typ != t {
		panic(fmt.Sprintf("theme: %q is already a %s token", key, prev.typ))
	}
	keys[key] = declared{typ: t, def: def, kind: k}
	return Token[T]{key: key, def: def, codec: c}
}

// Color declares a colour token.
func Color(key string, def color.NRGBA) Token[color.NRGBA] {
	return declare(key, def, anim.ColorCodec, KindColor)
}

// Foreground declares a colour token for what is drawn on top of other
// colours, such as text.
//
// When a switch swaps light and dark, text and its background must
// pass a moment where they are equally bright, and text drawn there
// smears into its background. A foreground token fades out in its old
// colour and back in with its new one instead, so the crossing happens
// while it is out of sight.
func Foreground(key string, def color.NRGBA) Token[color.NRGBA] {
	t := declare(key, def, anim.ColorCodec, KindForeground)
	t.blend = fadeThrough
	return t
}

// fadeThrough draws from fading out over the first half of p and to
// fading in over the second.
func fadeThrough(from, to color.NRGBA, p float32) color.NRGBA {
	fade := func(c color.NRGBA, v float32) color.NRGBA {
		v = max(0, min(v, 1))
		c.A = uint8(float32(c.A)*v*v*(3-2*v) + 0.5)
		return c
	}
	if p < 0.5 {
		return fade(from, 1-2*p)
	}
	return fade(to, 2*p-1)
}

// Length declares a length token in logical pixels: a radius, a gap, a
// font size.
func Length(key string, def float32) Token[float32] {
	return declare(key, def, anim.FloatCodec, KindLength)
}

// Number declares a plain number token: a strength, a share, a count.
func Number(key string, def float32) Token[float32] {
	return declare(key, def, anim.FloatCodec, KindNumber)
}

// Insets declares a padding or margin token.
func Insets(key string, def geom.Insets) Token[geom.Insets] {
	return declare(key, def, anim.InsetsCodec, KindInsets)
}

// Spring declares a motion token.
func Spring(key string, def anim.Spring) Token[anim.Spring] {
	return declare(key, def, anim.SpringCodec, KindSpring)
}

// Choice declares a token for a value that cannot blend, such as a
// font: a switch changes it whole, halfway through.
func Choice[T any](key string, def T) Token[T] {
	t := declare(key, def, anim.Codec[T]{}, KindChoice)
	t.blend = func(from, to T, p float32) T {
		if p < 0.5 {
			return from
		}
		return to
	}
	return t
}

// Declared returns every token declared so far, by key, with its default. A test can walk it to check that a theme
// gives each token a value that suits it.
func Declared() map[string]any {
	keysMu.Lock()
	defer keysMu.Unlock()
	out := make(map[string]any, len(keys))
	for k, d := range keys {
		out[k] = d.def
	}
	return out
}

// Key returns the token's name.
func (t Token[T]) Key() string { return t.key }

// Default returns the value the token has in a theme that leaves it out.
func (t Token[T]) Default() T { return t.def }

// Get returns the token's value in l right now, part way to its target
// while a theme switch is running. A nil Live gives the default.
//
// In a scope, a token the scope's theme leaves out comes from the theme
// around it, and moves with it.
func (t Token[T]) Get(l *Live) T {
	if l == nil {
		return t.def
	}
	s, ok := l.slots[t.key]
	if !ok {
		if l.parent != nil && !l.active.has(t.key) {
			// Remember the token, so a later switch to a theme that sets
			// it starts from the value it shows now.
			l.reads[t.key] = func() stepper { return t.slotAt(t.Get(l.parent)) }
			return t.Get(l.parent)
		}
		s = t.slotAt(t.in(l.active))
		l.slots[t.key] = s
	}
	typed, ok := s.(valuer[T])
	if !ok {
		panic(fmt.Sprintf("theme: %q read as the wrong type", t.key))
	}
	return typed.value()
}

// target is where t is heading in th: th's value, or else the theme
// around it, or else the default.
func (t Token[T]) target(th Theme, parent *Live) T {
	if v, ok := th.values[t.key]; ok {
		if typed, ok := v.(T); ok {
			return typed
		}
	}
	if parent != nil {
		return t.Get(parent)
	}
	return t.def
}

// in returns the token's value in th, or its default.
func (t Token[T]) in(th Theme) T {
	if v, ok := th.values[t.key]; ok {
		if typed, ok := v.(T); ok {
			return typed
		}
	}
	return t.def
}

// A Theme is a named set of token values.
type Theme struct {
	Name   string
	values map[string]any
}

// Has reports whether th gives the token named key a value.
func (th Theme) Has(key string) bool { return th.has(key) }

func (th Theme) has(key string) bool {
	_, ok := th.values[key]
	return ok
}

// With returns a copy of th with entries added, replacing any value th
// already gives the same token. It is how an application adds its own
// tokens to a widget library's theme.
//
//	light := widget.Light().With(theme.Set(Background, white))
func (th Theme) With(entries ...Entry) Theme {
	out := Theme{Name: th.Name, values: make(map[string]any, len(th.values)+len(entries))}
	for k, v := range th.values {
		out.values[k] = v
	}
	for _, e := range entries {
		out.values[e.key] = e.value
	}
	return out
}

// An Entry is one token's value in a theme, made by [Set].
type Entry struct {
	key   string
	value any
}

// Set gives token t the value v.
func Set[T any](t Token[T], v T) Entry { return Entry{key: t.key, value: v} }

// Make builds a theme from entries. A later entry for the same token
// wins.
func Make(name string, entries ...Entry) Theme {
	th := Theme{Name: name, values: make(map[string]any, len(entries))}
	for _, e := range entries {
		th.values[e.key] = e.value
	}
	return th
}

// Switch is the motion a theme switch runs with. A theme can set it too:
// the new theme's value is the one used.
var Switch = Spring("theme.switch", anim.Spring{Response: 0.6, Damping: 1})

// A Live is the theme in force in one window, or in one part of it:
// every token read so far, each an animated value. It belongs to the
// window's UI goroutine.
type Live struct {
	active Theme
	slots  map[string]stepper
	// parent is the theme around a scope, and nil for a window's own.
	parent *Live
	// reads holds the tokens a scope has passed on to its parent, each
	// with a way to give it a value of its own.
	reads map[string]func() stepper
}

type stepper interface {
	step(dt time.Duration) bool
	// retarget starts moving toward th's value, or the parent's.
	retarget(th Theme, parent *Live, m anim.Motion)
	// follow keeps heading for th's value, or the parent's, as the
	// parent's moves, without starting over.
	follow(th Theme, parent *Live, m anim.Motion)
	moving() bool
}

type valuer[T any] interface {
	value() T
}

// slotAt returns a live value for t, at rest at v.
func (t Token[T]) slotAt(v T) stepper {
	if t.blend != nil {
		return &blendSlot[T]{token: t, from: v, to: v, p: anim.NewFloat(1)}
	}
	return &slot[T]{a: anim.New(v, t.codec), token: t}
}

// slot animates a token's value itself.
type slot[T any] struct {
	a     *anim.Animated[T]
	token Token[T]
}

func (s *slot[T]) step(dt time.Duration) bool { return s.a.Step(dt) }

func (s *slot[T]) retarget(th Theme, parent *Live, m anim.Motion) {
	s.a.Animate(s.token.target(th, parent), m)
}

func (s *slot[T]) follow(th Theme, parent *Live, m anim.Motion) { s.retarget(th, parent, m) }

func (s *slot[T]) moving() bool { return s.a.Active() }

func (s *slot[T]) value() T { return s.a.Value() }

// blendSlot animates a switch's progress, and draws the token's value
// from it with the token's blend.
type blendSlot[T any] struct {
	token    Token[T]
	from, to T
	p        *anim.Float
}

func (s *blendSlot[T]) step(dt time.Duration) bool { return s.p.Step(dt) }

func (s *blendSlot[T]) retarget(th Theme, parent *Live, m anim.Motion) {
	to := s.token.target(th, parent)
	if reflect.DeepEqual(to, s.to) {
		// Going where it goes already. Starting again would blend the
		// value into itself, and a blend that fades through, as a
		// foreground does, would dip the text and bring it back.
		return
	}
	s.from, s.to = s.value(), to
	s.p.Jump(0)
	s.p.Animate(1, m)
}

func (s *blendSlot[T]) follow(th Theme, parent *Live, _ anim.Motion) {
	s.to = s.token.target(th, parent)
}

func (s *blendSlot[T]) moving() bool { return s.p.Active() }

func (s *blendSlot[T]) value() T { return s.token.blend(s.from, s.to, min(s.p.Value(), 1)) }

// NewLive returns the live form of th, at rest.
func NewLive(th Theme) *Live {
	return &Live{active: th, slots: map[string]stepper{}, reads: map[string]func() stepper{}}
}

// Under makes l a scope within parent: tokens l's theme leaves out come
// from parent. The engine calls it every frame for a node that gives its
// subtree a theme.
func (l *Live) Under(parent *Live) { l.parent = parent }

// Active returns the theme the values are heading for.
func (l *Live) Active() Theme { return l.active }

// Sets reports whether the theme in use, or one around it, sets key,
// for a token that falls back to another where none does.
func (l *Live) Sets(key string) bool {
	for ; l != nil; l = l.parent {
		if l.active.has(key) {
			return true
		}
	}
	return false
}

// motion returns the motion a switch to th runs with: th's own, or the
// one around it.
func (l *Live) motion(th Theme) anim.Motion {
	return Switch.target(th, l.parent)
}

// Use switches to th, animating every token from where it is now. Tokens
// first read after the switch start at th's values. In a scope, a token
// th stops setting glides to the value around it, and one th starts
// setting glides from there.
func (l *Live) Use(th Theme) {
	l.active = th
	m := l.motion(th)
	for _, s := range l.slots {
		s.retarget(th, l.parent, m)
	}
	for key, mk := range l.reads {
		if _, ok := l.slots[key]; !ok && th.has(key) {
			s := mk()
			s.retarget(th, l.parent, m)
			l.slots[key] = s
		}
	}
}

// Step advances every value by dt and reports whether any is still
// moving. The engine calls it once a frame.
//
// In a scope, a token the scope's theme leaves out follows the value
// around it until it gets there, then reads straight through again.
func (l *Live) Step(dt time.Duration) bool {
	moving := false
	m := l.motion(l.active)
	for key, s := range l.slots {
		inherited := l.parent != nil && !l.active.has(key)
		if inherited {
			s.follow(l.active, l.parent, m)
		}
		if s.step(dt) {
			moving = true
		}
		if inherited && !s.moving() {
			delete(l.slots, key)
		}
	}
	return moving
}
