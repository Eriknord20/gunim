//go:build linux && !android && (amd64 || arm64)

package driver

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/marrasen/gunim/audio/internal/alsa"
	"github.com/marrasen/gunim/audio/rt"
)

const (
	// alsaPeriods is how many buffers the card holds. As one ends, the next
	// plays and one behind it stands ready, and a third is filled.
	alsaPeriods = 3
	// alsaPreferred is the buffer a card takes where none is asked for.
	alsaPreferred = 256
	// alsaBusyWait is how long a card already in use is waited for, as a
	// sound server asked to let go of it closes it.
	alsaBusyWait = 2 * time.Second
	// alsaBreathe is how long the card's thread goes without a sleep, at
	// the most, though it finds no reason to wait.
	alsaBreathe = 20 * time.Millisecond
)

// alsaCard is a sound card's device, as listed.
type alsaCard struct {
	name         string
	card, device int
	plays, recs  bool
}

// alsaCards lists the cards' devices that play, under their names. HDMI
// outputs are left out, as most lead nowhere and a device's name for them
// says little.
func alsaCards() []alsaCard {
	list, err := alsa.List()
	if err != nil {
		return nil
	}
	return namedCards(list)
}

// namedCards names the devices of list: by their card, with the device's
// own name where a card has several that count, and a number for a second
// card of one name.
func namedCards(list []alsa.PCMInfo) []alsaCard {
	onCard := map[int]int{}
	var kept []alsa.PCMInfo
	for _, p := range list {
		if !p.Plays || strings.HasPrefix(p.ID, "HDMI") {
			continue
		}
		onCard[p.Card]++
		kept = append(kept, p)
	}
	seen := map[string]int{}
	out := make([]alsaCard, 0, len(kept))
	for _, p := range kept {
		name := ALSAPrefix + p.Name
		if onCard[p.Card] > 1 {
			name += ", " + p.ID
		}
		seen[name]++
		if n := seen[name]; n > 1 {
			name = fmt.Sprintf("%s %d", name, n)
		}
		out = append(out, alsaCard{name: name, card: p.Card, device: p.Device, plays: p.Plays, recs: p.Record})
	}
	return out
}

func alsaDrivers() []Driver {
	cards := alsaCards()
	out := make([]Driver, 0, len(cards))
	for _, c := range cards {
		out = append(out, Driver{Name: c.name})
	}
	return out
}

// alsaFind returns the card of a name.
func alsaFind(name string) (alsaCard, bool) {
	for _, c := range alsaCards() {
		if c.name == name {
			return c, true
		}
	}
	return alsaCard{}, false
}

// alsaChannelCount asks a device how many channels it has, with no wait
// and no reservation: listing a card's channels takes it from no one. Where
// another program holds it, a USB card says through /proc, and any other
// is taken as stereo.
func alsaChannelCount(c alsaCard, record bool) int {
	if p, err := alsa.Open(c.card, c.device, record); err == nil {
		defer func() { _ = p.Close() }()
		if r, err := p.Range(); err == nil {
			return r.MaxChannels
		}
	}
	plays, records := alsa.StreamChannels(c.card, c.device)
	n := plays
	if record {
		n = records
	}
	if n == 0 {
		return 2
	}
	return n
}

func alsaChannels(name string) (inputs, outputs []string, err error) {
	c, ok := alsaFind(name)
	if !ok {
		return nil, nil, ErrUnsupported
	}
	outputs = numbered("Output", alsaChannelCount(c, false))
	if c.recs {
		inputs = numbered("Input", alsaChannelCount(c, true))
	}
	return inputs, outputs, nil
}

// waitCard runs f again while the card is busy, for a while.
func waitCard[T any](f func() (T, error)) (T, error) {
	end := time.Now().Add(alsaBusyWait)
	for {
		v, err := f()
		if !errors.Is(err, alsa.ErrBusy) || time.Now().After(end) {
			return v, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A Device is an open sound card, playing.
type Device struct {
	info     Info
	cfg      Config
	out, in  alsaPCM
	loop     alsaLoop
	release  func()
	done     chan struct{}
	quit     atomic.Bool
	closed   atomic.Bool
	gone     atomic.Bool
	closePCM func()
	periods  int
}

// alsaPCM is an open device, as [Device] needs it: a test's own has no
// card.
type alsaPCM interface {
	Write(b []byte, frames int) (int, error)
	Read(b []byte, frames int) (int, error)
	Wait() error
	Prepare() error
	Start() error
	Drop() error
}

// Open opens the sound card c names and starts it playing, past any sound
// server.
func Open(c Config) (*Device, error) {
	card, ok := alsaFind(c.Name)
	if !ok {
		if !strings.HasPrefix(c.Name, ALSAPrefix) {
			return nil, ErrUnsupported
		}
		return nil, fmt.Errorf("alsa: there is no sound card %q now", strings.TrimPrefix(c.Name, ALSAPrefix))
	}
	d := &Device{cfg: c, done: make(chan struct{})}
	d.release = reserveCard(card.card, c.Log)
	fail := func(err error) (*Device, error) {
		d.release()
		if errors.Is(err, alsa.ErrBusy) {
			return nil, fmt.Errorf("%w: close what plays through it, or choose it in no other program", err)
		}
		return nil, err
	}
	var r alsa.Range
	end := time.Now().Add(alsaBusyWait)
	out, err := waitCard(func() (*alsa.PCM, error) {
		p, err := alsa.Open(card.card, card.device, false)
		if err != nil {
			return nil, err
		}
		r, err = p.Range()
		// A USB card's two ways share a clock, so one buffer or rate alone
		// can mean a sound server holds the other way, and is letting go. A
		// card still so after the wait really does have only that one.
		pinned := c.Buffer > 0 && r.MinPeriod == r.MaxPeriod && r.MinPeriod != c.Buffer ||
			c.Rate > 0 && r.MinRate == r.MaxRate && r.MinRate != c.Rate
		if err == nil && pinned && time.Now().Before(end) {
			err = alsa.ErrBusy
		}
		if err != nil {
			_ = p.Close()
			return nil, err
		}
		return p, nil
	})
	if err != nil {
		return fail(err)
	}
	buffer := fitBuffer(c.Buffer, r.MinPeriod, min(r.MaxPeriod, 8192), alsaPreferred, 1)
	if c.Buffer <= 0 {
		buffer = alsaPreferred
	}
	rate := c.Rate
	if rate <= 0 {
		rate = 48000
	}
	if err := out.Configure(rate, buffer, alsaPeriods); err != nil {
		_ = out.Close()
		return fail(err)
	}
	var in *alsa.PCM
	if card.recs && c.Record != nil {
		in, err = waitCard(func() (*alsa.PCM, error) { return alsa.Open(card.card, card.device, true) })
		if err == nil {
			if err = in.Configure(out.Rate, out.Period, alsaPeriods); err == nil && (in.Rate != out.Rate || in.Period != out.Period) {
				err = fmt.Errorf("alsa: the inputs run at %d Hz, %d frames, the outputs at %d Hz, %d frames", in.Rate, in.Period, out.Rate, out.Period)
			}
			if err == nil {
				err = in.Link(out)
			}
			if err != nil {
				_ = in.Close()
			}
		}
		if err != nil {
			_ = out.Close()
			return fail(err)
		}
	}
	d.closePCM = func() {
		_ = out.Close()
		if in != nil {
			_ = in.Close()
		}
	}
	d.out = out
	var inFormat alsaFormat
	if in != nil {
		d.in = in
		inFormat = alsaFormat{in.Format, in.Bytes, in.Channels}
	}
	played := layout(c.Outputs, out.Channels)
	d.info = Info{Driver: c.Name, Rate: out.Rate, Buffer: out.Period, MinBuffer: r.MinPeriod, MaxBuffer: min(r.MaxPeriod, 8192),
		PreferredBuffer: alsaPreferred, Latency: out.Period * (out.Periods - 1), Bits: 8 * out.Bytes, Float: out.Format == alsa.FormatFloat,
		Outputs: out.Channels, OutputNames: numbered("Output", out.Channels), Played: played, TellsOverrun: true}
	if in != nil {
		d.info.Inputs = in.Channels
		d.info.Recorded = min(in.Channels, MaxInputs)
		d.info.InputNames = numbered("Input", d.info.Recorded)
		d.info.InputLatency = in.Period
	}
	d.periods = out.Periods
	d.loop = newALSALoop(c, out.Period, alsaFormat{out.Format, out.Bytes, out.Channels}, inFormat, played)
	if err := d.begin(); err != nil {
		d.closePCM()
		return fail(err)
	}
	go d.run()
	return d, nil
}

// begin readies the card with its buffers filled, and starts it, the
// inputs with the outputs. From then on each cycle waits for a period's
// room before it fills one, so a buffer filled is heard all the periods
// but one later, and it has that long to be late in before the card runs
// out.
func (d *Device) begin() error {
	if err := d.out.Prepare(); err != nil {
		return fmt.Errorf("alsa: preparing the device: %w", err)
	}
	clear(d.loop.outBytes)
	for range d.periods {
		if _, err := d.out.Write(d.loop.outBytes, d.loop.frames); err != nil {
			return fmt.Errorf("alsa: filling the device: %w", err)
		}
	}
	if err := d.out.Start(); err != nil {
		return fmt.Errorf("alsa: starting the device: %w", err)
	}
	return nil
}

// run plays until the device closes: one thread, at the system's audio
// priority, as a card's period is too short for anything else.
func (d *Device) run() {
	defer close(d.done)
	leave := rt.Enter()
	defer leave()
	breathed := time.Now()
	for !d.quit.Load() {
		err := d.cycle()
		// A song too heavy for the card never waits on it, and the system
		// limits how long a real-time thread may run without a sleep.
		if now := time.Now(); now.Sub(breathed) > alsaBreathe {
			breathed = now
			rt.Breathe()
		}
		switch {
		case err == nil:
		case d.quit.Load():
			return
		case errors.Is(err, alsa.ErrXrun):
			if d.cfg.Overrun != nil {
				d.cfg.Overrun()
			}
			if err := d.again(); err != nil {
				d.lost(err)
				return
			}
		default:
			d.lost(err)
			return
		}
	}
}

// again starts the card anew after it ran out of time.
func (d *Device) again() error {
	_ = d.out.Drop()
	return d.begin()
}

// lost tells that the card has gone, as where unplugged, and asks to be
// opened again.
func (d *Device) lost(err error) {
	if d.quit.Load() {
		return
	}
	d.gone.Store(true)
	if d.cfg.Log != nil {
		d.cfg.Log("%s stopped: %v", d.info.Driver, err)
	}
	if d.cfg.Reset != nil {
		go d.cfg.Reset()
	}
}

// cycle takes a period of the inputs, as the card has it, and plays the
// next.
func (d *Device) cycle() error {
	l := &d.loop
	if d.in != nil {
		// The card waits for its inputs to be read, wanted or not.
		if _, err := d.in.Read(l.inBytes, l.frames); err != nil {
			return err
		}
		l.record()
	} else if err := d.out.Wait(); err != nil {
		// Fill at the last moment, as with inputs.
		return err
	}
	l.fill()
	_, err := d.out.Write(l.outBytes, l.frames)
	return err
}

// Info says how the device plays.
func (d *Device) Info() Info { return d.info }

// ControlPanel opens the driver's own settings: a sound card has none.
func (*Device) ControlPanel() error { return errALSAPanel }

// ControlPanel opens the settings of the driver named.
func ControlPanel(name string) error {
	if strings.HasPrefix(name, ALSAPrefix) {
		return errALSAPanel
	}
	return ErrUnsupported
}

var errALSAPanel = errors.New("alsa: a sound card has no control panel: set its buffer in this program's settings")

// Close stops the card and lets it go, for a sound server to take back.
func (d *Device) Close() error {
	if d.closed.Swap(true) {
		return nil
	}
	d.quit.Store(true)
	// Dropping wakes the thread where it waits on the card.
	_ = d.out.Drop()
	select {
	case <-d.done:
	case <-time.After(time.Second):
		return ErrNotAnswering
	}
	d.closePCM()
	d.release()
	return nil
}

// alsaFormat is how a device takes its samples.
type alsaFormat struct{ format, bytes, channels int }

// alsaLoop turns the sound between the program's samples and the card's,
// a period at a time, and allocates nothing as it runs.
type alsaLoop struct {
	cfg              Config
	frames           int
	out, in          alsaFormat
	played           []int
	chans            int
	sound, inSound   []float32
	outBytes         []byte
	inBytes          []byte
	recordedChannels int
}

func newALSALoop(c Config, frames int, out, in alsaFormat, played []int) alsaLoop {
	l := alsaLoop{cfg: c, frames: frames, out: out, in: in, played: played, chans: 2}
	if c.FillN != nil && len(c.Outputs) > 0 {
		l.chans = len(c.Outputs)
	}
	l.sound = make([]float32, frames*l.chans)
	l.outBytes = make([]byte, frames*out.channels*out.bytes)
	if in.channels > 0 {
		l.recordedChannels = min(in.channels, MaxInputs)
		l.inSound = make([]float32, frames*l.recordedChannels)
		l.inBytes = make([]byte, frames*in.channels*in.bytes)
	}
	return l
}

// fill asks for a period of sound, and puts it in the card's format.
func (l *alsaLoop) fill() {
	if l.cfg.FillN != nil {
		l.cfg.FillN(l.sound, l.chans)
	} else {
		l.cfg.Fill(l.sound)
	}
	clear(l.outBytes)
	if l.out.channels == 1 && l.cfg.FillN == nil && len(l.played) == 1 {
		// One output hears the pair averaged.
		for i := range l.frames {
			putSample(l.outBytes[i*l.out.bytes:], l.out, (l.sound[2*i]+l.sound[2*i+1])/2)
		}
		return
	}
	for c, o := range l.played {
		if o < 0 || c >= l.chans {
			continue
		}
		for i := range l.frames {
			at := (i*l.out.channels + o) * l.out.bytes
			putSample(l.outBytes[at:], l.out, l.sound[i*l.chans+c])
		}
	}
}

// record hands on a period of the inputs, where wanted.
func (l *alsaLoop) record() {
	if l.cfg.Record == nil || (l.cfg.Recording != nil && !l.cfg.Recording()) {
		return
	}
	n := l.recordedChannels
	for i := range l.frames {
		for c := range n {
			at := (i*l.in.channels + c) * l.in.bytes
			l.inSound[i*n+c] = getSample(l.inBytes[at:], l.in)
		}
	}
	l.cfg.Record(l.inSound, n)
}

// putSample writes v, from -1 to 1, to b in format f.
func putSample(b []byte, f alsaFormat, v float32) {
	if f.format == alsa.FormatFloat {
		*(*float32)(unsafe.Pointer(&b[0])) = v
		return
	}
	if v != v { // NaN
		v = 0
	}
	x := float64(max(min(v, 1), -1))
	switch f.format {
	case alsa.FormatS32:
		n := int32(max(min(x*(1<<31), math.MaxInt32), math.MinInt32))
		b[0], b[1], b[2], b[3] = byte(n), byte(n>>8), byte(n>>16), byte(n>>24)
	case alsa.FormatS24:
		n := int32(max(min(x*(1<<23), 1<<23-1), -1<<23))
		b[0], b[1], b[2], b[3] = byte(n), byte(n>>8), byte(n>>16), byte(n>>24)
	case alsa.FormatS243:
		n := int32(max(min(x*(1<<23), 1<<23-1), -1<<23))
		b[0], b[1], b[2] = byte(n), byte(n>>8), byte(n>>16)
	case alsa.FormatS16:
		n := int32(max(min(x*(1<<15), 1<<15-1), -1<<15))
		b[0], b[1] = byte(n), byte(n>>8)
	}
}

// getSample reads a sample of format f from b, as -1 to 1.
func getSample(b []byte, f alsaFormat) float32 {
	switch f.format {
	case alsa.FormatFloat:
		return *(*float32)(unsafe.Pointer(&b[0]))
	case alsa.FormatS32:
		n := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16 | int32(b[3])<<24
		return float32(float64(n) / (1 << 31))
	case alsa.FormatS24:
		n := int32(b[0])<<8 | int32(b[1])<<16 | int32(b[2])<<24
		return float32(float64(n) / (1 << 31))
	case alsa.FormatS243:
		n := int32(b[0])<<8 | int32(b[1])<<16 | int32(b[2])<<24
		return float32(float64(n) / (1 << 31))
	case alsa.FormatS16:
		n := int16(b[0]) | int16(b[1])<<8
		return float32(n) / (1 << 15)
	}
	return 0
}
