package speaker

import "github.com/marrasen/gunim/audio/internal/wasapi"

func init() {
	openWasDevice = func(c wasapi.Config) (wasDevice, error) {
		d, err := wasapi.Open(c)
		if err != nil {
			return nil, err
		}
		return d, nil
	}
}
