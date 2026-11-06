//go:build linux && !android && (amd64 || arm64)

// Package alsa plays and records through a sound card directly, by the
// kernel's own interface to it, with no library between and no cgo. It
// reaches the card past PipeWire and PulseAudio, so the program has the
// card to itself, all of its channels, and buffers as short as the card
// allows.
package alsa

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// A PCMInfo is a device of a sound card that plays or records.
type PCMInfo struct {
	Card, Device int
	// Name is the card's name; ID the device's own, as "ALC3271 Analog".
	Name, ID      string
	Plays, Record bool
}

// List lists the sound cards' devices, as /proc/asound tells of them.
func List() ([]PCMInfo, error) {
	cards, err := os.ReadFile("/proc/asound/cards")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	pcm, err := os.ReadFile("/proc/asound/pcm")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return parseList(string(cards), string(pcm)), nil
}

// parseList reads the text of /proc/asound/cards and /proc/asound/pcm.
func parseList(cards, pcm string) []PCMInfo {
	names := map[int]string{}
	for line := range strings.SplitSeq(cards, "\n") {
		// " 1 [M4             ]: USB-Audio - M4"
		num, rest, ok := strings.Cut(strings.TrimSpace(line), " [")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(num))
		if err != nil {
			continue
		}
		if _, name, ok := strings.Cut(rest, " - "); ok {
			names[n] = strings.TrimSpace(name)
		}
	}
	var out []PCMInfo
	for line := range strings.SplitSeq(pcm, "\n") {
		// "01-00: USB Audio : USB Audio : playback 1 : capture 1"
		f := strings.Split(line, ":")
		if len(f) < 3 {
			continue
		}
		c, d, ok := strings.Cut(strings.TrimSpace(f[0]), "-")
		if !ok {
			continue
		}
		card, err1 := strconv.Atoi(c)
		dev, err2 := strconv.Atoi(d)
		if err1 != nil || err2 != nil {
			continue
		}
		p := PCMInfo{Card: card, Device: dev, Name: names[card], ID: strings.TrimSpace(f[1])}
		for _, part := range f[3:] {
			part = strings.TrimSpace(part)
			p.Plays = p.Plays || strings.HasPrefix(part, "playback")
			p.Record = p.Record || strings.HasPrefix(part, "capture")
		}
		out = append(out, p)
	}
	return out
}

// The ioctls of the kernel's PCM interface, for a 64-bit kernel's
// structures, from sound/asound.h.
const (
	ioctlHWRefine = 0xc2604110
	ioctlHWParams = 0xc2604111
	ioctlHWFree   = 0x00004112
	ioctlSWParams = 0xc0884113
	ioctlDelay    = 0x80084121
	ioctlPrepare  = 0x00004140
	ioctlStart    = 0x00004142
	ioctlDrop     = 0x00004143
	ioctlWriteI   = 0x40184150
	ioctlReadI    = 0x80184151
	ioctlLink     = 0x40044160
	accessRWInter = 3
	paramAccess   = 0
	paramFormat   = 1
	firstInterval = 8
	paramChannels = 10
	paramRate     = 11
	paramPeriod   = 13
	paramPeriods  = 15
	paramBuffer   = 17
	intervalInt   = 1 << 2
	intervalEmpty = 1 << 3
	maskWords     = 8
	numMasks      = 8
	numIntervals  = 21
	startNever    = 0x7fffffff
)

// The formats a card may take, as sound/asound.h numbers them.
const (
	FormatS16   = 2
	FormatS24   = 6 // 24 bits, in the low bytes of 32
	FormatS32   = 10
	FormatFloat = 14
	FormatS243  = 32 // 24 bits in 3 bytes
)

// formats are those it takes, the best first.
var formats = []int{FormatS32, FormatFloat, FormatS24, FormatS243, FormatS16}

type mask [maskWords]uint32

type interval struct {
	min, max, flags uint32
}

// hwParams is struct snd_pcm_hw_params.
type hwParams struct {
	flags     uint32
	masks     [numMasks]mask
	intervals [numIntervals]interval
	rmask     uint32
	cmask     uint32
	info      uint32
	msbits    uint32
	rateNum   uint32
	rateDen   uint32
	fifoSize  uint64
	_         [64]byte
}

// swParams is struct snd_pcm_sw_params.
type swParams struct {
	tstampMode, periodStep, sleepMin uint32
	_                                uint32
	availMin, xferAlign              uint64
	startThreshold, stopThreshold    uint64
	silenceThreshold, silenceSize    uint64
	boundary                         uint64
	proto, tstampType                uint32
	_                                [56]byte
}

// xferi is struct snd_xferi.
type xferi struct {
	result int64
	buf    unsafe.Pointer
	frames uint64
}

// ErrBusy is returned by [Open] where another program has the card.
var ErrBusy = errors.New("alsa: another program is using the sound card")

// ErrXrun is returned by Read and Write where the card ran out of sound
// to play, or of room for what it recorded: [PCM.Prepare] goes on.
var ErrXrun = errors.New("alsa: the card ran out of time")

// ErrGone is returned by Read and Write where the card has gone, as
// where it was unplugged, or the system suspended.
var ErrGone = errors.New("alsa: the sound card went away")

// A PCM is an open device of a sound card, one way: playing or recording.
type PCM struct {
	fd int
	// Channels, Rate, Period and Periods are as [PCM.Configure] set them,
	// and Format and Bytes a sample's.
	Channels, Rate, Period, Periods int
	Format, Bytes                   int
}

// Open opens a device of a card, to play or to record. It doesn't wait
// for another program to let go.
func Open(card, device int, record bool) (*PCM, error) {
	dir := "p"
	if record {
		dir = "c"
	}
	path := fmt.Sprintf("/dev/snd/pcmC%dD%d%s", card, device, dir)
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.EBUSY) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("alsa: opening %s: %w", path, err)
	}
	// Open at once, but reads and writes wait.
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFL, 0); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("alsa: opening %s: %w", path, err)
	}
	return &PCM{fd: fd}, nil
}

func (p *PCM) ioctl(req uintptr, arg unsafe.Pointer) error {
	_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(p.fd), req, uintptr(arg))
	if e != 0 {
		return e
	}
	return nil
}

// anyParams returns parameters that allow anything.
func anyParams() *hwParams {
	h := &hwParams{rmask: ^uint32(0)}
	for i := range h.masks {
		for j := range h.masks[i] {
			h.masks[i][j] = ^uint32(0)
		}
	}
	for i := range h.intervals {
		h.intervals[i] = interval{max: ^uint32(0)}
	}
	return h
}

func (p *PCM) refine(h *hwParams) error {
	h.rmask = ^uint32(0)
	return p.ioctl(ioctlHWRefine, unsafe.Pointer(h))
}

func (h *hwParams) in(param int) *interval { return &h.intervals[param-firstInterval] }

func (h *hwParams) setMask(param, bit int) {
	m := &h.masks[param]
	*m = mask{}
	m[bit/32] = 1 << (bit % 32)
}

// StreamChannels returns how many channels a USB card's device plays and
// records, as it says in /proc/asound without being opened, 0 where it
// doesn't say.
func StreamChannels(card, device int) (playback, capture int) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/asound/card%d/stream%d", card, device))
	if err != nil {
		return 0, 0
	}
	return parseStream(string(b))
}

// parseStream reads the text of a /proc/asound/cardN/streamN file, for the
// most channels each way.
func parseStream(text string) (playback, capture int) {
	on := &playback
	for line := range strings.SplitSeq(text, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "Playback:"):
			on = &playback
		case strings.HasPrefix(t, "Capture:"):
			on = &capture
		case strings.HasPrefix(t, "Channels:"):
			if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(t, "Channels:"))); err == nil {
				*on = max(*on, n)
			}
		}
	}
	return playback, capture
}

// Range tells what a device allows: of channels, rate and period.
type Range struct {
	MaxChannels          int
	MinPeriod, MaxPeriod int
	MinRate, MaxRate     int
}

// Range returns what the device allows.
func (p *PCM) Range() (Range, error) {
	h := anyParams()
	if err := p.refine(h); err != nil {
		return Range{}, fmt.Errorf("alsa: asking what the device allows: %w", err)
	}
	return Range{
		MaxChannels: int(h.in(paramChannels).max),
		MinPeriod:   int(h.in(paramPeriod).min), MaxPeriod: int(h.in(paramPeriod).max),
		MinRate: int(h.in(paramRate).min), MaxRate: int(h.in(paramRate).max),
	}, nil
}

// Configure sets the device to play or record all its channels, at the
// rate nearest rate, in periods of the size nearest period, periods of
// them in its buffer. It stays set till [PCM.Close].
func (p *PCM) Configure(rate, period, periods int) error {
	h := anyParams()
	h.setMask(paramAccess, accessRWInter)
	if err := p.refine(h); err != nil {
		return fmt.Errorf("alsa: the device can't read and write frames in turn: %w", err)
	}
	format := -1
	for _, f := range formats {
		try := *h
		try.setMask(paramFormat, f)
		if p.refine(&try) == nil {
			*h, format = try, f
			break
		}
	}
	if format < 0 {
		return errors.New("alsa: the device takes no format known here")
	}
	// The rate first: a card may have fewer channels at a high rate.
	if err := p.near(h, paramRate, uint32(rate)); err != nil {
		return fmt.Errorf("alsa: setting the rate: %w", err)
	}
	// All of its channels.
	ch := h.in(paramChannels).max
	if err := p.pin(h, paramChannels, ch); err != nil {
		return fmt.Errorf("alsa: setting %d channels: %w", ch, err)
	}
	if err := p.near(h, paramPeriod, uint32(period)); err != nil {
		return fmt.Errorf("alsa: setting the period: %w", err)
	}
	if err := p.near(h, paramPeriods, uint32(periods)); err != nil {
		return fmt.Errorf("alsa: setting the periods: %w", err)
	}
	h.rmask = ^uint32(0)
	if err := p.ioctl(ioctlHWParams, unsafe.Pointer(h)); err != nil {
		if errors.Is(err, syscall.EBUSY) {
			return ErrBusy
		}
		return fmt.Errorf("alsa: setting up the device: %w", err)
	}
	p.Channels, p.Rate = int(h.in(paramChannels).min), int(h.in(paramRate).min)
	p.Period, p.Periods = int(h.in(paramPeriod).min), int(h.in(paramPeriods).min)
	p.Format, p.Bytes = format, bytesOf(format)
	if p.Periods < 2 {
		return fmt.Errorf("alsa: the device holds %d buffer at a time, and needs 2", p.Periods)
	}
	sw := swParams{availMin: uint64(p.Period), periodStep: 1, startThreshold: startNever,
		stopThreshold: uint64(p.Period * p.Periods)}
	if err := p.ioctl(ioctlSWParams, unsafe.Pointer(&sw)); err != nil {
		return fmt.Errorf("alsa: setting up the device: %w", err)
	}
	return nil
}

func bytesOf(format int) int {
	switch format {
	case FormatS16:
		return 2
	case FormatS243:
		return 3
	}
	return 4
}

// pin narrows a parameter to one value.
func (p *PCM) pin(h *hwParams, param int, v uint32) error {
	try := *h
	*try.in(param) = interval{min: v, max: v, flags: intervalInt}
	if err := p.refine(&try); err != nil {
		return err
	}
	*h = try
	return nil
}

// near narrows a parameter to the value it allows nearest v.
func (p *PCM) near(h *hwParams, param int, v uint32) error {
	in := *h.in(param)
	v = max(in.min, min(v, in.max))
	if p.pin(h, param, v) == nil {
		return nil
	}
	// The kernel narrows a range to what the device allows, so a range
	// each side of v says what is nearest on that side.
	below, above := p.within(h, param, in.min, v, true), p.within(h, param, v, in.max, false)
	switch {
	case below == nil && above == nil:
		return p.pin(h, param, in.max)
	case below == nil:
		return p.pin(h, param, *above)
	case above == nil:
		return p.pin(h, param, *below)
	case v-*below <= *above-v:
		return p.pin(h, param, *below)
	}
	return p.pin(h, param, *above)
}

// within returns the highest or lowest value a parameter allows from lo
// to hi, nil where it allows none.
func (p *PCM) within(h *hwParams, param int, lo, hi uint32, highest bool) *uint32 {
	try := *h
	*try.in(param) = interval{min: lo, max: hi}
	if p.refine(&try) != nil {
		return nil
	}
	v := try.in(param).min
	if highest {
		v = try.in(param).max
	}
	return &v
}

// Prepare readies the device to start, after a [ErrXrun] too.
func (p *PCM) Prepare() error { return p.ioctl(ioctlPrepare, nil) }

// Start starts the device, and those linked to it.
func (p *PCM) Start() error { return p.ioctl(ioctlStart, nil) }

// Drop stops the device, and those linked to it, at once. It wakes a
// Read or Write of another thread.
func (p *PCM) Drop() error { return p.ioctl(ioctlDrop, nil) }

// Link has the two devices start and stop together, on one clock.
func (p *PCM) Link(o *PCM) error {
	// This ioctl alone takes its argument as it is, not its address.
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(p.fd), ioctlLink, uintptr(o.fd)); e != 0 {
		return e
	}
	return nil
}

// Delay returns how many frames stand between what is written next and
// its sound at the device's converter, or what was read and now.
func (p *PCM) Delay() (int, error) {
	var d int64
	if err := p.ioctl(ioctlDelay, unsafe.Pointer(&d)); err != nil {
		return 0, err
	}
	return int(d), nil
}

// Write plays frames of samples, as the format says, in b, waiting as
// the device has no room for them.
func (p *PCM) Write(b []byte, frames int) (int, error) { return p.xfer(ioctlWriteI, b, frames) }

// Read records frames of samples into b, waiting as they come.
func (p *PCM) Read(b []byte, frames int) (int, error) { return p.xfer(ioctlReadI, b, frames) }

func (p *PCM) xfer(req uintptr, b []byte, frames int) (int, error) {
	size := p.Channels * p.Bytes
	done := 0
	for done < frames {
		if err := p.Wait(); err != nil {
			return done, err
		}
		x := xferi{buf: unsafe.Pointer(&b[done*size]), frames: uint64(frames - done)}
		// Having waited, this doesn't wait, or not for long. RawSyscall, as
		// coming back from a Syscall the thread could have to wait for a
		// place in Go's scheduler, behind goroutines of no priority at all.
		var err error
		if _, _, e := unix.RawSyscall(unix.SYS_IOCTL, uintptr(p.fd), req, uintptr(unsafe.Pointer(&x))); e != 0 {
			err = e
		}
		switch {
		case err == nil:
			// A signal can end a transfer part way.
			done += int(x.result)
		case errors.Is(err, syscall.EINTR), errors.Is(err, syscall.EAGAIN):
		case errors.Is(err, syscall.EPIPE):
			return done, ErrXrun
		case errors.Is(err, syscall.ESTRPIPE), errors.Is(err, syscall.ENODEV), errors.Is(err, syscall.EIO), errors.Is(err, syscall.EBADFD):
			return done, ErrGone
		default:
			return done, err
		}
	}
	return done, nil
}

// Wait waits until the device has a period of room or of sound. A signal
// ends the wait at once, unlike a wait in the transfer itself, so a thread
// here keeps nothing in Go waiting: a garbage collection stops it as any
// other. A wait far longer than a period is a card that has stalled. That
// wait is left to the scheduler, to take as long as it takes.
func (p *PCM) Wait() error {
	fds := [1]unix.PollFd{{Fd: int32(p.fd), Events: unix.POLLIN | unix.POLLOUT}}
	limit := unix.NsecToTimespec(int64(p.Period) * 4 * 1e9 / int64(max(p.Rate, 1)))
	n, _, e := unix.RawSyscall6(unix.SYS_PPOLL, uintptr(unsafe.Pointer(&fds[0])), 1, uintptr(unsafe.Pointer(&limit)), 0, 0, 0)
	switch {
	case e == syscall.EINTR:
		return nil
	case e != 0:
		return e
	case n > 0:
		return nil
	}
	// Stalled.
	fds[0].Revents = 0
	_, err := unix.Poll(fds[:], alsaStallMillis)
	if err != nil && !errors.Is(err, syscall.EINTR) {
		return err
	}
	if fds[0].Revents == 0 && err == nil {
		return ErrGone
	}
	return nil
}

// alsaStallMillis is how long a card may stall before it counts as gone.
const alsaStallMillis = 5000

// Close lets the device go.
func (p *PCM) Close() error {
	_ = p.ioctl(ioctlHWFree, nil)
	return unix.Close(p.fd)
}
