package audio

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/marrasen/gunim/anim"
)

// endless is a sound that plays at a level for ever, and can seek.
type endless struct{ at int64 }

func (e *endless) Len() int64 { return 1 << 40 }

func (e *endless) SeekFrame(f int64) error {
	e.at = f
	return nil
}

func (e *endless) Read(dst []float32) (int, error) {
	for i := range dst {
		dst[i] = 0.1
	}
	e.at += int64(len(dst) / 2)
	return len(dst) / 2, nil
}

func TestMixingMakesNothingAsTheSoundsChange(t *testing.T) {
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	m := NewMixer()
	an := NewAnalyzer(m, 16)
	an.Heard(nil, 0)
	an.HeardBefore(nil, 0)
	eq := NewEQ()
	eq.Set([]Band{{ID: 1, Kind: Bell, Freq: 1000, Gain: 3, Q: 1, On: true}})
	voices := []*Voice{
		m.Play(&endless{}, Options{Insert: eq.Insert()}),
		m.Play(&endless{}, Options{Loop: true}),
	}
	buf := make([]float32, 2*1024)
	m.Mix(buf)
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	var made uint64
	for i := range 300 {
		// What the application does between blocks, which makes what
		// it makes on its own side.
		v := voices[i%2]
		switch i % 10 {
		case 0:
			v.SetVolume(float32(i%5)/5, anim.Tween{Duration: 50 * time.Millisecond})
		case 1:
			v.SetPan(float32(i%3)-1, nil)
		case 2:
			v.Pause()
		case 3:
			v.Resume()
		case 4:
			_ = v.Seek(time.Duration(i) * time.Millisecond)
		case 5:
			v.Then(&endless{})
		case 6:
			m.Play(&endless{}, Options{FadeIn: 10 * time.Millisecond}).Stop(20 * time.Millisecond)
		case 7:
			eq.Set([]Band{{ID: 1 + i%3, Kind: FilterKind(i % 6), Freq: 500, Gain: 2, Q: 1, On: true}})
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		m.Mix(buf)
		runtime.ReadMemStats(&after)
		made += after.Mallocs - before.Mallocs
	}
	if made != 0 {
		t.Fatalf("mixing made %d things over 300 blocks", made)
	}
}

func TestMixingRunsAsEveryMethodIsCalledMeanwhile(t *testing.T) {
	m := NewMixer()
	m.SetLatency(func() int64 { return 256 })
	an := NewAnalyzer(m, 16)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]float32, 2*512)
		for {
			select {
			case <-stop:
				return
			default:
				m.Mix(buf)
			}
		}
	}()
	eq := NewEQ()
	for g := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var heard []float32
			at := int64(0)
			for i := range 300 {
				v := m.Play(&endless{}, Options{Insert: eq.Insert(), Paused: i%4 == 0})
				v.SetVolume(0.5, anim.Tween{Duration: time.Millisecond})
				v.SetPan(float32(g)-1, nil)
				v.Resume()
				_ = v.Seek(time.Duration(i) * time.Millisecond)
				_ = v.Position()
				_ = v.Len()
				_ = v.Paused()
				_ = v.Volume()
				_ = v.Err()
				v.Then(&endless{})
				heard, at = an.Heard(heard[:0], at)
				an.Wave(make([]float32, 64))
				_ = m.Mixed()
				_ = m.Playing()
				v.Stop(0)
			}
		}()
	}
	time.Sleep(10 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// meddler is a sound that, as the mixer reads it, calls the methods
// of its own voice and mixer: a mixer that held a lock they take as it
// mixed would wait on itself for ever.
type meddler struct {
	endless
	m *Mixer
	v *Voice
}

func (d *meddler) Read(dst []float32) (int, error) {
	if d.v != nil {
		d.v.SetVolume(0.9, nil)
		_ = d.v.Position()
		_ = d.v.Paused()
		_ = d.v.Len()
		_ = d.m.Mixed()
		_ = d.m.Playing()
		d.m.SetRate(d.m.Rate())
	}
	return d.endless.Read(dst)
}

func TestMixingHoldsNoLockTheApplicationTakes(t *testing.T) {
	m := NewMixer()
	d := &meddler{m: m}
	d.v = m.Play(d, Options{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Mix(make([]float32, 2*1024))
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("mixing waited on a lock the voice's own methods held")
	}
}
