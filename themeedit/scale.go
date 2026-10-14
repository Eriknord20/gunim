package themeedit

import (
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/theme"
)

// MaxScale is the longest the Animations slider makes every animation:
// five times as long as made.
const MaxScale = 5

// ScaleField is the field for [theme.Scale], how long every animation
// takes, which the editor puts first under Motion on the first tab, or
// in a Motion section of its own after the program's, unless the
// program places it itself.
var ScaleField = Field{
	Key: theme.Scale.Key(), Label: "Animations",
	Detail: "How long every animation takes: 0 is off, 1 as made, 5 five times as long.",
	Min:    0, Max: MaxScale, Step: 0.05,
}

// withScale returns sections with [ScaleField] added, where none of
// them has it. With no sections, All values has it, as every token.
func withScale(sections []Section) []Section {
	if len(sections) == 0 {
		return sections
	}
	for _, s := range sections {
		for _, f := range s.Fields {
			if f.Key == ScaleField.Key {
				return sections
			}
		}
	}
	out := make([]Section, len(sections), len(sections)+1)
	copy(out, sections)
	for i, s := range out {
		if s.Title == "Motion" {
			out[i].Fields = append([]Field{ScaleField}, s.Fields...)
			return out
		}
	}
	return append(out, Section{Title: "Motion", Fields: []Field{ScaleField}})
}

// paced returns th with every motion it gives, or leaves at its
// default, by times as long. The program runs every animation at the
// scale it saved; by times that scale is the one edited, so the
// preview, which wears th, moves at the scale edited.
func paced(th theme.Theme, by float64) theme.Theme {
	if by == 1 {
		return th
	}
	for _, info := range theme.Tokens() {
		if info.Kind != theme.KindSpring {
			continue
		}
		v, ok := th.Value(info.Key)
		if !ok {
			v = info.Default
		}
		s, ok := v.(anim.Spring)
		if !ok {
			continue
		}
		s.Response *= float32(by)
		if over, err := th.WithValue(info.Key, s); err == nil {
			th = over
		}
	}
	return th
}
