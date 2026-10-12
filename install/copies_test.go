package install

import "testing"

func TestCountOthersLeavesOutThisCopyAndWhatItStarted(t *testing.T) {
	self := 100
	procs := map[int]int{
		100: 1,   // this copy
		101: 100, // its helper process, started from its own file
		200: 1,   // another copy, started by the user
		201: 200, // that copy's helper
	}
	if got := countOthers(procs, self); got != 2 {
		t.Fatalf("countOthers = %d, want 2: the other copy and its helper", got)
	}
	if got := countOthers(map[int]int{100: 1, 101: 100}, self); got != 0 {
		t.Fatalf("with only this copy and its helper, countOthers = %d", got)
	}
}
