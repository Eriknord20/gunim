package pipewire

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// formatPod48k is what spa_format_audio_raw_build made, built against
// PipeWire 1.0.5's headers, for SPA_PARAM_EnumFormat and a
// spa_audio_info_raw of SPA_AUDIO_FORMAT_F32, 48000 Hz, two channels at
// FL and FR.
var formatPod48k = []byte{
	160, 0, 0, 0, 15, 0, 0, 0, 3, 0, 4, 0, 3, 0, 0, 0,
	1, 0, 0, 0, 0, 0, 0, 0, 4, 0, 0, 0, 3, 0, 0, 0,
	1, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0,
	4, 0, 0, 0, 3, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0,
	1, 0, 1, 0, 0, 0, 0, 0, 4, 0, 0, 0, 3, 0, 0, 0,
	27, 1, 0, 0, 0, 0, 0, 0, 3, 0, 1, 0, 0, 0, 0, 0,
	4, 0, 0, 0, 4, 0, 0, 0, 128, 187, 0, 0, 0, 0, 0, 0,
	4, 0, 1, 0, 0, 0, 0, 0, 4, 0, 0, 0, 4, 0, 0, 0,
	2, 0, 0, 0, 0, 0, 0, 0, 5, 0, 1, 0, 0, 0, 0, 0,
	16, 0, 0, 0, 13, 0, 0, 0, 4, 0, 0, 0, 3, 0, 0, 0,
	3, 0, 0, 0, 4, 0, 0, 0,
}

func TestFormatPodMatchesSPA(t *testing.T) {
	if got := formatPod(48000, stereoPositions); !bytes.Equal(got, formatPod48k) {
		t.Fatalf("formatPod(48000, stereoPositions) =\n%v\nwant\n%v", got, formatPod48k)
	}
	// Another rate changes the rate's word alone.
	want := bytes.Clone(formatPod48k)
	binary.LittleEndian.PutUint32(want[104:], 44100)
	if got := formatPod(44100, stereoPositions); !bytes.Equal(got, want) {
		t.Fatalf("formatPod(44100, stereoPositions) =\n%v\nwant\n%v", got, want)
	}
}

var stereoPositions = []uint32{channelFL, channelFR}
