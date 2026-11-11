// Command synccheck checks by eye and ear whether gunim's picture keeps
// in step with its sound: it plays four rising notes and shows their
// numbers, 1 to 4, as the speaker says each is heard. Buttons shift the
// picture earlier or later until the two feel together; the shift left
// is how far the delay the speaker reports is off. Other buttons switch
// between gunim's own driver named, as AAudio on Android or PipeWire on
// Linux, and the system's own sound, and turn Options.Quick on and
// off. Done logs a line that starts "SYNCCHECK", with the shift and what
// the speaker reports. On Android the speaker's log is on, for the
// delays AAudio and the Bluetooth route report.
//
//	go run ./tools/gunimapk -run ./example/synccheck
//	go run ./example/synccheck   # on Linux, through PipeWire
package main

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"log"
	"math"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	audiodriver "github.com/marrasen/gunim/audio/driver"
	"github.com/marrasen/gunim/audio/speaker"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

const name = "gunim sync check"

// native is the driver of gunim's own the check plays through first:
// PipeWire on Linux, WASAPI on Windows, Core Audio on macOS and AAudio
// on Android.
var native = func() string {
	switch runtime.GOOS {
	case "linux":
		return audiodriver.PipeWire
	case "windows":
		return audiodriver.WASAPI
	case "darwin":
		return audiodriver.CoreAudio
	}
	return audiodriver.AAudio
}()

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if runtime.GOOS == "android" && os.Getenv("GUNIM_DEBUG_AUDIO") == "" {
		// Android passes the application no environment to ask for the
		// speaker's log.
		_ = os.Setenv("GUNIM_DEBUG_AUDIO", "1")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := gunim.Main(ctx, func(a *gunim.App) error {
		w, err := a.NewWindow(gunim.WindowOptions{Title: name, Size: geom.Sz(380, 640), Root: widget.NewSurface()})
		if err != nil {
			return err
		}
		mix := audio.NewMixer()
		spk, err := speaker.Open(mix, speaker.Options{Name: name, Driver: native})
		if err != nil {
			return err
		}
		defer func() { _ = spk.Close() }()
		n := &notes{rate: mix.Rate(), mix: mix}
		n.start.Store(-1)
		mix.Play(n, audio.Options{})
		gunim.RegisterView(w, "sync", func(struct{}) *view { return newView(mix, spk, n) }, nil)
		c := w.Client()
		if err := c.Mount(gunim.Root, "sync", "sync", struct{}{}); err != nil {
			return err
		}
		// Comparing a picture with a sound takes a while, with no touch.
		if err := c.KeepAwake(true); err != nil && !errors.Is(err, driver.ErrNoAwaker) {
			log.Print(err)
		}
		for range c.Intents() {
		}
		return c.Err()
	})
	if errors.Is(err, driver.ErrNoDriver) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// The notes: a beat every 500 ms, four notes rising, then a beat's rest.
const (
	beat  = 500 * time.Millisecond
	beats = 5
	tone  = 80 * time.Millisecond
)

var pitches = [4]float64{440, 554.37, 659.25, 880}

// notes plays the four notes over and over.
type notes struct {
	rate int
	n    int64
	mix  *audio.Mixer
	// start is the mixer frame the first note starts at, or -1 before
	// the mixer first reads.
	start atomic.Int64
}

func (s *notes) Read(dst []float32) (int, error) {
	if s.start.Load() < 0 {
		s.start.Store(s.mix.Mixed())
	}
	per := int64(s.rate) * int64(beat) / int64(time.Second)
	long := int64(s.rate) * int64(tone) / int64(time.Second)
	for i := 0; i+1 < len(dst); i += 2 {
		v := float32(0)
		b, at := (s.n/per)%beats, s.n%per
		if b < 4 && at < long {
			env := 1 - float64(at)/float64(long)
			v = float32(0.4 * env * math.Sin(2*math.Pi*pitches[b]*float64(at)/float64(s.rate)))
		}
		dst[i], dst[i+1] = v, v
		s.n++
	}
	return len(dst) / 2, nil
}

var digitSize = theme.Length("synccheck.digit", 160)

type view struct {
	mix   *audio.Mixer
	spk   *speaker.Speaker
	notes *notes
	// shift moves the picture later, or earlier where negative.
	shift time.Duration
	// driver and quick are what the speaker was last set to.
	driver string
	quick  bool
	// saved says what Done logged last, shown a few seconds from savedAt.
	saved   string
	savedAt time.Time
	digit   *widget.Label
	info    *widget.Label
	button  []*widget.Button
}

// set opens the speaker as v.driver and v.quick say.
func (v *view) set() {
	if err := v.spk.Set(speaker.Options{Name: name, Driver: v.driver, Quick: v.quick}); err != nil {
		log.Print(err)
	}
}

// through names the driver playing.
func through(st speaker.State) string {
	if st.Driver == "" {
		return "the system's sound"
	}
	return st.Driver
}

// onOff names a switch's state.
func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func newView(mix *audio.Mixer, spk *speaker.Speaker, n *notes) *view {
	v := &view{mix: mix, spk: spk, notes: n, driver: native, digit: widget.NewLabel(""),
		info: widget.NewLabel("")}
	v.digit.Size = digitSize
	for _, ms := range []int{-50, -10, 10, 50} {
		b := widget.NewButton(fmt.Sprintf("%+d ms", ms))
		b.OnClick = func(*gunim.UI) gunim.Intent {
			v.shift += time.Duration(ms) * time.Millisecond
			return nil
		}
		v.button = append(v.button, b)
	}
	sw := widget.NewButton("Switch driver")
	sw.OnClick = func(*gunim.UI) gunim.Intent {
		if v.driver == native {
			v.driver = ""
		} else {
			v.driver = native
		}
		v.set()
		return nil
	}
	quick := widget.NewButton("Quick")
	quick.OnClick = func(*gunim.UI) gunim.Intent {
		v.quick = !v.quick
		quick.Active = v.quick
		v.set()
		return nil
	}
	done := widget.NewButton("Done")
	done.Kind = widget.ButtonPrimary
	done.OnClick = func(*gunim.UI) gunim.Intent {
		st := v.spk.State()
		device := time.Duration(0)
		if st.Info != nil && st.Rate > 0 {
			device = audio.DurationAt(int64(st.Info.Latency), st.Rate)
		}
		log.Printf("SYNCCHECK driver=%q quick=%v route=%v shift=%v latency=%v device=%v", st.Driver, v.quick,
			st.Route, v.shift, st.Latency.Round(100*time.Microsecond), device.Round(100*time.Microsecond))
		v.saved, v.savedAt = fmt.Sprintf("Saved: %s, Quick %s, picture %+v.", through(st), onOff(v.quick), v.shift), time.Now()
		return nil
	}
	v.button = append(v.button, sw, quick, done)
	return v
}

func (v *view) Children() []gunim.Node {
	kids := make([]gunim.Node, 0, 2+len(v.button))
	kids = append(kids, v.digit, v.info)
	for _, b := range v.button {
		kids = append(kids, b)
	}
	return kids
}

// Step keeps the window drawing every frame.
func (v *view) Step(time.Duration) bool { return true }

// lit returns the note, 0 to 3, whose number shows now, or -1: a number
// shows for 250 ms from when its note is heard, moved by shift.
func (v *view) lit() int {
	start := v.notes.start.Load()
	if start < 0 {
		return -1
	}
	rate := int64(v.notes.rate)
	h := v.mix.Heard() - start - rate*int64(v.shift)/int64(time.Second)
	if h < 0 {
		return -1
	}
	per := rate * int64(beat) / int64(time.Second)
	b, at := (h/per)%beats, h%per
	if b < 4 && at < rate/4 {
		return int(b)
	}
	return -1
}

func (v *view) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	if b := v.lit(); b >= 0 {
		v.digit.Text = strconv.Itoa(b + 1)
	} else {
		v.digit.Text = ""
	}
	st := v.spk.State()
	later := "later"
	if v.shift < 0 {
		later = "earlier"
	}
	v.info.Text = fmt.Sprintf("Playing through %s, route %v, Quick %s.\nReported latency %v.\n\nPicture shifted %v %s.\n\n"+
		"Shift the picture until each number shows exactly as its note sounds. Then press Done.",
		through(st), st.Route, onOff(v.quick), st.Latency.Round(100*time.Microsecond), v.shift.Abs(), later)
	if time.Since(v.savedAt) < 4*time.Second {
		v.info.Text += "\n\n" + v.saved
	}
	w := c.Max.W - 32
	i := 0
	x, y := float32(16), c.Max.H-200
	for kid := range kids.All {
		switch i {
		case 0:
			kid.Layout(gunim.Tight(geom.Sz(w, 220)))
			kid.Place(geom.Pt(c.Max.W/2-40, 80))
		case 1:
			kid.Layout(gunim.Tight(geom.Sz(w, 220)))
			kid.Place(geom.Pt(16, 320))
		default:
			sz := kid.Layout(gunim.Loose(geom.Sz(w, 64)))
			if x+sz.W > c.Max.W-16 {
				x, y = 16, y+sz.H+12
			}
			kid.Place(geom.Pt(x, y))
			x += sz.W + 12
		}
		i++
	}
	return c.Max
}

func (v *view) Paint(p *paint.Painter, _ gunim.Frame, size geom.Size, kids gunim.Children) {
	// Four boxes under the number, the one heard now lit.
	lit := v.lit()
	bw := (size.W - 32 - 3*12) / 4
	for i := range 4 {
		r := geom.Rect{Min: geom.Pt(16+float32(i)*(bw+12), 280), Max: geom.Pt(16+float32(i)*(bw+12)+bw, 300)}
		c := color.NRGBA{R: 60, G: 60, B: 70, A: 255}
		if i == lit {
			c = color.NRGBA{R: 80, G: 220, B: 120, A: 255}
		}
		p.RRect(r, 6, paint.Solid(c))
	}
	for kid := range kids.All {
		kid.Paint(p)
	}
}
