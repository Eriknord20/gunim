//go:build !race

package audio

// raceOn says the tests run with the race detector, whose runtime
// allocates of its own: allocations are not counted then.
const raceOn = false
