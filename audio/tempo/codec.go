package tempo

import (
	"encoding/json"
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
func (m *Map) MarshalJSON() ([]byte, error) {
	m.prep()
	return json.Marshal(mapJSON{
		StartMs: float64(m.start) / float64(time.Millisecond),
		Tempos:  m.tempos,
		Meters:  m.meters,
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
	m.start = time.Duration(j.StartMs * float64(time.Millisecond))
	m.tempos, m.meters = j.Tempos, j.Meters
	m.tidy()
	return nil
}
