package asio

// Info says how an open driver plays.
type Info struct {
	// Driver is the driver's name.
	Driver string
	// Rate is the rate it plays at, in frames a second.
	Rate int
	// Buffer is how many frames it takes at a time; MinBuffer,
	// MaxBuffer and PreferredBuffer are the sizes it allows and the one
	// its control panel is set to.
	Buffer, MinBuffer, MaxBuffer, PreferredBuffer int
	// Latency is how many frames pass from a buffer filled to its sound
	// heard, as the driver reports it.
	Latency int
	// Bits is how many bits a sample the driver takes, and Float says
	// the samples are floating point.
	Bits  int
	Float bool
	// Outputs is how many outputs the driver has; the sound plays on the
	// first two, or on one where it has one.
	Outputs int
	// Inputs is how many inputs the driver has. Where [Config.Record]
	// is set, Recorded is how many of them it records, and
	// InputNames what the driver calls those. InputLatency is how many
	// frames pass from a sound at the inputs to Record's call with it,
	// as the driver reports it.
	Inputs, Recorded int
	InputNames       []string
	InputLatency     int
}

// fitBuffer returns the buffer size nearest want that a driver allows:
// from lo to hi, in steps of gran; where gran is -1, powers of two;
// where it is 0, pref alone. A want of zero takes pref.
func fitBuffer(want, lo, hi, pref, gran int) int {
	if want <= 0 || gran == 0 || lo >= hi {
		return pref
	}
	want = max(lo, min(want, hi))
	if gran < 0 {
		best := 0
		for n := 1; n <= hi; n *= 2 {
			if n < lo {
				continue
			}
			if best == 0 || abs(n-want) < abs(best-want) {
				best = n
			}
		}
		if best == 0 {
			return pref
		}
		return best
	}
	k := (want - lo + gran/2) / gran
	return min(lo+k*gran, hi)
}

func abs(n int) int { return max(n, -n) }
