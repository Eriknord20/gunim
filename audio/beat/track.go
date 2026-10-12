package beat

import (
	"math"
	"slices"
)

// Tightness is how firmly the beats are held a beat apart. The beat
// chosen at each step gives up this much for the square of the log of
// how far it strays from one beat since the last, so a few per cent of
// drift costs almost nothing and half a beat costs more than any onset
// is worth.
const Tightness = 100.0

// Centre is the tempo the search leans towards, and Spread how many
// octaves away that leaning reaches half its strength. Without it a
// recording at 140 is as well explained by beats at 70, and both by
// beats at 280.
//
// The lean sits a little above the 120 a listener would guess at,
// because the two mistakes are not equally likely. Half a recording's
// tempo always explains its onsets, since every other beat is still a
// beat; twice its tempo only explains them where something falls
// between the beats. Leaning high is what keeps a track at 175 from
// being read as one at 87.
const (
	Centre = 130.0
	Spread = 0.9
)

// level is the onset strength at each column: how far the rise there
// stands above the rises around it, so a loud passage does not read as
// one long onset, measured against how much the rises vary, so the
// numbers mean the same whatever the recording's loudness.
func level(rise []float32, win int) []float32 {
	out := make([]float32, len(rise))
	if len(rise) == 0 {
		return out
	}
	win = max(win, 1)
	sum := make([]float64, len(rise)+1)
	for i, v := range rise {
		sum[i+1] = sum[i] + float64(v)
	}
	for i := range rise {
		lo, hi := max(0, i-win), min(len(rise), i+win+1)
		mean := (sum[hi] - sum[lo]) / float64(hi-lo)
		out[i] = float32(max(0, float64(rise[i])-mean))
	}
	var square float64
	for _, v := range out {
		square += float64(v) * float64(v)
	}
	if rms := math.Sqrt(square / float64(len(out))); rms > 0 {
		for i := range out {
			out[i] = float32(float64(out[i]) / rms)
		}
	}
	return out
}

// period is how many columns a beat lasts, found by how well the onsets
// line up with themselves that far apart, leaning towards [Centre].
//
// It also reports how far that lining up stands above the lining up at
// every other spacing, from 0 for sound that repeats no more at one
// spacing than another to near 1 for a plain steady beat. It reports
// false for sound too short or too still to tell.
func period(f []float32, fps, minBPM, maxBPM float64) (per, clarity float64, ok bool) {
	lo := max(int(math.Floor(60*fps/maxBPM)), 2)
	hi := int(math.Ceil(60 * fps / minBPM))
	if hi <= lo || len(f) < 4*hi {
		return 0, 0, false
	}
	// The onsets are softened first, each spread over the columns
	// either side. A beat's length is rarely a whole number of columns
	// — at 150 a beat is 37.5 of them — and sharp onsets half a column
	// out line up no better than ones a whole beat out, which is how a
	// tempo comes back halved.
	soft := spread(f)
	score := make([]float64, hi+2)
	// alike is the lining up itself, without the lean, which is what
	// says whether there is a beat there at all.
	alike := make([]float64, hi+2)
	best, at := 0.0, 0
	for lag := lo; lag <= hi; lag++ {
		var sum float64
		for i := 0; i+lag < len(soft); i++ {
			sum += float64(soft[i]) * float64(soft[i+lag])
		}
		alike[lag] = sum / float64(len(soft)-lag)
		lean := math.Exp(-0.5 * math.Pow(math.Log2(60*fps/float64(lag)/Centre)/Spread, 2))
		score[lag] = alike[lag] * lean
		if score[lag] > best {
			best, at = score[lag], lag
		}
	}
	if at == 0 {
		return 0, 0, false
	}
	var mean float64
	for lag := lo; lag <= hi; lag++ {
		mean += alike[lag]
	}
	mean /= float64(hi - lo + 1)
	if alike[at] > 0 && mean < alike[at] {
		clarity = 1 - mean/alike[at]
	}
	// Sharpen the peak between the lags either side of it, so a beat's
	// length is not rounded to a whole column.
	per = float64(at)
	if at > lo && at < hi {
		a, b, c := score[at-1], score[at], score[at+1]
		if bend := a - 2*b + c; bend < 0 {
			per += min(max(0.5*(a-c)/bend, -0.5), 0.5)
		}
	}
	return per, clarity, true
}

// spread softens the onsets, each column taking in a little of the
// columns either side.
func spread(f []float32) []float32 {
	out := make([]float32, len(f))
	for i := range f {
		sum, weight := 0.0, 0.0
		for k := -2; k <= 2; k++ {
			if i+k < 0 || i+k >= len(f) {
				continue
			}
			w := []float64{0.2, 0.6, 1, 0.6, 0.2}[k+2]
			sum += w * float64(f[i+k])
			weight += w
		}
		out[i] = float32(sum / weight)
	}
	return out
}

// walk picks the columns the beats fall on: the run of them catching
// the most onset strength while staying near a beat apart, chosen over
// the whole recording at once rather than one at a time.
//
// This is the dynamic programme Daniel P. W. Ellis set out in "Beat
// Tracking by Dynamic Programming" (2007).
func walk(f []float32, per float64) []int {
	n := len(f)
	if n == 0 || per < 2 {
		return nil
	}
	lo, hi := max(int(math.Round(per/2)), 1), int(math.Round(2*per))
	score := make([]float64, n)
	from := make([]int, n)
	for i := range n {
		best, pick := math.Inf(-1), -1
		for j := max(0, i-hi); j <= i-lo; j++ {
			stray := math.Log(float64(i-j) / per)
			if s := score[j] - Tightness*stray*stray; s > best {
				best, pick = s, j
			}
		}
		score[i], from[i] = float64(f[i]), -1
		if pick >= 0 {
			score[i], from[i] = float64(f[i])+best, pick
		}
	}
	// End on the best of the last beat's worth of columns, so a quiet
	// ending does not pull the last beat out of place.
	end, most := -1, math.Inf(-1)
	for i := max(0, n-hi); i < n; i++ {
		if score[i] > most {
			end, most = i, score[i]
		}
	}
	var out []int
	for i := end; i >= 0; i = from[i] {
		out = append(out, i)
	}
	slices.Reverse(out)
	return out
}

// downbeat is the beat that starts a bar: of the beats a bar apart, the
// run carrying the most bass onset, since it is the bass drum that
// marks the first beat.
func downbeat(bass []float32, at []int, perBar int) int {
	if perBar < 2 {
		return 0
	}
	best, pick := math.Inf(-1), 0
	for p := range perBar {
		var sum float64
		var n int
		for i := p; i < len(at); i += perBar {
			sum += float64(peak(bass, at[i]))
			n++
		}
		if n == 0 {
			continue
		}
		if mean := sum / float64(n); mean > best {
			best, pick = mean, p
		}
	}
	return pick
}

// trim drops the beats at either end that carry no onset: a lead-in, or
// a quiet tail, where the beat was carried along rather than heard.
func trim(f []float32, at []int) []int {
	if len(at) == 0 {
		return at
	}
	var sum float64
	for _, i := range at {
		sum += float64(peak(f, i))
	}
	gate := float32(sum / float64(len(at)) / 4)
	lo := 0
	for lo < len(at) && peak(f, at[lo]) < gate {
		lo++
	}
	hi := len(at)
	for hi > lo && peak(f, at[hi-1]) < gate {
		hi--
	}
	return at[lo:hi]
}

// place is where a beat falls between the columns, read from the onset
// around it, so a beat is not rounded to the hundredth of a second the
// columns are taken at. A beat landing where there is no onset to read
// stays on its column.
func place(f []float32, i int) float64 {
	if i <= 0 || i+1 >= len(f) {
		return float64(i)
	}
	a, b, c := float64(f[i-1]), float64(f[i]), float64(f[i+1])
	if bend := a - 2*b + c; bend < 0 && b > a && b > c {
		return float64(i) + min(max(0.5*(a-c)/bend, -0.5), 0.5)
	}
	return float64(i)
}

// peak is the strength at a column, taking the most of it and the
// columns either side, since an onset may land a column out.
func peak(f []float32, i int) float32 {
	var out float32
	for k := max(i-1, 0); k <= min(i+1, len(f)-1); k++ {
		out = max(out, f[k])
	}
	return out
}
