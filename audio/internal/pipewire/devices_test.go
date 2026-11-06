//go:build linux && !android && (amd64 || arm64)

package pipewire

import (
	"slices"
	"testing"

	"github.com/jfreymuth/pulse/proto"
)

func props(card string) proto.PropList {
	return proto.PropList{"alsa.card_name": proto.PropListString(card)}
}

func TestDevicesPairACardsOutputAndInput(t *testing.T) {
	four := proto.ChannelMap{proto.ChannelFrontLeft, proto.ChannelFrontRight, proto.ChannelRearLeft, proto.ChannelRearRight}
	got := pairDevices([]*proto.GetSinkInfoReply{
		{SinkName: "virtual", Device: "A virtual one", CardIndex: noIndex, ChannelMap: proto.ChannelMap{proto.ChannelMono}},
		{SinkName: "alsa_output.usb-M4-B.analog-surround-40", Device: "M4 Analog Surround 4.0", CardIndex: 2, Properties: props("M4"), ChannelMap: four},
		{SinkName: "alsa_output.usb-M4-A.analog-surround-40.49", Device: "M4 Analog Surround 4.0", CardIndex: 1, Properties: props("M4"), ChannelMap: four},
	}, []*proto.GetSourceInfoReply{
		{SourceName: "alsa_output.usb-M4-A.analog-surround-40.49.monitor", MonitorSourceIndex: 3, CardIndex: 1, ChannelMap: four},
		{SourceName: "alsa_input.usb-M4-A.analog-surround-40.49", MonitorSourceIndex: noIndex, CardIndex: 1, ChannelMap: four},
	})
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	m := got[0]
	if m.Name != "M4" || m.Input != "alsa_input.usb-M4-A.analog-surround-40.49" {
		t.Errorf("got %+v", m)
	}
	if want := []uint32{channelFL, channelFR, 12, 13}; !slices.Equal(m.OutPositions, want) || !slices.Equal(m.InPositions, want) {
		t.Errorf("got %+v", m)
	}
	if got[1].Name != "M4 2" || got[1].Input != "" {
		t.Errorf("a second card of one name is %+v", got[1])
	}
	if got[2].Name != "A virtual one" || got[2].Input != "" || got[2].OutPositions[0] != 2 {
		t.Errorf("got %+v", got[2])
	}
}

func TestAProAudioCardsInputIsFoundByItsCard(t *testing.T) {
	aux := proto.ChannelMap{proto.ChannelAux0, proto.ChannelAux0 + 1}
	got := pairDevices([]*proto.GetSinkInfoReply{
		{SinkName: "alsa_output.usb-M4.pro-output-0", CardIndex: 1, Properties: props("M4"), ChannelMap: aux},
	}, []*proto.GetSourceInfoReply{
		{SourceName: "alsa_input.other.pro-input-0", MonitorSourceIndex: noIndex, CardIndex: 9, ChannelMap: aux},
		{SourceName: "alsa_input.usb-M4.pro-input-0", MonitorSourceIndex: noIndex, CardIndex: 1, ChannelMap: aux},
	})
	if len(got) != 1 || got[0].Input != "alsa_input.usb-M4.pro-input-0" {
		t.Errorf("got %+v", got)
	}
}

func TestACardOfSeveralOutputsTellsThemApart(t *testing.T) {
	two := proto.ChannelMap{proto.ChannelFrontLeft, proto.ChannelFrontRight}
	got := pairDevices([]*proto.GetSinkInfoReply{
		{SinkName: "alsa_output.a", Device: "HDA Analog", CardIndex: 0, Properties: props("HDA"), ChannelMap: two},
		{SinkName: "alsa_output.b", Device: "HDA HDMI", CardIndex: 0, Properties: props("HDA"), ChannelMap: two},
	}, nil)
	if got[0].Name != "HDA Analog" || got[1].Name != "HDA HDMI" {
		t.Errorf("got %+v", got)
	}
}

func TestPositionsOfAProAudioCardAreAux(t *testing.T) {
	m := proto.ChannelMap{proto.ChannelAux0, proto.ChannelAux0 + 1, proto.ChannelAux31}
	if got, want := positionsOf(m), []uint32{channelAux0, channelAux0 + 1, channelAux0 + 31}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
