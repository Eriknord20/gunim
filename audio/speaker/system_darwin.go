//go:build darwin && !ios

package speaker

import (
	"github.com/marrasen/gunim/audio/coreaudio"
	"github.com/marrasen/gunim/audio/driver"
)

// openSystem opens the system's own sound as a device that asks for its
// sound, as an ASIO driver does: Core Audio's default output device, its
// thread asking for each buffer, so Options.Buffer and Direct set how
// soon the sound is heard. Where it fails to open, oto plays.
var openSystem = func(c driver.Config) (pullDevice, error) {
	d, err := coreaudio.Open(coreaudio.Config{Rate: c.Rate, Buffer: c.Buffer, Fill: c.Fill, Reset: c.Reset, Log: c.Log})
	if err != nil {
		return nil, err
	}
	return systemDevice{d}, nil
}

// systemDevice is Core Audio's device, saying of itself as a driver
// does.
type systemDevice struct{ *coreaudio.Device }

// Info returns what the device says of itself, as a driver says it.
func (d systemDevice) Info() driver.Info {
	i := d.Device.Info()
	played := []int{0, 1}
	if i.Outputs == 1 {
		played = []int{0}
	}
	return driver.Info{Driver: i.Device, Rate: i.Rate, Buffer: i.Buffer, MinBuffer: i.MinBuffer, MaxBuffer: i.MaxBuffer,
		PreferredBuffer: i.Buffer, Latency: i.Latency, Bits: 32, Float: true, Outputs: i.Outputs, Played: played}
}
