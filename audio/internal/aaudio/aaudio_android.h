// The C half of package aaudio: AAudio's streams, and the callbacks
// that run on AAudio's own thread. The callbacks touch nothing but the
// shared state and the rings, which live in C memory, so no Go runs on
// that thread.

#include <stdint.h>
#include <aaudio/AAudio.h>

// gunim_i64 is an int64_t aligned to 8 bytes on every ABI, as Go's
// atomic.Int64 is: 32-bit x86 aligns a plain one to 4.
typedef int64_t gunim_i64 __attribute__((aligned(8)));

// gunim_ring is a ring of frames, of chans values each, written by one
// thread and read by another, neither waiting. It mirrors ringHeader in
// ring.go, field for field.
typedef struct {
	gunim_i64 w, r;    // frames written and read, ever
	gunim_i64 size;    // frames it holds
	gunim_i64 chans;   // values a frame
	void *buf;
} gunim_ring;

// gunim_shared is what a stream's callbacks tell Go. It mirrors Shared
// in ring.go, field for field.
typedef struct {
	gunim_i64 frames;     // frames through the callback, since the stream opened
	gunim_i64 silence;    // output: frames of silence played where the ring ran short
	gunim_i64 underruns;  // output: callbacks that found the ring short
	gunim_i64 dropped;    // input: frames dropped where the ring was full
	gunim_i64 most;       // the most frames a callback has carried
	gunim_i64 err;        // the error the error callback was given, or 0
	gunim_ring *data;   // the frames
	gunim_ring *blocks; // input: each block's first frame and length
} gunim_shared;

// gunim_aa_open opens a stream: an input where input is set. It
// returns NULL, or what failed.
const char *gunim_aa_open(AAudioStream **out, gunim_shared *sh, int input, int rate, int chans, int exclusive);

// gunim_aa_api returns the device's Android API level.
int gunim_aa_api(void);

// gunim_aa_timestamp reads where the stream's frames are heard, or
// were recorded, on CLOCK_MONOTONIC, and the clock now. It returns 0,
// or AAudio's error.
int gunim_aa_timestamp(AAudioStream *s, int64_t *pos, int64_t *at, int64_t *now);
