package text

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-text/typesetting/font"
)

// emojiFace returns the system's colour emoji font, or skips the test where there is none it knows.
func emojiFace(t *testing.T) *Face {
	t.Helper()
	for _, path := range []string{`C:\Windows\Fonts\seguiemj.ttf`, "/usr/share/fonts/truetype/noto/NotoColorEmoji.ttf",
		"/usr/share/fonts/noto/NotoColorEmoji.ttf"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		f, err := Parse(data)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		return f
	}
	t.Skip("no colour emoji font here")
	return nil
}

// emojiGlyph returns the glyph f maps r to.
func emojiGlyph(t *testing.T, f *Face, r rune) uint32 {
	t.Helper()
	mu.Lock()
	gid, ok := f.face.NominalGlyph(r)
	mu.Unlock()
	if !ok {
		t.Fatalf("the font has no glyph for %U", r)
	}
	return uint32(gid)
}

func TestEmojiRenderInColour(t *testing.T) {
	f := emojiFace(t)
	out := os.Getenv("GUNIM_EMOJI_OUT")
	for _, r := range []rune{0x1F44D, 0x1F600, 0x1F680, 0x2764, 0x1F389, 0x1F525} {
		id := emojiGlyph(t, f, r)
		if !f.IsColor(id) {
			t.Fatalf("%U is not a colour glyph", r)
		}
		m := f.RasterizeColor(id, 48)
		if !m.Color || m.W < 30 || m.H < 30 || len(m.Pix) != m.W*m.H*4 {
			t.Fatalf("%U drew %d by %d, colour %v, want about 48 by 48 in colour", r, m.W, m.H, m.Color)
		}
		// A colour glyph has pixels of more than one hue: not all grey, not all one colour.
		colours := map[[3]uint8]bool{}
		for i := 0; i < len(m.Pix); i += 4 {
			if m.Pix[i+3] > 200 {
				colours[[3]uint8{m.Pix[i] / 32, m.Pix[i+1] / 32, m.Pix[i+2] / 32}] = true
			}
		}
		if len(colours) < 4 {
			t.Fatalf("%U drew %d colours, want a colourful picture", r, len(colours))
		}
		if out != "" {
			img := &image.RGBA{Pix: m.Pix, Stride: m.W * 4, Rect: image.Rect(0, 0, m.W, m.H)}
			w, err := os.Create(filepath.Join(out, "emoji-"+string(r)+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(w, img); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestPlainGlyphsAreNotColour(t *testing.T) {
	f := GoSans(false, false)
	mu.Lock()
	gid, _ := f.face.NominalGlyph('A')
	mu.Unlock()
	if f.IsColor(uint32(gid)) {
		t.Fatal("A in Go Sans is a colour glyph")
	}
	_ = font.GID(0)
}

func TestAnEmojiInTextComesFromTheColourFont(t *testing.T) {
	emojiFace(t)
	run := GoSans(false, false).Shape("Merged \U0001F44D\U0001F3FD and \U0001F469\u200d\U0001F4BB", 14)
	var colour, plain int
	for _, g := range run.Glyphs {
		f, ok := Lookup(g.Face)
		if !ok {
			t.Fatalf("glyph %d names no face", g.ID)
		}
		if f.IsColor(g.ID) {
			colour++
		} else {
			plain++
		}
	}
	// The thumb with its skin tone and the woman at a computer each shape to one picture.
	if colour != 2 {
		t.Fatalf("%d colour glyphs, want 2: one for each emoji sequence", colour)
	}
	if plain < 10 {
		t.Fatalf("%d plain glyphs, want the words", plain)
	}
}

func readEmojiFont() ([]byte, error) { return os.ReadFile(`C:\Windows\Fonts\seguiemj.ttf`) }

func TestEmojiShowsTellsWhatTheSystemDraws(t *testing.T) {
	emojiFace(t)
	if !EmojiShows("\U0001F44D") {
		t.Fatal("thumbs up does not show")
	}
	if EmojiShows("A") {
		t.Fatal("the letter A shows as an emoji")
	}
}
