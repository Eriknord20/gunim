package anim

import "testing"

func TestMixEndpointsAndOvershoot(t *testing.T) {
	calls := 0
	c := Codec[float32]{N: 1, Encode: func(v float32) (s [MaxScalars]float32) { calls++; s[0] = v; return s }, Decode: func(s [MaxScalars]float32) float32 { return s[0] }}
	for _, v := range []struct{ t, want float32 }{{0, 10}, {1, 20}, {-.5, 5}, {1.5, 25}, {.5, 15}} {
		before := calls
		if got := Mix(c, 10, 20, v.t); got != v.want {
			t.Fatalf("Mix(t=%g)=%g want %g", v.t, got, v.want)
		}
		if (v.t == 0 || v.t == 1) && calls != before {
			t.Fatal("endpoint encoded unnecessarily")
		}
	}
}
