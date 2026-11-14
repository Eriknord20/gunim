//go:build !race

package widget

// raceOn says the tests run with the race detector, which slows the code
// many times over: a limit on how long something takes is not checked then.
const raceOn = false
