//go:build race

package main

// slowdown is how many times slower the tests run: the race detector
// makes code run some times slower.
const slowdown = 4
