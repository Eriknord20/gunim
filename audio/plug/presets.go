package plug

import "strings"

// A Preset is a sound a plugin can take: a state of its own, filed
// under a bank and a group there, as the host's browser of presets
// lists it. The host plays one by setting its state, as one step of
// its history.
type Preset struct {
	// Bank is where it comes from, as a plugin's own library, its
	// factory programs, or the presets the user kept; Group is the group
	// it is filed under there, and Name its name in the group.
	Bank, Group, Name string
	// About says what it sounds like, which a search reads too.
	About string
	// Pitch is the note a preview of it plays, as a kick of a drum kit
	// does; 0 plays middle C.
	Pitch float64
	// Drums marks a drum kit.
	Drums bool
	// State returns the plugin's state that plays it, for
	// [Plugin.SetState]; it is read as the preset is picked.
	State func() ([]byte, error)
}

// Same reports whether p is q: the same name, filed in the same place.
func (p Preset) Same(q Preset) bool {
	return p.Bank == q.Bank && p.Group == q.Group && p.Name == q.Name
}

// Matches reports whether p has every word of words, lower case, in its
// name, its group or what it sounds like, in any case.
func (p Preset) Matches(words []string) bool {
	hay := strings.ToLower(p.Name + " " + p.Group + " " + p.About)
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// A PresetLister is a plugin that offers presets of its own, which the
// host's browser lists beside the presets the host keeps for it. Its
// methods are safe from any goroutine but the audio's.
type PresetLister interface {
	// Presets returns the presets, bank by bank and each bank group by
	// group, in the order a browser lists them.
	Presets() []Preset
	// PresetOf returns the preset state was picked from, as far as the
	// plugin can tell, or false where it can tell of none: the browser
	// marks it as the one playing. It runs as the browser paints, so a
	// host keeps what it said while state stays as it was.
	PresetOf(state []byte) (p Preset, ok bool)
}
