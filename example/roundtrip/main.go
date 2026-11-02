// Command roundtrip plays and records through AAudio on Android,
// WASAPI on Windows, or the driver -driver names, and shows how: a click
// each second through the speakers, the microphone's level, the delays
// the speaker reports, and the times the sound ran dry. A tap on the
// screen, or a click, measures the round trip: a click played, heard by
// the microphone, and found in the recording by its stamp, as package
// audio/capture's Measure finds it. An offset near zero says the stamps
// line a take up with what the singer heard.
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
		t := &tester{mix: mix, spk: spk}
		mix.Play(&ticks{rate: mix.Rate()}, audio.Options{})
		go func() { t.allowed.Store(a.Ask(driver.PermissionMicrophone)) }()
		go t.record(ctx)
		go t.report(ctx)
		gunim.RegisterView(w, "roundtrip", func(struct{}) *view { return &view{t: t, label: widget.NewLabel("")} }, nil)
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
}

func (t *ticks) Read(dst []float32) (int, error) {
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
}

func (v *view) Children() []gunim.Node { return []gunim.Node{v.label} }

// Handle implements [gunim.Handler].
func (v *view) Handle(e input.Event, _ *gunim.UI) bool {
	switch e.(type) {
	case input.PointerUp:
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

// WakeIn has the text follow the speaker each second.
func (v *view) WakeIn() time.Duration { return time.Second }

func (v *view) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	v.label.Text = v.t.text()
	for kid := range kids.All {
		kid.Layout(gunim.Tight(geom.Sz(c.Max.W-32, c.Max.H-96)))
		kid.Place(geom.Pt(16, 64))
	}
	return c.Max
}

func (v *view) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for kid := range kids.All {
		kid.Paint(p)
	}
}
