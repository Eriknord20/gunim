//go:build linux && !android && (amd64 || arm64)

package pipewire

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jfreymuth/pulse"
	"github.com/jfreymuth/pulse/proto"
)

// A Device is a device PipeWire plays on, and records from where it can.
type Device struct {
	// Name is what to call it: its card's, as "M4".
	Name string
	// Output and Input are its nodes, to open with Config, Input empty for
	// a device that doesn't record.
	Output, Input string
	// OutPositions and InPositions name its channels, as Config takes them.
	OutPositions, InPositions []uint32
}

// listWait is how long the server may take to list its devices.
const listWait = time.Second

// noIndex is PulseAudio's PA_INVALID_INDEX.
const noIndex = 0xffffffff

// Devices lists the devices PipeWire plays on. The registry's list would
// have PipeWire call Go on its own thread, so it comes from the server's
// PulseAudio protocol, which speaks of the same nodes by the same names,
// in Go, over a socket.
func Devices() ([]Device, error) {
	if !Available() {
		return nil, errNoServer
	}
	c, err := pulse.NewClient(pulse.ClientApplicationName(programName()), pulse.ClientTimeout(listWait))
	if err != nil {
		return nil, fmt.Errorf("pipewire: asking for the devices: %w", err)
	}
	defer c.Close()
	var sinks proto.GetSinkInfoListReply
	if err := c.RawRequest(&proto.GetSinkInfoList{}, &sinks); err != nil {
		return nil, fmt.Errorf("pipewire: listing the outputs: %w", err)
	}
	var sources proto.GetSourceInfoListReply
	if err := c.RawRequest(&proto.GetSourceInfoList{}, &sources); err != nil {
		return nil, fmt.Errorf("pipewire: listing the inputs: %w", err)
	}
	return pairDevices(sinks, sources), nil
}

// pairDevices makes devices of the outputs, each with an input of its card,
// by the card's own name, which stays as its profile changes. The outputs go
// by their node names, so two cards of one name keep their numbers, and a card
// of several outputs tells them apart by what each is.
func pairDevices(sinks []*proto.GetSinkInfoReply, sources []*proto.GetSourceInfoReply) []Device {
	sinks = slices.SortedFunc(slices.Values(sinks), func(a, b *proto.GetSinkInfoReply) int {
		return strings.Compare(a.SinkName, b.SinkName)
	})
	onCard := map[uint32]int{}
	for _, k := range sinks {
		onCard[k.CardIndex]++
	}
	seen := map[string]int{}
	out := make([]Device, 0, len(sinks))
	for _, k := range sinks {
		d := Device{Name: deviceName(k, onCard[k.CardIndex] > 1), Output: k.SinkName, OutPositions: positionsOf(k.ChannelMap)}
		// An output's twin by name, else any input of its card.
		twin := strings.Replace(k.SinkName, "alsa_output", "alsa_input", 1)
		for _, s := range sources {
			if s.MonitorSourceIndex != noIndex {
				continue // what an output plays, not an input
			}
			named := s.SourceName == twin
			if named || k.CardIndex != noIndex && s.CardIndex == k.CardIndex && d.Input == "" {
				d.Input, d.InPositions = s.SourceName, positionsOf(s.ChannelMap)
			}
			if named {
				break
			}
		}
		seen[d.Name]++
		if n := seen[d.Name]; n > 1 {
			d.Name = fmt.Sprintf("%s %d", d.Name, n)
		}
		out = append(out, d)
	}
	return out
}

// deviceName names an output by its card, with what it is where the card has
// several, or as the desktop does where it has no card.
func deviceName(k *proto.GetSinkInfoReply, several bool) string {
	card := ""
	if v, ok := k.Properties["alsa.card_name"]; ok {
		card = strings.TrimRight(string(v), "\x00")
	}
	switch {
	case card != "" && !several:
		return card
	case k.Device != "":
		return k.Device
	}
	return k.SinkName
}

// SPA's numbers for the channels PulseAudio's map doesn't count in the same
// order, from spa/param/audio/raw.h.
var pulseToSPA = map[byte]uint32{
	proto.ChannelMono:           2,
	proto.ChannelFrontLeft:      channelFL,
	proto.ChannelFrontRight:     channelFR,
	proto.ChannelFrontCenter:    5,
	proto.ChannelLFE:            6,
	proto.ChannelLeftSide:       7,
	proto.ChannelRightSide:      8,
	proto.ChannelLeftCenter:     9,
	proto.ChannelRightCenter:    10,
	proto.ChannelRearCenter:     11,
	proto.ChannelRearLeft:       12,
	proto.ChannelRearRight:      13,
	proto.ChannelTopCenter:      14,
	proto.ChannelTopFrontLeft:   15,
	proto.ChannelTopFrontCenter: 16,
	proto.ChannelTopFrontRight:  17,
	proto.ChannelTopRearLeft:    18,
	proto.ChannelTopRearCenter:  19,
	proto.ChannelTopRearRight:   20,
}

// positionsOf turns PulseAudio's channel map into SPA's channels.
func positionsOf(m proto.ChannelMap) []uint32 {
	out := make([]uint32, len(m))
	for i, c := range m {
		if p, ok := pulseToSPA[c]; ok {
			out[i] = p
			continue
		}
		// The aux channels, and any other.
		out[i] = channelAux0 + uint32(c) - proto.ChannelAux0
		if c < proto.ChannelAux0 || c > proto.ChannelAux31 {
			out[i] = channelAux0 + uint32(i)
		}
	}
	return out
}
