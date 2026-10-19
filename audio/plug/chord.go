package plug

import (
	"math"
	"time"
)

// MaxChordTones is the most tones a [ChordNews] carries: more than a
// thirteenth chord's seven.
const MaxChordTones = 8

// MaxChordAhead is the longest [Descriptor.ChordAhead]: a riser of
// eight bars at 120 beats a minute. It bounds the news a host keeps
// room for.
const MaxChordAhead = 16 * time.Second

// ChordNews is news of the chords a plugin's notes play over, for a
// plugin with a [Descriptor.ChordAhead].
//
// The host follows each lane that plays the plugin, and the chords the
// lane reads, voiced as the lane's own notes are voiced from them. It
// tells of each chord once, with Coming, as the chord comes within
// ChordAhead, and with Moved or Removed as an edit changes it. A chord
// has no event of its own as it starts: it starts at its Coming's
// Frame, or its last Moved's, and sounds for Length frames, or until
// the next chord of its Group starts. Unlike a note's news in
// [Block.Upcoming], a Coming or a Moved may start the chord in the very
// block it comes in.
//
// A loop sends a Coming each time it runs on from its start, for the
// chord sounding there, though that chord started before and may be the
// one the plugin has: the chord at the loop's end. A plugin that
// strikes anew at each change, as a root sub or a riser that lands on
// the change, compares the tones, and goes on where they match. The
// host tells of a loop's chords at most one pass ahead, whatever
// ChordAhead asks: each pass is told of as it comes.
//
// Now tells the chord sounding, so a plugin always knows it, for notes
// played live too. The host sends one for each lane as playing starts
// or jumps, after a [Processor.Reset], after an edit, as playing stops,
// and while the song stands still, as the playhead moves to another
// chord. A Now of no tones says no chord sounds there; its key is still
// the key there. A Now may tell of the chord the plugin has already:
// compare the tones here too.
//
// The news comes in the order the host sends it: by the frame it is
// sent at, then by lane. For one lane at one frame, Removed and Moved
// come before Now, and Now before Coming, so a Now and the Comings after
// it say what sounds from there on.
//
// Its one pointer is Name's, to a name the host keeps: a host fills a
// block's news, and a plugin keeps it, with nothing allocated.
type ChordNews struct {
	Kind ChordKind
	// Frame is where the chord starts, counted from the block's start:
	// in this block, as a note's news never is, or a later one. A Now's
	// is where in this block the chord it tells of holds from, and a
	// Removed's is 0.
	Frame int
	// ID is the chord's, from its Coming on: a Moved or a Removed names
	// the chord by it. The same chord, played again, as a loop repeats,
	// comes with an ID of its own. A Now's is 0.
	ID int32
	// Group is the lane whose chord it is, as an [Event]'s: the notes of
	// that group play over it. Lanes that read different chords may
	// play one plugin, each in a group of its own.
	Group int32
	// Length is how many frames the chord sounds from Frame, as the
	// music has it, cut where a loop ends. Like a note's, it is news,
	// not a promise: an edit, a stop or a jump may end it sooner, and
	// the news after says so. A Now's is 0 while the song stands still.
	Length int
	// Beat is where the chord starts in the music, in quarter notes from
	// the start of the track, as [Position.Beat] counts them, and
	// BarBeats how many quarter notes a bar of the meter holds there: 4
	// in 4/4, 3 in 3/4, 3.5 in 7/8. A plugin that times itself to the
	// chord, as a riser some bars long into it, measures in beats, and
	// follows the beat of each block on the way, so whatever the tempo
	// does it lands as the song has it, from any place play starts. A
	// Now's Beat is where its Frame falls. A Removed's are 0.
	Beat     float64
	BarBeats float64
	// Tones are the chord's notes, as MIDI note numbers, lowest first,
	// voiced as the host voices the lane's notes from it. Count of them
	// hold: [ChordNews.Pitches] returns them.
	Tones [MaxChordTones]float64
	Count int
	// Bass is the chord's bass note, as a MIDI note number in the
	// octave from C2, 36: its root, or the note under a slash chord, as
	// E for C/E. A sub that follows the chord plays it.
	Bass float64
	// Root is the pitch class of the chord's root, from 0 for C to 11
	// for B.
	Root int
	// KeyRoot is the pitch class of the key's tonic where the chord
	// starts, and Scale the key's scale, as a set of pitch classes: bit
	// p is set for each pitch class p in it, from bit 0 for C. C major
	// is 0b1010_1011_0101. [ChordNews.InKey] reads it.
	KeyRoot int
	Scale   uint16
	// Name is the chord's name, by its root, as Am7 or C/E, for a
	// plugin to show.
	Name string
}

// Pitches returns the chord's tones, lowest first, as a slice of Tones:
// it allocates nothing.
func (c *ChordNews) Pitches() []float64 { return c.Tones[:min(max(c.Count, 0), MaxChordTones)] }

// InKey reports whether MIDI note pitch, rounded to the nearest, is in
// the key's scale where the chord starts.
func (c *ChordNews) InKey(pitch float64) bool {
	p := int(math.Round(pitch))
	return c.Scale&(1<<uint(((p%12)+12)%12)) != 0
}

// ChordKind is what a [ChordNews] tells.
type ChordKind uint8

// The kinds of chord news.
const (
	// ChordComing tells of a chord that starts at Frame.
	ChordComing ChordKind = iota
	// ChordMoved tells that a chord told of starts at Frame now, as
	// the rest of the news says, as an edit moved or changed it: its
	// tones, its length or its key.
	ChordMoved
	// ChordRemoved takes back a chord told of, as an edit took it out
	// of the music. A plugin passes over a Removed for a chord it never
	// heard of.
	ChordRemoved
	// ChordNow tells the chord sounding from Frame on, in this block.
	ChordNow
)
