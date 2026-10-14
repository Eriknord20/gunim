package widget

import (
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
)

// showToast shows to in a fresh stack, runs n frames, and returns a runner and the window's masks.
func showToast(t *testing.T, to Toast, n int) (run func(int), masks func() []*paint.MaskOp) {
	t.Helper()
	h := &toastHost{t: &Toasts{Life: time.Minute}}
	w, run := stage(t, h)
	type do struct{ fn func(u *gunim.UI) }
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, d do, u *gunim.UI) { d.fn(u) })
	if err := w.Client().Patch("stage", do{func(u *gunim.UI) { h.t.Show(to, u) }}); err != nil {
		t.Fatal(err)
	}
	run(n)
	lastOps = w.Offscreen().Ops()
	return run, func() []*paint.MaskOp { return maskOps(w.Offscreen()) }
}

func TestAToastsKindShowsItsIconInItsColour(t *testing.T) {
	for _, c := range []struct {
		kind ToastKind
		ic   *icon.Icon
		ink  color.NRGBA
	}{
		{ToastInfo, icon.Info, ToastInfoInk.Default()},
		{ToastSuccess, icon.CircleCheck, ToastSuccessInk.Default()},
		{ToastWarning, icon.TriangleAlert, ToastWarningInk.Default()},
		{ToastError, icon.CircleAlert, ToastErrorInk.Default()},
	} {
		_, masks := showToast(t, Toast{Title: "Saved", Kind: c.kind}, 60)
		ms, closes := withoutClose(t, masks())
		if want := c.kind == ToastError; closes != want {
			t.Fatalf("kind %d drew a close button %v, want %v", c.kind, closes, want)
		}
		if len(ms) != 1 || strokeOf(t, ms[0]).Icon != c.ic {
			t.Fatalf("kind %d drew %d icons, want %s", c.kind, len(ms), c.ic.Name)
		}
		if ms[0].Color != c.ink {
			t.Errorf("kind %d's icon is %v, want %v", c.kind, ms[0].Color, c.ink)
		}
		pad := CardPadding.Default()
		if x := ms[0].Transform.C + ms[0].Rect.Min.X; x != pad.Left {
			t.Errorf("kind %d's icon is at %v, want at the card's padding", c.kind, x)
		}
		if x := firstTextX(t, lastOps); x < pad.Left+IconSize.Default()+IconGap.Default() {
			t.Errorf("kind %d's title starts at %v, over its icon", c.kind, x)
		}
	}
}

func TestAPlainToastShowsNoIconUnlessGivenOne(t *testing.T) {
	_, masks := showToast(t, Toast{Title: "Copied"}, 60)
	if ms := masks(); len(ms) != 0 {
		t.Fatalf("a plain toast drew %d icons, want none", len(ms))
	}
	_, masks = showToast(t, Toast{Title: "Copied", Icon: icon.Copy}, 60)
	if ms := masks(); len(ms) != 1 || strokeOf(t, ms[0]).Icon != icon.Copy || ms[0].Color != Ink.Default() {
		t.Fatalf("a plain toast with an icon drew %d, want the icon in the ink", len(ms))
	}
	_, masks = showToast(t, Toast{Title: "Gone", Kind: ToastError, Icon: icon.Trash2}, 60)
	if ms, _ := withoutClose(t, masks()); len(ms) != 1 || strokeOf(t, ms[0]).Icon != icon.Trash2 || ms[0].Color != ToastErrorInk.Default() {
		t.Fatalf("an error toast with an icon drew %d, want the icon in the error colour", len(ms))
	}
}

func TestAToastsIconDrawsOnAsItArrives(t *testing.T) {
	run, masks := showToast(t, Toast{Title: "Saved", Kind: ToastSuccess}, 3)
	ms := masks()
	if len(ms) != 1 {
		t.Fatalf("arriving, the toast drew %d icons", len(ms))
	}
	first := strokeOf(t, ms[0]).Progress
	if first <= 0 || first >= 0.5 {
		t.Fatalf("three frames in, the icon is drawn %v of the way", first)
	}
	run(60)
	if s := strokeOf(t, masks()[0]); !s.Settled() {
		t.Fatalf("a second in, the icon is drawn %v of the way, want whole", s.Progress)
	}
}

// withoutClose returns ms without the cross of a close button, and
// whether there was one.
func withoutClose(t *testing.T, ms []*paint.MaskOp) ([]*paint.MaskOp, bool) {
	t.Helper()
	var out []*paint.MaskOp
	closes := false
	for _, m := range ms {
		if strokeOf(t, m).Icon == icon.X {
			closes = true
			continue
		}
		out = append(out, m)
	}
	return out, closes
}

// lastOps holds the ops of the last window showToast ran.
var lastOps []paint.Op

// firstTextX returns where the first text in ops starts.
func firstTextX(t *testing.T, ops []paint.Op) float32 {
	t.Helper()
	for _, op := range ops {
		if o, ok := op.(*paint.TextOp); ok {
			return o.Transform.C
		}
	}
	t.Fatal("nothing drew text")
	return 0
}

// Beside a title that wraps, the icon sits level with the first line.
func TestAToastsIconSitsBesideTheTitlesFirstLine(t *testing.T) {
	long := "The copy of the folder could not finish, for the disk it was going to has no room left on it at all"
	_, masks := showToast(t, Toast{Title: long, Kind: ToastError}, 60)
	ms, _ := withoutClose(t, masks())
	short := ms[0]
	_, masks = showToast(t, Toast{Title: "Saved", Kind: ToastError}, 60)
	ms, _ = withoutClose(t, masks())
	one := ms[0]
	top := func(m *paint.MaskOp) float32 { return m.Transform.F + m.Rect.Min.Y }
	if a, b := top(short), top(one); a != b {
		t.Fatalf("beside a wrapped title the icon's top is at %v, beside one line at %v, want the same", a, b)
	}
}
