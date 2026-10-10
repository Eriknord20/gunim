package audio

import (
	"errors"
	"io"
)

// decoder is what a file format gives: frames of stereo float32 at its
// own rate.
type decoder interface {
	// read fills dst with up to len(dst)/2 frames, as Source.Read.
	read(dst []float32) (int, error)
	// seek moves to frame, at the decoder's rate.
	seek(frame int64) error
	// len returns the length at the decoder's rate, or -1.
	len() int64
	rate() int
	// info says what format the sound is in.
	info() Format
}

// resampler plays a decoder at another rate, drawing a cubic curve
// through the frames either side of each it makes.
type resampler struct {
	d decoder
	// step is how far through the decoder's frames each frame made
	// moves.
	step float64
	// win holds four frames of the decoder, from pos-1 to pos+2, and
	// frac is how far between pos and pos+1 the next frame is.
	win  [8]float32
	frac float64
	// pos is the decoder's frame at win's second frame.
	pos  int64
	in   []float32
	inN  int
	inAt int
	// ended says the decoder has no more to read, and read counts the
	// decoder's frames taken, so end is its frame count once known.
	ended    bool
	read     int64
	end      int64
	endKnown bool
	// primed counts the frames win holds from the decoder, to three.
	primed int
}

func newResampler(d decoder, to int) *resampler {
	return &resampler{d: d, step: float64(d.rate()) / float64(to), in: make([]float32, 2*1024)}
}

// next returns the decoder's next frame, or false at its end; wait
// says the decoder has none yet, as a [Stream] not yet decoded that
// far, and to try again later.
func (r *resampler) next() (l, rt float32, ok, wait bool, err error) {
	for r.inAt >= r.inN {
		if r.ended {
			return 0, 0, false, false, nil
		}
		n, err := r.d.read(r.in)
		r.inN, r.inAt = n, 0
		if errors.Is(err, io.EOF) {
			r.ended = true
		} else if err != nil {
			return 0, 0, false, false, err
		}
		if n == 0 && !r.ended {
			return 0, 0, false, true, nil
		}
	}
	l, rt = r.in[2*r.inAt], r.in[2*r.inAt+1]
	r.inAt++
	return l, rt, true, false, nil
}

// push moves the window on by one frame of the decoder; past its end
// the window fills with silence. It returns false, the window as it
// was, where the decoder has no frame yet.
func (r *resampler) push() (bool, error) {
	l, rt, ok, wait, err := r.next()
	if wait || err != nil {
		return false, err
	}
	if ok {
		r.read++
	} else if !r.endKnown {
		r.end, r.endKnown = r.read, true
	}
	copy(r.win[:6], r.win[2:])
	r.win[6], r.win[7] = l, rt
	return true, nil
}

// Read implements [Source]: it stops short, with no error, where the
// decoder has no frames yet.
func (r *resampler) Read(dst []float32) (int, error) {
	// The frame before the first is silence; win holds -1 to 2.
	for r.primed < 3 {
		if moved, err := r.push(); !moved {
			return 0, err
		}
		r.primed++
	}
	frames := len(dst) / 2
	for i := range frames {
		// The window moves on to the frame made next, before it is
		// made, so a decoder with none yet leaves nothing half done.
		for r.frac >= 1 {
			if moved, err := r.push(); !moved {
				return i, err
			}
			r.frac--
			r.pos++
		}
		if r.past() {
			return i, io.EOF
		}
		t := float32(r.frac)
		for c := range 2 {
			dst[2*i+c] = hermite(r.win[c], r.win[2+c], r.win[4+c], r.win[6+c], t)
		}
		r.frac += r.step
	}
	return frames, nil
}

// past reports whether the next frame lies past the decoder's last.
func (r *resampler) past() bool {
	return r.endKnown && r.pos >= r.end
}

// SeekFrame implements [Seeker].
func (r *resampler) SeekFrame(f int64) error {
	src := int64(float64(f) * r.step)
	if err := r.d.seek(src); err != nil {
		return err
	}
	r.pos, r.frac = src, float64(f)*r.step-float64(src)
	r.inN, r.inAt, r.ended, r.primed = 0, 0, false, 0
	r.read, r.endKnown = src, false
	r.win = [8]float32{}
	return nil
}

// Len implements [Seeker].
func (r *resampler) Len() int64 {
	l := r.d.len()
	if l < 0 {
		return -1
	}
	return int64(float64(l) / r.step)
}

// hermite returns the point t of the way from b to c on the
// Catmull-Rom curve through a, b, c and d.
func hermite(a, b, c, d, t float32) float32 {
	c0 := b
	c1 := (c - a) / 2
	c2 := a - 2.5*b + 2*c - d/2
	c3 := (d-a)/2 + 1.5*(b-c)
	return ((c3*t+c2)*t+c1)*t + c0
}

// direct plays a decoder at its own rate.
type direct struct{ d decoder }

// Read implements [Source].
func (s direct) Read(dst []float32) (int, error) { return s.d.read(dst) }

// SeekFrame implements [Seeker].
func (s direct) SeekFrame(f int64) error { return s.d.seek(f) }

// Len implements [Seeker].
func (s direct) Len() int64 { return s.d.len() }
