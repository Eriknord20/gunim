// Command roundtrip plays and records through AAudio on Android,
// WASAPI on Windows, or the driver -driver names, and shows how: a click
// each second through the speakers, the microphone's level, the delays
// the speaker reports, and the times the sound ran dry. A tap on the
// screen, or a click, measures the round trip: a click played, heard by
// the microphone, and found in the recording by its stamp, as package
// audio/capture's Measure finds it. An offset near zero says the stamps
// line a take up with what the singer heard. A dot lights as each click
// is heard, at the mixer frame the speaker says plays now: it should
// light with the click, neither before nor after.
//
// Below the text, a guided check takes a tester through the device's
// own speaker, recording, headphones, and the application going away,
// a step at a time. Done logs the state as each step ends, after
// "CHECKSTEP n done", for whoever reads the log.
//
//	go run ./tools/gunimapk -permissions microphone -run ./example/roundtrip
//	go run ./example/roundtrip -driver "Core Audio"   # on macOS
//	go run ./example/roundtrip -driver JACK           # on Linux
//
// On Windows:
//
//	go run ./example/roundtrip
//
// -driver names another driver to play through, such as an ASIO
// driver's name. It asks for the microphone as it starts. Where the
// driver fails to open, it plays through the system's sound, and says
// why it records nothing.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image/color"
	"log"
	"math"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/capture"
	audiodriver "github.com/marrasen/gunim/audio/driver"
	"github.com/marrasen/gunim/audio/speaker"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	def := audiodriver.AAudio
	if runtime.GOOS == "windows" {
		def = audiodriver.WASAPI
	}
	through := flag.String("driver", def, "the driver to play and record through, as package "+
		"audio/driver names it: \"Core Audio\" on macOS, JACK on Linux")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := gunim.Main(ctx, func(a *gunim.App) error {
		w, err := a.NewWindow(gunim.WindowOptions{Title: "gunim round trip", Size: geom.Sz(380, 560),
			Root: widget.NewSurface()})
		if err != nil {
			return err
		}
		mix := audio.NewMixer()
		spk, err := speaker.Open(mix, speaker.Options{Name: "gunim round trip", Driver: *through, Record: true})
		if err != nil {
			return err
		}
		defer func() { _ = spk.Close() }()
		tk := &ticks{rate: mix.Rate(), mix: mix}
		tk.start.Store(-1)
		t := &tester{mix: mix, spk: spk, ticks: tk}
		mix.Play(tk, audio.Options{})
		go func() { t.allowed.Store(a.Ask(driver.PermissionMicrophone)) }()
		go t.record(ctx)
		go t.report(ctx)
		gunim.RegisterView(w, "roundtrip", func(struct{}) *view {
			v := &view{t: t, label: widget.NewLabel(""), guide: widget.NewLabel(""), done: widget.NewButton("Done")}
			v.done.Kind = widget.ButtonPrimary
			v.done.OnClick = func(*gunim.UI) gunim.Intent {
				v.next()
				return nil
			}
			return v
		}, nil)
		c := w.Client()
		if err := c.Mount(gunim.Root, "roundtrip", "roundtrip", struct{}{}); err != nil {
			return err
		}
		for range c.Intents() {
		}
		return c.Err()
	})
	if errors.Is(err, driver.ErrNoDriver) {
		log.Print("gunim has no driver for this operating system yet")
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// ticks is a click of 20 ms at 1 kHz at the start of each second.
type ticks struct {
	rate int
	n    int
	mix  *audio.Mixer
	// start is the mixer frame the first click starts at, or -1 before
	// the mixer first reads: the dot lights as each click is heard.
	start atomic.Int64
}

func (t *ticks) Read(dst []float32) (int, error) {
	if t.start.Load() < 0 {
		t.start.Store(t.mix.Mixed())
	}
	for i := 0; i+1 < len(dst); i += 2 {
		v := float32(0)
		if at := t.n % t.rate; at < t.rate/50 {
			v = 0.3 * float32(math.Sin(2*math.Pi*1000*float64(at)/float64(t.rate)))
		}
		dst[i], dst[i+1] = v, v
		t.n++
	}
	return len(dst) / 2, nil
}

// A tester records the microphone, and measures the round trip when
// asked.
type tester struct {
	ticks   *ticks
	mix     *audio.Mixer
	spk     *speaker.Speaker
	allowed atomic.Bool
	// measure asks the recording goroutine to measure.
	measure atomic.Bool
	mu      sync.Mutex
	// peak is the microphone's loudest sample in the last second;
	// result what the last measurement found; recording what the
	// recording says of itself.
	peak      float32
	result    string
	recording string
}

// record records from the speaker's microphone, opening it again as it
// goes away, until ctx ends.
func (t *tester) record(ctx context.Context) {
	for ctx.Err() == nil {
		d, ok := capture.DriverInputs(t.spk)
		if !ok {
			t.say(&t.recording, "nothing to record from")
			time.Sleep(time.Second)
			continue
		}
		in, err := capture.Open(capture.Options{Device: d.ID, Speaker: t.spk})
		if err != nil {
			t.say(&t.recording, err.Error())
			time.Sleep(time.Second)
			continue
		}
		t.read(ctx, in)
		_ = in.Close()
	}
}

// read reads in, measuring when asked, until it ends.
func (t *tester) read(ctx context.Context, in *capture.Stream) {
	buf := make([]float32, 4096)
	var peak float32
	since := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-in.Done():
			t.say(&t.recording, fmt.Sprintf("the input ended: %v", in.Err()))
			return
		default:
		}
		if t.measure.Swap(false) {
			t.say(&t.result, "measuring…")
			m, err := capture.Measure(in, t.mix, 0, 3*time.Second)
			if err != nil {
				t.say(&t.result, err.Error())
			} else {
				t.say(&t.result, fmt.Sprintf("offset %v, round trip %v, level %.2f",
					t.mix.Duration(m.Offset).Round(100*time.Microsecond), m.RoundTrip.Round(100*time.Microsecond), m.Level))
			}
		}
		b := in.Read(buf)
		for _, v := range b.Frames {
			peak = max(peak, v, -v)
		}
		if time.Since(since) >= time.Second {
			t.mu.Lock()
			t.peak = peak
			t.recording = fmt.Sprintf("recording, latency %v", in.Latency().Round(100*time.Microsecond))
			t.mu.Unlock()
			peak, since = 0, time.Now()
		}
		if b.Len() == 0 {
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func (t *tester) say(s *string, v string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	*s = v
}

// report prints the text to the terminal every five seconds, and as a
// measurement ends, so it can be copied, until ctx ends.
func (t *tester) report(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	last := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-time.After(200 * time.Millisecond):
			t.mu.Lock()
			r := t.result
			t.mu.Unlock()
			if r == last || r == "measuring…" {
				continue
			}
			last = r
		}
		log.Printf("\n%s", t.text())
	}
}

// text says how the speaker plays and records now.
func (t *tester) text() string {
	st := t.spk.State()
	var b strings.Builder
	through := st.Driver
	if through == "" {
		through = "the system's sound"
	}
	fmt.Fprintf(&b, "Playing through %s at %d Hz.\n", through, st.Rate)
	fmt.Fprintf(&b, "Latency %v.\n", st.Latency.Round(100*time.Microsecond))
	if st.Info != nil {
		fmt.Fprintf(&b, "Device delay %v, input %v, burst %d frames.\n",
			audio.DurationAt(int64(st.Info.Latency), st.Rate).Round(100*time.Microsecond),
			audio.DurationAt(int64(st.Info.InputLatency), st.Rate).Round(100*time.Microsecond), st.Info.Buffer)
	}
	fmt.Fprintf(&b, "Ran dry %d times.\n", st.Dropouts)
	if st.Problem != nil {
		fmt.Fprintf(&b, "Problem: %v\n", st.Problem)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.allowed.Load() {
		b.WriteString("The microphone is not allowed.\n")
	}
	fmt.Fprintf(&b, "\nMicrophone: %s, peak %.3f.\n", t.recording, t.peak)
	fmt.Fprintf(&b, "\nTap to measure the round trip.\n%s\n", t.result)
	return b.String()
}

// view shows the tester's text, and measures on a tap. It suspends the
// speaker while the window is out of sight.
type view struct {
	t     *tester
	label *widget.Label
	// guide shows the step of the check to do now, and done goes on to
	// the next, logging the state for whoever reads the log.
	guide *widget.Label
	done  *widget.Button
	step  int
	// began is when the step began, for the timer beside it.
	began time.Time
	// doneAt is where done sits, so a tap on it measures nothing.
	doneAt geom.Rect
}

// steps is the check, a step at a time.
var steps = []string{
	"Step 1 of 6: the device's own speaker. Headphones off. Let it click for a minute. " +
		"Then tap or click above this text three times, waiting for each result. Then press Done.",
	"Step 2 of 6: recording. Clap or sing for a few seconds while it clicks. Then press Done.",
	"Step 3 of 6: headphones, Bluetooth ones where you have them. Connect them while it clicks. " +
		"Listen for a minute: any crackle, gaps or wrong device? Does the dot light with the click? " +
		"Note what you hear. Then press Done.",
	"Step 4 of 6: the headphones' delay. Hold one earbud or ear cup against the device's microphone, " +
		"in a quiet room. Tap or click above this text three times, waiting for each result. The offset " +
		"is the delay the system leaves out. Then press Done.",
	"Step 5 of 6: headphones off and on. Disconnect them, wait 5 s, connect them again, wait 5 s. " +
		"Do it three times. Then press Done.",
	"Step 6 of 6: going away. Lock the screen, wait 5 s, unlock. Then switch to another application " +
		"for 10 s and come back. Watch the system's microphone indicator while away: it should go out. " +
		"Then press Done.",
	"All done. Thank you!",
}

// next logs the state as the step ends, for a screenshot from it, and
// goes on to the next step.
func (v *view) next() {
	if v.step >= len(steps)-1 {
		return
	}
	v.step++
	v.began = time.Now()
	log.Printf("CHECKSTEP %d done\n%s", v.step, v.t.text())
}

func (v *view) Children() []gunim.Node { return []gunim.Node{v.label, v.guide, v.done} }

// Handle implements [gunim.Handler].
func (v *view) Handle(e input.Event, _ *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerUp:
		if v.doneAt.Contains(e.Pos) {
			return false
		}
		v.t.measure.Store(true)
		return true
	case input.WindowHidden:
		if err := v.t.spk.Suspend(); err != nil {
			log.Print(err)
		}
	case input.WindowShown:
		if err := v.t.spk.Resume(); err != nil {
			log.Print(err)
		}
	}
	return false
}

// Step draws every frame while the dot is lit.
func (v *view) Step(time.Duration) bool { return v.flash() > 0 }

// WakeIn wakes the window as the next click is heard, and each second
// at the latest, for the text.
func (v *view) WakeIn() time.Duration {
	since, ok := v.sinceClick()
	if !ok {
		return time.Second
	}
	rate := int64(v.t.mix.Rate())
	return max(time.Millisecond, min(time.Second, audio.DurationAt(rate-since, int(rate))))
}

// sinceClick returns how many frames ago the last click was heard, at
// the mixer frame the speaker plays now, and false before the first.
func (v *view) sinceClick() (int64, bool) {
	start := v.t.ticks.start.Load()
	if start < 0 {
		return 0, false
	}
	h := v.t.mix.Heard() - start
	if h < 0 {
		return 0, false
	}
	return h % int64(v.t.mix.Rate()), true
}

// flash returns how bright the dot is now, 0 to 1: full as a click is
// heard, and fading over 150 ms.
func (v *view) flash() float32 {
	since, ok := v.sinceClick()
	fade := int64(v.t.mix.Rate()) * 15 / 100
	if !ok || since >= fade {
		return 0
	}
	return 1 - float32(since)/float32(fade)
}

func (v *view) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	v.label.Text = v.t.text()
	if v.began.IsZero() {
		v.began = time.Now()
	}
	el := time.Since(v.began)
	v.guide.Text = fmt.Sprintf("%s\n\nTime on this step: %d:%02d", steps[v.step], int(el.Minutes()), int(el.Seconds())%60)
	w := c.Max.W - 32
	i := 0
	for kid := range kids.All {
		switch i {
		case 0:
			kid.Layout(gunim.Tight(geom.Sz(w, c.Max.H*0.45)))
			kid.Place(geom.Pt(16, 64))
		case 1:
			kid.Layout(gunim.Tight(geom.Sz(w, c.Max.H*0.25)))
			kid.Place(geom.Pt(16, 64+c.Max.H*0.45))
		case 2:
			sz := kid.Layout(gunim.Loose(geom.Sz(w, 64)))
			at := geom.Pt(16, c.Max.H-sz.H-48)
			kid.Place(at)
			v.doneAt = geom.Rect{Min: at, Max: at.Add(geom.Pt(sz.W, sz.H))}
		}
		i++
	}
	return c.Max
}

func (v *view) Paint(p *paint.Painter, _ gunim.Frame, size geom.Size, kids gunim.Children) {
	// The dot, right of the text: it lights as each click is heard.
	const d = 120
	at := geom.Pt(size.W-d-24, size.H*0.30)
	r := geom.Rect{Min: at, Max: at.Add(geom.Pt(d, d))}
	p.RRect(r, d/2, paint.Solid(color.NRGBA{R: 60, G: 60, B: 70, A: 255}))
	if f := v.flash(); f > 0 {
		p.RRect(r, d/2, paint.Solid(color.NRGBA{R: 80, G: 220, B: 120, A: uint8(255 * f)}))
	}
	for kid := range kids.All {
		kid.Paint(p)
	}
}
