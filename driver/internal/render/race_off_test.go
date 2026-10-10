//go:build !race

package render

// raceOn says the tests run with the race detector, whose runtime
// allocates of its own: allocations are not counted then.
const raceOn = false
