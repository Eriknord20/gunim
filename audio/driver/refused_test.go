package driver

import (
	"errors"
	"testing"
	"time"
)

func TestARefusalIsTriedAgain(t *testing.T) {
	refused := &Error{Op: "starting X", refused: true}
	playing := &Device{}
	other := errors.New("no such driver")
	failed := &Error{Op: "counting the channels", Code: 1}
	for _, c := range []struct {
		name   string
		errs   []error // what each try returns; past its end, the device opens
		tries  int
		sleeps int
		want   error
	}{
		{"opens at once", nil, 1, 0, nil},
		{"opens on the second try", []error{refused}, 2, 1, nil},
		{"opens on the last try", []error{refused, refused}, 3, 2, nil},
		{"stops at the last try", []error{refused, refused, refused, refused}, 3, 2, refused},
		{"another error is not tried again", []error{other}, 1, 0, other},
		{"a failure after it started is not tried again", []error{failed}, 1, 0, failed},
		{"another error after a refusal stops it", []error{refused, other}, 2, 1, other},
	} {
		t.Run(c.name, func(t *testing.T) {
			tries, sleeps := 0, 0
			d, err := retryRefused(func() (*Device, error) {
				tries++
				if tries <= len(c.errs) {
					return nil, c.errs[tries-1]
				}
				return playing, nil
			}, func(d time.Duration) {
				if d != refusedGap {
					t.Errorf("slept %v, want %v", d, refusedGap)
				}
				sleeps++
			})
			if !errors.Is(err, c.want) {
				t.Errorf("err = %v, want %v", err, c.want)
			}
			if c.want == nil && d != playing {
				t.Errorf("device = %v, want the one that opened", d)
			}
			if tries != c.tries || sleeps != c.sleeps {
				t.Errorf("%d tries and %d sleeps, want %d and %d", tries, sleeps, c.tries, c.sleeps)
			}
		})
	}
}
