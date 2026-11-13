//go:build linux && (arm64 || amd64)

// Package kmsbench measures a program's own screen on driver/kms against the goals of
// docs/plans/kms-touch.md, as tools/kmsbench measures the demo and the widgets gallery: frames and
// missed flips while the screen moves with a touch script playing, frame times, touch to flip,
// the time to the first frame, memory, CPU use while moving and at rest, and the screen blanked
// and woken by a tap that reaches nothing.
//
// A program hands Bench a Scene: a function that opens its window as the bench starts, and what
// makes it still for the rest. The touch script plays through the driver's own input path, so it
// needs no uinput and no finger. DragScript and TouchScript make two such scripts. tools/kmsbench
// runs it on gunim's own demo and widgets gallery, and a program that brings its own scene links
// neither.
//
//	r, err := kmsbench.Bench(ctx, kmsbench.Options{
//		KMS:     kms.Options{Rotate: 90, VT: "free"},
//		Scene:   kmsbench.Scene{Name: "status", Open: openStatus},
//		Started: started, // when the program started
//		Warm:    3 * time.Second, Moving: time.Minute, Rest: time.Minute,
//	})
//	missed, err := kmsbench.Report(os.Stdout, r)
package kmsbench

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"
)

// Report writes r as tools/kmsbench prints it: a table of the goals, a line of figures, and what
// the bench measures elsewhere. It returns how many goals were missed.
func Report(w io.Writer, r Result) (missed int, err error) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Goal\tTarget\tMeasured\t\n")
	for _, g := range r.Goals() {
		mark := "ok"
		if !g.OK {
			mark = "MISSED"
			missed++
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", g.Name, g.Target, g.Got, mark)
	}
	if err := tw.Flush(); err != nil {
		return missed, err
	}
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	_, _ = fmt.Fprintf(w, "\n%.2f Hz; %d frames in %.1f s while moving; draw p50 %.1f ms p99 %.1f ms; frame max %.1f ms; %d touch reports answered",
		r.Rate, r.Moving.Frames, r.MovingFor.Seconds(), ms(r.Moving.Draw.P50), ms(r.Moving.Draw.P99), ms(r.Moving.Frame.Max), r.Moving.Touch.N)
	if r.Software {
		_, _ = fmt.Fprint(w, "; drawn by the CPU")
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Measured elsewhere:")
	for _, s := range NotHere {
		_, _ = fmt.Fprintln(w, "  "+s)
	}
	if missed > 0 {
		_, err = fmt.Fprintf(w, "%d goals missed\n", missed)
	}
	return missed, err
}
