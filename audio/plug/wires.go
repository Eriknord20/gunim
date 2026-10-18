package plug

import "image/color"

// MainIn names a plugin's main sound in, where [WiresHost.Feeds] takes
// the place of an aux input: the sound its chain gives it.
const MainIn = -1

// Feed is what feeds one of a plugin's inputs, as the host names it.
type Feed struct {
	// Name is what the host calls it: "Hej Drums · Snare" for a synth's
	// output, or a track's name.
	Name string
	// Color is its colour in the host, as a track's, or zero where it
	// has none.
	Color color.NRGBA
}

// WiresHost is a [Host] that says what feeds the plugin's inputs, for
// a view to name its parts by what they hear, as a drum mixer names a
// lane "Hej Drums · Snare" in place of "Snare top". The processor
// knows only whether an input is connected, as one with nothing
// connected comes as nil; a view knows nothing of it without this. A
// view asks for it by a type assertion on its Host, and may ask as
// often as it draws.
type WiresHost interface {
	Host
	// Feeds returns what feeds aux input i, by its place in
	// [Descriptor.AuxIn], or the main input for i of [MainIn], in the
	// host's order: the first is the one to name the input by. It is
	// empty where nothing does.
	Feeds(i int) []Feed
}
