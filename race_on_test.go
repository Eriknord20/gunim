//go:build race

package gunim

// raceOn says the tests run with the race detector, which slows the code many times over: a wait for something
// that happens in a few milliseconds is given longer then.
const raceOn = true
