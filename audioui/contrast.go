package audioui

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/theme"
)

// Contrast sets the audio pieces' colours for a contrast theme, to add to widget.Contrast's, as Light is added to
// widget.Light. Grounds take the window and caps the button face. What plays takes the window text, and a level near
// its mark and over it take the highlight and the hot-tracking colour, so the three stay apart.
func Contrast(c gunim.ContrastColors) []theme.Entry {
	return []theme.Entry{
		theme.Set(Ground, c.Window),
		theme.Set(Raised, c.ButtonFace),
		theme.Set(Sound, c.WindowText),
		theme.Set(Safe, c.WindowText),
		theme.Set(Near, c.Highlight),
		theme.Set(Over, c.HotLight),
		theme.Set(Spread, c.Highlight),
		theme.Set(Short, c.HotLight),
	}
}
