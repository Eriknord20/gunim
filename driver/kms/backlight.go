//go:build linux && (arm64 || amd64)

package kms

import (
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// backlight is a panel's backlight under /sys/class/backlight. Its
// brightness is a whole number of steps, up to max: 31 on the Touch
// Display 2.
type backlight struct {
	dir string
	max int
	// was is the brightness the driver found, which it puts back.
	was int
	cur int
}

// sysBacklight is where the kernel lists backlights; a test points it
// elsewhere.
var sysBacklight = "/sys/class/backlight"

// openBacklight finds the backlight named, or the first one, and reads
// it. It returns nil with no error where there is none.
func openBacklight(name string) (*backlight, error) {
	dirs, _ := filepath.Glob(filepath.Join(sysBacklight, "*"))
	slices.Sort(dirs)
	for _, d := range dirs {
		if name != "" && filepath.Base(d) != name {
			continue
		}
		maxV, err := readInt(filepath.Join(d, "max_brightness"))
		if err != nil {
			return nil, fmt.Errorf("backlight: %w", err)
		}
		cur, err := readInt(filepath.Join(d, "brightness"))
		if err != nil {
			return nil, fmt.Errorf("backlight: %w", err)
		}
		b := &backlight{dir: d, max: maxV, was: cur, cur: cur}
		if cur == 0 && maxV > 0 {
			// A dark panel at start is no level to come back to: a run
			// before may have blanked it and ended. Half way is.
			b.was = (maxV + 1) / 2
			log.Printf("gunim: kms: the backlight was at 0; it goes back to %d of %d on exit", b.was, maxV)
		}
		return b, nil
	}
	if name != "" {
		return nil, fmt.Errorf("backlight: no %s under %s", name, sysBacklight)
	}
	return nil, nil
}

func readInt(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

// level returns the step for a brightness of v, from 0 to 1: the nearest
// step at or above it, so a dim screen never goes dark.
func (b *backlight) level(v float32) int {
	v = min(max(v, 0), 1)
	return int(math.Ceil(float64(v)*float64(b.max) - 1e-6))
}

// set writes the step for a brightness of v.
func (b *backlight) set(v float32) error {
	return b.setLevel(b.level(v))
}

func (b *backlight) setLevel(n int) error {
	if err := os.WriteFile(filepath.Join(b.dir, "brightness"), []byte(strconv.Itoa(n)), 0); err != nil {
		return fmt.Errorf("backlight: %w", err)
	}
	b.cur = n
	return nil
}

// brightness returns the brightness now, from 0 to 1.
func (b *backlight) brightness() float32 {
	if b.max == 0 {
		return 1
	}
	return float32(b.cur) / float32(b.max)
}

// restore puts back the brightness the driver found.
func (b *backlight) restore() error {
	if b == nil || b.cur == b.was {
		return nil
	}
	return errors.Join(b.setLevel(b.was))
}
