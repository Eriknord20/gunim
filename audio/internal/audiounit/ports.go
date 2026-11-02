//go:build ((darwin && !ios) || (linux && !android)) && (amd64 || arm64)

package audiounit

// Ports returns the names of the default input device's channels and of
// the default output device's, as a client would record and play them.
func Ports() (inputs, outputs []string, err error) {
	a, err := getAPI()
	if err != nil {
		return nil, nil, err
	}
	of := func(selector, scope uint32) []string {
		dev, ok := get32(a, systemObject, selector, scopeGlobal)
		if !ok || dev == 0 {
			return nil
		}
		return channelNames(a, dev, scope, deviceName(a, dev), channels(a, dev, scope))
	}
	return of(propDefaultInputDevice, scopeInput), of(propDefaultOutputDevice, scopeOutput), nil
}
