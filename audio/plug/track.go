package plug

// TrackPlugin is a plugin that reads whole tracks, not only the blocks
// played through it, as ARA 2 lets a VST3 plugin: to find the notes in
// a vocal, or the loudness of a whole song before its first block plays.
// The host tells it of each change to its [Document]; it reads the
// sound whenever it likes, on a goroutine of its own, and plays it back
// through its [Processor].
//
// A host gives the document to each copy of the plugin: the one made
// for its sound, and the one made for its views, which may read the
// track to show it, or to set the plugin from it.
type TrackPlugin interface {
	Plugin
	// SetDocument gives the plugin the host's document as it is now,
	// as the plugin is made and after each change to it. A source in
	// it that the plugin has seen before, by ID, may have new sound;
	// [Source.Version] says.
	SetDocument(d Document)
	// Analysed returns how far the plugin has got through reading the
	// sources, from 0 to 1, for the host to show.
	Analysed() float64
}

// Document is the sound a host has, as a [TrackPlugin] sees it: the
// files, and the stretches of them played where.
type Document interface {
	Sources() []Source
	Regions() []Region
}

// Source is a recording the host has, as a track's file. A host may
// give a plugin the track as it reaches the plugin, in place of the file
// itself: as it is edited, and through the plugins before it in a chain,
// so a plugin reads what it is fed. Reading it the first time may wait
// while the host renders it.
type Source interface {
	// ID tells the source from the others, for as long as it is in
	// the document.
	ID() int
	// Version counts the changes to the source's sound: a plugin that
	// analysed one version reads it again at another.
	Version() int
	Name() string
	Rate() float64
	Channels() int
	Frames() int64
	// ReadAt fills dst, a slice a channel, from frame at on, and
	// returns how many frames it read. It is safe from any goroutine,
	// at any time.
	ReadAt(dst [][]float32, at int64) (int, error)
}

// Region is a stretch of a source, played at a place.
type Region struct {
	// Source is the [Source.ID] played.
	Source int
	// From is the first frame played, in the source, and Length how
	// many are.
	From, Length int64
	// At is where the first frame plays, in frames from the start of
	// the track.
	At int64
}
