// Package speaker plays a [audio.Mixer] through the computer's
// speakers: through PulseAudio or PipeWire, else ALSA, on Linux,
// WASAPI on Windows, Core Audio on macOS and Oboe on Android; and on
// Windows through an ASIO driver too, as package audio/asio plays.
//
// A process opens the speakers once and plays everything through the
// one mixer:
//
//	mix := audio.NewMixer()
//	spk, err := speaker.Open(mix, speaker.Options{Name: "My app"})
//
// [Speaker.Set] changes how they play while they play: the driver, the
// rate, the samples' bits, and how far ahead the mixer works.
//
// Through an ASIO driver, [Options].Outputs picks the driver's outputs
// to play on, and [Options].Aux fills those past the mixer's two, in
// step with it.
// [Options].Direct has the driver's own thread mix each buffer as it
// takes it, so the sound is heard sooner, by a buffer at the least.
//
// Package audio/capture records the inputs of the ASIO driver the
// speakers play through, where [Options].Record is set, and stamps
// what it records with [Speaker.Heard].
//
// [Speaker.Anchor] ties the mixer's frames to the clock [time.Now]
// reads, so a host sends MIDI for a frame to leave as it is heard, and
// places MIDI that comes in on the frame heard as it arrived.
package speaker

import (
	"errors"
	"log"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/asio"
)

// Options says how to open the speakers.
type Options struct {
	// Name names the application to the system's volume controls.
	Name string
	// Driver is the ASIO driver to play through, by its name from
	// [asio.Drivers]. Empty plays through the system's own sound, as
	// does a driver that fails to open: [State] tells why.
	Driver string
	// Rate is the rate to play at, in frames a second; the speaker sets
	// the mixer to it. Zero is [audio.SampleRate]. An ASIO driver that
	// refuses a rate plays at its own. The system's own sound may
	// convert the rate on its way to the device, as Windows converts to
	// the format its device is set to. [State] tells both rates.
	Rate int
	// Bits rounds the sound to integer samples of 16 or 24 bits as it
	// leaves, as a WAV file of such samples holds it, with dither where
	// Dither says, as [audio.Quantizer] does. Zero or 32 leaves it as
	// the mixer made it.
	Bits   int
	Dither bool
	// Latency is how far ahead of the speakers the mixer starts out
	// working: how long a sound takes to be heard after it starts. Zero
	// means 30 milliseconds.
	Latency time.Duration
	// Fixed holds Latency where it is. Otherwise, where the sound runs
	// dry, as where the system asks for it in larger pieces or the
	// mixer is late to fill it, the speaker works further ahead, 10
	// milliseconds at a time, as far as [MaxLatency].
	Fixed bool
	// Buffer is how many frames an ASIO driver takes at a time, brought
	// to the nearest size the driver allows. Zero takes the size its
	// control panel sets. The latency is never less than a buffer and
	// the driver's own latency, whatever Latency asks for.
	Buffer int
	// Direct mixes each buffer an ASIO driver takes on the driver's own
	// thread, as it takes it, in place of reading the mixer ahead into a
	// ring on a thread of the speaker's. The sound is then heard the
	// driver's latency after it is mixed, sooner by the ring's length, a
	// buffer at the least, and no hand-over between threads can make a
	// buffer late; but the mixer must fill each buffer in the time a
	// buffer plays, with nothing to spare, and a buffer that takes longer
	// counts in [State].Dropouts. Latency and Fixed are let be, and the
	// speaker never works further ahead: choose the Buffer to fit.
	// Through the system's own sound it changes nothing.
	Direct bool
	// Record opens the inputs of the ASIO driver too, for package
	// audio/capture to record. A driver may then open a device of its
	// own to record from, as FlexASIO and ASIO4ALL open Windows'
	// microphone, so leave it unset to play alone.
	Record bool
	// Outputs are the outputs of the ASIO driver to play on, by their
	// index from 0, as [asio.Channels] lists them; nil plays on its
	// first two. The mixer plays on the first two named, or on the one
	// named alone. Those named past two play what Aux fills, in step
	// with the mixer, at the same latency. The system's own sound plays
	// the mixer's two alone: [State].Aux tells how many more play.
	Outputs []int
	// Aux, where set, fills the channels past the mixer's two, where
	// Outputs names more than two: dst holds frames of channels
	// interleaved samples, cleared, one for each output past the first
	// two named, in order. at is the mixer frame of dst's first frame,
	// as [audio.Mixer.Mixed] counts them: the speaker calls Aux where it
	// reads the mixer, on a goroutine of its own or, where Direct is set,
	// on the driver's thread, right after it mixed those same frames,
	// with each [audio.Mixer.Mix] call it makes. It must be as
	// quick as a voice's Read, and must not allocate. Bits and Dither round the mixer's pair alone, and
	// the fades of Suspend and Resume fade it alone.
	Aux func(dst []float32, channels int, at int64)
}

// rate returns the rate o asks for.
func (o Options) rate() int {
	if o.Rate > 0 {
		return o.Rate
	}
	return audio.SampleRate
}

// latency returns the latency o asks for.
func (o Options) latency() time.Duration {
	if o.Latency > 0 {
		return min(o.Latency, MaxLatency)
	}
	return 30 * time.Millisecond
}

// MaxLatency is as far ahead as the speaker grows to work.
const MaxLatency = time.Second

// device is the device's own buffer, as the speaker counts it where the
// system reports no latency of its own. The player's buffer, which the
// mixer fills, makes up the rest of the latency.
const device = 10 * time.Millisecond

// chunk is the most the mixer hands on at once. The player reads a
// buffer's worth whenever its buffer has room, so a read of a whole
// buffer could leave nearly two waiting; small reads keep it to one.
const chunk = 5 * time.Millisecond

// A Speaker plays a mixer. Its methods are safe from any goroutine.
type Speaker struct {
	m *audio.Mixer
	// mu guards o and problem, and opening and closing the output.
	mu      sync.Mutex
	o       Options
	problem error
	// out is the output playing, nil while it changes.
	out atomic.Pointer[outlet]
	// ahead is how far ahead the mixer works, as Latency set it and dry
	// spells grew it, in nanoseconds.
	ahead atomic.Int64
	fixed atomic.Bool
	// dry counts the reads that found the buffer empty: the sound ran
	// out before the mixer filled it again. reads counts all of them, so
	// the first, into an empty buffer, are let be. dropouts counts dry
	// reads ever.
	dry, reads, dropouts atomic.Int64
	// suspended stops the watch growing the buffer while nothing plays.
	suspended atomic.Bool
	// smu guards stopped, faded and asked: the device is stopped; the
	// mix was faded out as it suspended, to fade in as it resumes; and
	// asked counts Suspend's and Resume's calls, so a stop Suspend
	// waits to make lapses where Resume came first.
	smu            sync.Mutex
	stopped, faded bool
	asked          int
	// q rounds the sound as it leaves, as bits and dither say: q is
	// the one thread's that pulls, the feed's or the driver's, and the
	// settings anyone's, so neither waits.
	bits   atomic.Int64
	dither atomic.Bool
	q      audio.Quantizer
	// aux is Options.Aux, set by Set while the sound plays.
	aux atomic.Pointer[auxFunc]
	// anchor ties the frames heard to the clock.
	anchor dll
	// reset asks the watch to open the output again, as an ASIO driver
	// asks; changed tells of an output opened again.
	reset   chan struct{}
	changed chan struct{}
	quit    chan struct{}
	done    chan struct{}
	closing sync.Once
	debug   bool
}

// auxFunc is Options.Aux's type.
type auxFunc = func(dst []float32, channels int, at int64)

// setAux sets the function that fills the channels past the mixer's.
func (s *Speaker) setAux(f auxFunc) {
	if f == nil {
		s.aux.Store(nil)
		return
	}
	s.aux.Store(&f)
}

// pullAux fills dst, frames of channels past the mixer's two from
// mixer frame at, as Options.Aux says: with silence, where it is unset.
func (s *Speaker) pullAux(dst []float32, channels int, at int64) {
	clear(dst)
	if f := s.aux.Load(); f != nil {
		(*f)(dst, channels, at)
	}
}

// An output is where the speaker's sound goes: the system's own sound,
// or an ASIO driver.
type output interface {
	// held returns how many frames the output took that are still to be
	// heard: in its buffer and past it.
	held() int64
	// setAhead sets how far ahead of the speakers the mixer works.
	setAhead(d time.Duration)
	// latency returns how far ahead it works now.
	latency() time.Duration
	// rate returns the rate it plays at.
	rate() int
	// fill adds what it knows of itself to st.
	fill(st *State)
	err() error
	suspend() error
	resume() error
	close() error
}

// outlet holds an output, for an atomic pointer.
type outlet struct{ output }

var (
	opened   sync.Mutex
	isOpened bool
)

// ErrOpen is returned by [Open] while the speakers are open already.
var ErrOpen = errors.New("speaker: the speakers are open already")

// Open starts the speakers playing m. A process has them open once at
// a time.
func Open(m *audio.Mixer, o Options) (*Speaker, error) {
	opened.Lock()
	defer opened.Unlock()
	if isOpened {
		return nil, ErrOpen
	}
	s := &Speaker{m: m, o: o, reset: make(chan struct{}, 1), changed: make(chan struct{}, 1),
		quit: make(chan struct{}), done: make(chan struct{}), debug: os.Getenv("GUNIM_DEBUG_AUDIO") != ""}
	s.bits.Store(int64(o.Bits))
	s.dither.Store(o.Dither)
	s.setAux(o.Aux)
	s.q.Ready()
	s.fixed.Store(o.Fixed)
	s.ahead.Store(int64(o.latency()))
	out, err := s.open()
	if err != nil {
		return nil, err
	}
	isOpened = true
	s.out.Store(&outlet{out})
	m.SetLatency(s.held)
	s.logf("working %v ahead", s.Latency())
	go s.watch()
	return s, nil
}

// open opens the output s.o asks for, falling back to the system's own
// sound, and sets the mixer to its rate. It runs with mu held, or
// before s is shared.
func (s *Speaker) open() (output, error) {
	s.problem = nil
	s.reads.Store(0)
	ahead := time.Duration(s.ahead.Load())
	if s.o.Driver != "" {
		out, err := openRing(s, s.o)
		if err == nil {
			s.m.SetRate(out.rate())
			out.start(ahead)
			return out, nil
		}
		s.problem = err
		s.logf("%v; playing through the system's sound", err)
	}
	out, err := openOto(s, s.o)
	if err != nil {
		return nil, err
	}
	s.m.SetRate(out.rate())
	out.start(ahead)
	return out, nil
}

// pull fills frames with what plays next, rounded as Bits says.
func (s *Speaker) pull(frames []float32) {
	s.m.Mix(frames)
	s.q.Bits, s.q.Dither = int(s.bits.Load()), s.dither.Load()
	s.q.Process(frames)
}

// ranDry counts a read that found the buffer empty.
func (s *Speaker) ranDry() {
	s.dry.Add(1)
	s.dropouts.Add(1)
}

// askReset asks the watch to open the output again.
func (s *Speaker) askReset() {
	select {
	case s.reset <- struct{}{}:
	default:
	}
}

func (s *Speaker) logf(format string, args ...any) {
	if s.debug {
		log.Printf("speaker: "+format, args...)
	}
}

// held returns how many frames the mixer gave that are still to be
// heard.
func (s *Speaker) held() int64 {
	if out := s.out.Load(); out != nil {
		return out.held()
	}
	return 0
}

// Heard returns the mixer's frame at the speakers now: how many of the
// frames it mixed have been heard. Package audio/capture stamps what it
// records with it.
func (s *Speaker) Heard() int64 { return s.m.Heard() }

// Latency returns how long a sound takes to be heard after it starts,
// as the speaker works now.
func (s *Speaker) Latency() time.Duration {
	if out := s.out.Load(); out != nil {
		return out.latency()
	}
	return time.Duration(s.ahead.Load())
}

// watch grows the buffer while the sound runs dry, and opens the
// output again as a driver asks.
func (s *Speaker) watch() {
	defer close(s.done)
	const every = 250 * time.Millisecond
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-s.reset:
			s.mu.Lock()
			if err := s.reopenLocked(); err != nil {
				s.logf("opening the output again: %v", err)
			}
			s.mu.Unlock()
		case <-t.C:
			s.Anchor()
			dry := s.dry.Swap(0)
			if dry > 0 {
				s.logf("ran dry %d times in %v", dry, every)
			}
			if out := s.out.Load(); out != nil {
				if r, ok := out.output.(*ringOutput); ok {
					if since, now := r.stoppedTaking(2 * time.Second); now {
						s.logf("the driver has asked for no sound in %v", since.Round(time.Millisecond))
					}
				}
			}
			// Once might be a hiccup; more is the buffer too small. It
			// grows from how far ahead the output works, which may be
			// further than asked, as a driver's buffer is long.
			ahead := max(time.Duration(s.ahead.Load()), s.Latency())
			if dry >= 2 && !s.suspended.Load() && !s.fixed.Load() && ahead < MaxLatency {
				s.setAhead(min(ahead+10*time.Millisecond, MaxLatency))
				s.logf("working %v ahead", s.Latency())
			}
		}
	}
}

// setAhead sets how far ahead the mixer works, as far as MaxLatency.
func (s *Speaker) setAhead(d time.Duration) {
	d = min(d, MaxLatency)
	s.ahead.Store(int64(d))
	if out := s.out.Load(); out != nil {
		out.setAhead(d)
	}
}

// SetLatency sets how far ahead of the speakers the mixer works, as
// Options.Latency does: further while nothing needs the sound to answer
// at once, as while a player plays on with its screen off, so the sound
// rides out a busy moment; nearer again when it does. Where Fixed is
// unset the speaker still grows it, as it runs dry.
func (s *Speaker) SetLatency(d time.Duration) {
	s.mu.Lock()
	s.o.Latency = d
	s.mu.Unlock()
	s.setAhead(max(d, device))
	s.logf("working %v ahead", s.Latency())
}

// Set changes how the speakers play. A change of driver or rate, or of
// Buffer, Record, Direct or Outputs while a driver plays, opens the output
// again, with a moment's silence; the mixer's rate follows, and
// [Speaker.Changed] tells of it. A change of Latency or Fixed sets how
// far ahead the mixer works from where it is.
func (s *Speaker) Set(o Options) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.o
	s.o = o
	s.bits.Store(int64(o.Bits))
	s.dither.Store(o.Dither)
	s.setAux(o.Aux)
	s.fixed.Store(o.Fixed)
	if o.latency() != prev.latency() || o.Fixed != prev.Fixed {
		s.setAhead(o.latency())
	}
	if o.Driver != prev.Driver || o.rate() != prev.rate() ||
		(o.Driver != "" && (o.Buffer != prev.Buffer || o.Record != prev.Record || o.Direct != prev.Direct ||
			!slices.Equal(o.Outputs, prev.Outputs))) {
		return s.reopenLocked()
	}
	return nil
}

// reopenLocked closes the output and opens the one s.o asks for. It
// runs with mu held.
func (s *Speaker) reopenLocked() error {
	if cur := s.out.Load(); cur != nil {
		s.out.Store(nil)
		if err := cur.close(); err != nil {
			if !errors.Is(err, asio.ErrNotAnswering) {
				s.out.Store(cur)
				return err
			}
			// Let go as it stopped answering: the output asked for
			// opens all the same.
			s.logf("%v; opening the output again all the same", err)
		}
	}
	out, err := s.open()
	if err != nil {
		s.problem = err
		return err
	}
	s.anchor.reset()
	s.out.Store(&outlet{out})
	s.logf("opened again, at %d Hz, working %v ahead", out.rate(), s.Latency())
	select {
	case s.changed <- struct{}{}:
	default:
	}
	return nil
}

// Changed returns a channel that receives as the output opens again:
// on a change of driver or rate, or as an ASIO driver asks after its
// settings change. The mixer plays at the output's rate from then, and
// an application plays its sounds again, made for that rate.
func (s *Speaker) Changed() <-chan struct{} { return s.changed }

// A State says how the speakers play.
type State struct {
	// Driver is the ASIO driver playing, or empty for the system's own
	// sound.
	Driver string
	// Rate is the rate the mixer and the output play at, in frames a
	// second. DeviceRate is the rate the system converts it to on its
	// way to the device, where the system reports one, and zero where it
	// plays at Rate or reports none.
	Rate, DeviceRate int
	// Bits and Dither are how the sound is rounded as it leaves.
	Bits   int
	Dither bool
	// Latency is how far ahead of the speakers the mixer works, and
	// Frames the same in frames at Rate. Fixed says it is held there.
	Latency time.Duration
	Frames  int64
	Fixed   bool
	// Dropouts counts the times the sound ran dry since the speakers
	// opened: where Direct is set, the buffers that took longer to mix
	// than they play.
	Dropouts int64
	// ASIO is what the driver says of itself, while one plays; its
	// Played tells the output each channel plays on.
	ASIO *asio.Info
	// Aux is how many channels play past the mixer's two, which
	// [Options].Aux fills: as many as Options.Outputs names past two,
	// while an ASIO driver plays; none through the system's own sound.
	Aux int
	// Problem is why the output asked for plays otherwise, or not at
	// all.
	Problem error
}

// State returns how the speakers play now.
func (s *Speaker) State() State {
	s.mu.Lock()
	st := State{Bits: s.o.Bits, Dither: s.o.Dither, Fixed: s.o.Fixed, Problem: s.problem}
	s.mu.Unlock()
	if st.Bits == 32 {
		st.Bits = 0
	}
	st.Dropouts = s.dropouts.Load()
	if out := s.out.Load(); out != nil {
		st.Rate = out.rate()
		st.Latency = out.latency()
		st.Frames = audio.FramesAt(st.Latency, st.Rate)
		out.fill(&st)
	}
	return st
}

// ControlPanel opens the own settings of the ASIO driver Options
// names, and returns once they close. A driver that failed to open
// opens its settings too, as a change there may let it start, and the
// speaker tries it again once they close.
func (s *Speaker) ControlPanel() error {
	if out := s.out.Load(); out != nil {
		if r, ok := out.output.(*ringOutput); ok {
			return r.dev.ControlPanel()
		}
	}
	s.mu.Lock()
	driver := s.o.Driver
	s.mu.Unlock()
	if driver == "" {
		return errors.New("speaker: no ASIO driver is chosen")
	}
	err := driverPanel(driver)
	s.askReset()
	return err
}

// driverPanel opens the settings of a driver that plays nothing; a
// test puts a function of its own here.
var driverPanel = asio.ControlPanel

// Err returns an error the speakers met while playing, if any.
func (s *Speaker) Err() error {
	if out := s.out.Load(); out != nil {
		return out.err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.problem
}

// SuspendFade is how long [Speaker.Suspend] takes to fade out the
// sound playing, and [Speaker.Resume] to fade it back in.
const SuspendFade = 150 * time.Millisecond

// Suspend stops the device, as while the application is in the
// background on a phone. The mixer stops with it. Sound playing fades
// out first, over [SuspendFade]: Suspend returns at once, and the
// device stops once the fade has been heard, unless Resume comes
// first.
func (s *Speaker) Suspend() error {
	s.smu.Lock()
	defer s.smu.Unlock()
	s.suspended.Store(true)
	s.asked++
	if s.stopped {
		return nil
	}
	if !s.faded && !s.m.Sounding() {
		return s.stopLocked()
	}
	s.faded = true
	s.m.FadeOut(SuspendFade)
	asked := s.asked
	time.AfterFunc(SuspendFade+s.Latency(), func() {
		s.smu.Lock()
		defer s.smu.Unlock()
		if s.asked == asked && !s.stopped {
			_ = s.stopLocked()
		}
	})
	return nil
}

// stopLocked stops the device. It runs with smu held.
func (s *Speaker) stopLocked() error {
	out := s.out.Load()
	if out == nil {
		return nil
	}
	s.stopped = true
	return out.suspend()
}

// Resume starts a suspended device again, and fades back in the sound
// Suspend faded out, over [SuspendFade]. Where nothing sounded as it
// suspended, a sound played as it resumes starts at once.
func (s *Speaker) Resume() error {
	s.smu.Lock()
	defer s.smu.Unlock()
	s.asked++
	var err error
	if s.stopped {
		if out := s.out.Load(); out != nil {
			err = out.resume()
		}
		s.stopped = false
	}
	if s.faded {
		s.faded = false
		s.m.FadeIn(SuspendFade)
	}
	s.suspended.Store(false)
	return err
}

// Close stops the speakers. [Open] can open them again after.
func (s *Speaker) Close() error {
	var err error
	s.closing.Do(func() {
		close(s.quit)
		<-s.done
		s.mu.Lock()
		if out := s.out.Swap(nil); out != nil {
			err = out.close()
		}
		s.mu.Unlock()
		s.anchor.clear()
		opened.Lock()
		isOpened = false
		opened.Unlock()
	})
	return err
}
