package eq

import (
	"encoding/json"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug"
)

// patches are the EQ's own patches, each a starting point to shape:
// the equalizer's presets of the music players made with gunim.
var patches = []plug.Patch{
	patchOf("Flat"),
	patchOf("Warm",
		audio.Band{Kind: audio.LowShelf, Freq: 120, Gain: 3, Q: 0.707},
		audio.Band{Kind: audio.Bell, Freq: 350, Gain: 1.5, Q: 1},
		audio.Band{Kind: audio.HighShelf, Freq: 8000, Gain: -2.5, Q: 0.707}),
	patchOf("Bright",
		audio.Band{Kind: audio.LowCut, Freq: 30, Q: 0.707, Slope: 24},
		audio.Band{Kind: audio.Bell, Freq: 300, Gain: -2, Q: 1.2},
		audio.Band{Kind: audio.HighShelf, Freq: 6000, Gain: 4, Q: 0.707}),
	patchOf("Vocal presence",
		audio.Band{Kind: audio.LowCut, Freq: 90, Q: 0.707, Slope: 24},
		audio.Band{Kind: audio.Bell, Freq: 250, Gain: -2.5, Q: 1.4},
		audio.Band{Kind: audio.Bell, Freq: 3000, Gain: 3.5, Q: 1.1},
		audio.Band{Kind: audio.HighShelf, Freq: 12000, Gain: 1.5, Q: 0.707}),
	patchOf("Bass boost",
		audio.Band{Kind: audio.LowShelf, Freq: 90, Gain: 6, Q: 0.8},
		audio.Band{Kind: audio.Bell, Freq: 400, Gain: -1.5, Q: 1}),
	patchOf("Loudness",
		audio.Band{Kind: audio.LowShelf, Freq: 80, Gain: 5, Q: 0.707},
		audio.Band{Kind: audio.Bell, Freq: 1000, Gain: -1.5, Q: 0.8},
		audio.Band{Kind: audio.HighShelf, Freq: 10000, Gain: 4, Q: 0.707}),
	patchOf("Low cut",
		audio.Band{Kind: audio.LowCut, Freq: 80, Q: 0.707, Slope: 24}),
}

// patchOf returns the patch name of bands, each switched on and
// numbered in turn.
func patchOf(name string, bands ...audio.Band) plug.Patch {
	for i := range bands {
		bands[i].ID, bands[i].On = i+1, true
	}
	b, err := json.Marshal(stateOf(bands))
	if err != nil {
		// Bands of numbers and names always marshal.
		panic("eq: " + err.Error())
	}
	return plug.Patch{Name: name, State: b}
}
