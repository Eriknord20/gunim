//go:build android

package speaker

import "github.com/marrasen/gunim/audio/internal/aaudio"

func init() {
	if aaudio.Supported() != nil {
		return
	}
	openAAStream = func(c aaConfig) (aaStream, error) {
		st, err := aaudio.Open(aaudio.Config{Input: c.input, Rate: c.rate, Channels: c.chans, Exclusive: c.exclusive,
			Data: c.data, Blocks: c.blocks})
		if err != nil {
			return nil, err
		}
		return st, nil
	}
}
