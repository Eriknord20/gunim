package install

import "os"

// otherCopies counts the copies of the program at exe running besides this
// one: the processes it started itself are its own, as a program's helper
// process run from its own file is, and not another copy. Where the
// system cannot say, it counts none.
func otherCopies(exe string) int {
	if exe == "" {
		return 0
	}
	procs, err := runningParents(exe)
	if err != nil {
		return 0
	}
	return countOthers(procs, os.Getpid())
}

// countOthers counts the processes of procs, each with the process that
// started it, that are neither self nor started by it.
func countOthers(procs map[int]int, self int) int {
	n := 0
	for pid, parent := range procs {
		if pid != self && parent != self {
			n++
		}
	}
	return n
}
