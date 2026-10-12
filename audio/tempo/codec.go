package tempo

import (
	"encoding/json"
	"math"
	"time"
)

// mapJSON is a map as it is written: the start in milliseconds, which
// reads as a number of seconds should, and the changes as they are.
type mapJSON struct {
	StartMs float64 `json:"startMs,omitempty"`
	Tempos  []Tempo `json:"tempos"`
	Meters  []Meter `json:"meters"`
}

// MarshalJSON writes the map, so an application keeps a recording's
// musical time in its own file without knowing how a map works.
//
// It takes the map by value, so a Map held in a struct, rather than a
// pointer to one, is written as a map too.
func (m Map) MarshalJSON() ([]byte, error) {
	r := m.ready()
	return json.Marshal(mapJSON{
		StartMs: float64(r.start) / float64(time.Millisecond),
		Tempos:  r.tempos,
		Meters:  r.meters,
	})
}

// UnmarshalJSON reads a map back, tidying what it finds: a file written
// by hand, or by an older version, comes back usable rather than
// broken.
func (m *Map) UnmarshalJSON(data []byte) error {
	var j mapJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	// Rounded, since a start written as 1234.567 is not exactly that in
	// binary, and cutting it off would lose a nanosecond each time.
	m.start = time.Duration(math.Round(j.StartMs * float64(time.Millisecond)))
	m.tempos, m.meters = j.Tempos, j.Meters
	m.tidy()
	return nil
}
