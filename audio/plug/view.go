package plug

import (
	"time"

	"github.com/marrasen/gunim"
)

// Size is how big a view the host has room for.
type Size int

// The sizes.
const (
	// Chip fits in a plugin's chip in a chain, about 120 by 24: a
	// value, a curve or a meter.
	Chip Size = iota
	// Panel fits in a panel beside or below the chain: the controls
	// used most.
	Panel
	// Full has a pane or a window to itself: every control.
	Full
)

// Viewer is a plugin with views of its own.
//
// A host may make one of the plugin for its views and another for its
// sound, as VST3 parts a plugin's controller from its processor: the
// one for the views never processes, and the host keeps the two in
// step, with each parameter set and each state changed.
type Viewer interface {
	Plugin
	// View returns the plugin's view at size s, which works through h,
	// or nil where it has none at that size. For a plugin without a
	// view at a size, the host draws its own: the plugin's name in a
	// chip, and a control for each parameter in a panel or in full.
	View(s Size, h Host) gunim.Node
}

// Follower is a view that follows its parameters as they change by
// other ways than its own: an undo, a preset, automation, or another
// view of the plugin. The host calls Follow on the window's goroutine.
type Follower interface {
	gunim.Node
	Follow(id uint32, v float64, u *gunim.UI)
}

// StateFollower is a view that follows the plugin's state as it is set
// by other ways than its own, as an undo or a preset: the host calls
// FollowState on the window's goroutine, after [Plugin.SetState]; and
// once as the view first shows, so it starts from the state as it is.
type StateFollower interface {
	gunim.Node
	FollowState(u *gunim.UI)
}

// DocumentFollower is a view of a [TrackPlugin] that follows the
// plugin's document: the host calls FollowDocument on the window's
// goroutine after it gives the plugin a new one, with
// [TrackPlugin.SetDocument].
type DocumentFollower interface {
	gunim.Node
	FollowDocument(u *gunim.UI)
}

// Auditioner is a host that lets a view play a note on the plugin as it
// sounds, as a sound browser plays one to let the user hear the sound
// picked. A view asks for it by a type assertion on its [Host].
type Auditioner interface {
	Host
	// Audition plays a note on the plugin playing: pitch is a MIDI note
	// number, as 60 for middle C, velocity runs from 0 to 1, and the
	// note is let go after length. It runs on the window's goroutine; a
	// host that plays nothing now plays nothing.
	Audition(pitch, velocity float64, length time.Duration, u *gunim.UI)
}
