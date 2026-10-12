package install

import (
	"os"
	"testing"
)

func TestParentOfReadsTheProcessThatStartedOne(t *testing.T) {
	if got, want := parentOf(os.Getpid()), os.Getppid(); got != want {
		t.Fatalf("parentOf(this test) = %d, want %d", got, want)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	procs, err := runningParents(self)
	if err != nil {
		t.Fatal(err)
	}
	if procs[os.Getpid()] != os.Getppid() {
		t.Fatalf("runningParents of this test's program = %v, want this process, started by %d", procs, os.Getppid())
	}
	if countOthers(procs, os.Getpid()) != 0 {
		t.Fatalf("this test alone counts others in %v", procs)
	}
}
