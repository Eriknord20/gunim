package gunim

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"time"

	"github.com/marrasen/gunim/driver"
)

// ErrNoPixels is returned by [Client.Shot] for a window whose driver
// draws nothing to read back, such as one from [NewOffscreen], and by
// [Window.Pixels] where it cannot draw.
var ErrNoPixels = errors.New("gunim: this window draws no pixels to read")

// shotWait is how long a shot waits for a window's next frame. A test may shorten it.
var shotWait = 2 * time.Second

// shotRequest asks the UI goroutine for a picture of the window and its popups, answered on got.
type shotRequest struct {
	got chan<- shotResult
}

type shotResult struct {
	img *image.RGBA
	err error
}

// Shot returns the window's next frame as a picture, with the popups
// open over it, such as a menu or a picker, where they show. A popup
// that reaches past the window's edge makes the picture larger, and
// what lies outside every window is transparent. It is for a tool that
// drives the window through a script and keeps what it shows. It draws
// a frame for the purpose, and waits for it, or for ctx to end.
func (c Client) Shot(ctx context.Context) (*image.RGBA, error) {
	if _, ok := c.w.dw.(driver.Shooter); !ok {
		return nil, ErrNoPixels
	}
	got := make(chan shotResult, 1)
	select {
	case c.w.shots <- shotRequest{got: got}:
	case <-c.w.done:
		return nil, ErrWindowClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case r := <-got:
		return r.img, r.err
	case <-c.w.done:
		return nil, ErrWindowClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// shotPart is one window's frame on its way, and where the window's content sits on the screen.
type shotPart struct {
	frame chan *image.RGBA
	at    image.Point
}

// shoot asks the window and each popup showing for its next frame, and draws frames until they come. A goroutine
// then puts the popups' frames over the window's, and answers req. It runs on the UI goroutine, which owns the
// popups.
func (u *UI) shoot(req shotRequest) {
	parts, err := u.shotParts()
	if err != nil {
		req.got <- shotResult{err: err}
		return
	}
	u.invalid = true
	go func() {
		img, err := compose(parts)
		if err != nil {
			// The frames that never came are owed no more.
			u.shotsOwed.Store(0)
		}
		req.got <- shotResult{img: img, err: err}
	}()
}

// shotParts asks each window for its next frame: the window first, then its popups in the order they opened. A
// window that cannot say where it is on the screen shows alone, as its popups could go anywhere over it.
func (u *UI) shotParts() ([]shotPart, error) {
	ask := func(dw driver.Window) (shotPart, error) {
		p := shotPart{frame: make(chan *image.RGBA, 1)}
		if pos, ok := dw.(driver.Positioner); ok {
			at, err := pos.ContentOrigin()
			if err != nil {
				return shotPart{}, fmt.Errorf("gunim: shot: finding a window on the screen: %w", err)
			}
			p.at = at
		}
		shooter, ok := dw.(driver.Shooter)
		if !ok {
			return shotPart{}, errors.New("gunim: shot: this window cannot be photographed")
		}
		u.shotsOwed.Add(1)
		shooter.Shoot(func(img *image.RGBA) {
			u.shotsOwed.Add(-1)
			p.frame <- img
		})
		return p, nil
	}
	main, err := ask(u.w.dw)
	if err != nil {
		return nil, err
	}
	parts := []shotPart{main}
	if _, ok := u.w.dw.(driver.Positioner); !ok {
		return parts, nil
	}
	for _, s := range u.popups {
		if s.dw == nil || s.closing {
			continue
		}
		if _, ok := s.dw.(driver.Shooter); !ok {
			continue
		}
		if _, ok := s.dw.(driver.Positioner); !ok {
			continue
		}
		p, err := ask(s.dw)
		if err != nil {
			return nil, err
		}
		parts = append(parts, p)
	}
	return parts, nil
}

// compose waits for each part's frame and puts them together, the first at the origin and the rest over it where
// they sit on the screen.
func compose(parts []shotPart) (*image.RGBA, error) {
	frames := make([]*image.RGBA, len(parts))
	deadline := time.After(shotWait)
	for i, p := range parts {
		select {
		case frames[i] = <-p.frame:
		case <-deadline:
			if i == 0 {
				return nil, errors.New("gunim: shot: the window drew no frame")
			}
			return nil, fmt.Errorf("gunim: shot: popup %d of %d drew no frame", i, len(parts)-1)
		}
	}
	if len(parts) == 1 {
		return frames[0], nil
	}
	origin := parts[0].at
	bounds := frames[0].Bounds()
	placed := make([]image.Rectangle, len(parts))
	for i, p := range parts {
		placed[i] = frames[i].Bounds().Add(p.at.Sub(origin))
		bounds = bounds.Union(placed[i])
	}
	out := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	shift := bounds.Min
	for i, f := range frames {
		op := draw.Over
		if i == 0 {
			op = draw.Src
		}
		draw.Draw(out, placed[i].Sub(shift), f, image.Point{}, op)
	}
	return out, nil
}
