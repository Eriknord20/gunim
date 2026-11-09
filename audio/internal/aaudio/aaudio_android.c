#include "aaudio_android.h"

#include <dlfcn.h>
#include <string.h>
#include <time.h>
#include <android/api-level.h>

static int64_t load(gunim_i64 *p) { return __atomic_load_n(p, __ATOMIC_ACQUIRE); }
static void store(gunim_i64 *p, int64_t v) { __atomic_store_n(p, v, __ATOMIC_RELEASE); }
static void add(gunim_i64 *p, int64_t v) { __atomic_fetch_add(p, v, __ATOMIC_RELEASE); }

// The layouts Go reads, as ring.go mirrors them.
_Static_assert(sizeof(gunim_ring) == 4 * 8 + 8, "gunim_ring's layout");
_Static_assert(sizeof(gunim_shared) == 6 * 8 + 2 * sizeof(void *), "gunim_shared's layout");

// pop reads up to n frames of floats into dst, and returns how many.
static int64_t pop(gunim_ring *q, float *dst, int64_t n) {
	int64_t r = load(&q->r), c = q->chans, size = q->size;
	int64_t have = load(&q->w) - r;
	if (n > have) {
		n = have;
	}
	float *buf = q->buf;
	for (int64_t i = 0; i < n;) {
		int64_t at = (r + i) % size;
		int64_t k = n - i < size - at ? n - i : size - at;
		memcpy(dst + i * c, buf + at * c, (size_t)(k * c) * sizeof(float));
		i += k;
	}
	store(&q->r, r + n);
	return n;
}

// room returns how many frames q has room for.
static int64_t room(gunim_ring *q) { return q->size - (load(&q->w) - load(&q->r)); }

// push writes n frames of size bytes a value, which room says fit.
static void push(gunim_ring *q, const void *src, int64_t n, size_t size) {
	int64_t w = load(&q->w), c = q->chans, sz = q->size;
	char *buf = q->buf;
	const char *in = src;
	for (int64_t i = 0; i < n;) {
		int64_t at = (w + i) % sz;
		int64_t k = n - i < sz - at ? n - i : sz - at;
		memcpy(buf + (size_t)(at * c) * size, in + (size_t)(i * c) * size, (size_t)(k * c) * size);
		i += k;
	}
	store(&q->w, w + n);
}

static void seen(gunim_shared *sh, int32_t n) {
	if (n > load(&sh->most)) {
		store(&sh->most, n);
	}
	add(&sh->frames, n);
}

// output plays the ring's frames, and silence where it runs short. It
// runs on AAudio's thread: it copies, and neither waits nor allocates.
static aaudio_data_callback_result_t output(AAudioStream *s, void *user, void *data, int32_t n) {
	(void)s;
	gunim_shared *sh = user;
	gunim_ring *q = sh->data;
	int64_t got = pop(q, data, n);
	if (got < n) {
		memset((float *)data + got * q->chans, 0, (size_t)((n - got) * q->chans) * sizeof(float));
		add(&sh->silence, n - got);
		add(&sh->underruns, 1);
	}
	seen(sh, n);
	return AAUDIO_CALLBACK_RESULT_CONTINUE;
}

// input keeps each block whole in the ring, with its first frame, or
// drops it whole where the ring is full. It runs as output does.
static aaudio_data_callback_result_t input(AAudioStream *s, void *user, void *data, int32_t n) {
	(void)s;
	gunim_shared *sh = user;
	if (room(sh->data) >= n && room(sh->blocks) >= 1) {
		int64_t block[2] = {load(&sh->frames), n};
		push(sh->data, data, n, sizeof(float));
		push(sh->blocks, block, 1, sizeof(int64_t));
	} else {
		add(&sh->dropped, n);
	}
	seen(sh, n);
	return AAUDIO_CALLBACK_RESULT_CONTINUE;
}

// failed keeps the error for Go, which opens the stream again: AAudio
// asks that no stream be closed or opened in the callback.
static void failed(AAudioStream *s, void *user, aaudio_result_t err) {
	(void)s;
	gunim_shared *sh = user;
	store(&sh->err, err ? err : AAUDIO_ERROR_DISCONNECTED);
}

int gunim_aa_api(void) { return android_get_device_api_level(); }

typedef void (*set_preset)(AAudioStreamBuilder *, aaudio_input_preset_t);
typedef void (*set_usage)(AAudioStreamBuilder *, aaudio_usage_t);
typedef void (*set_content)(AAudioStreamBuilder *, aaudio_content_type_t);

const char *gunim_aa_open(AAudioStream **out, gunim_shared *sh, int in, int rate, int chans, int exclusive, int quick,
	int device) {
	AAudioStreamBuilder *b;
	aaudio_result_t r = AAudio_createStreamBuilder(&b);
	if (r != AAUDIO_OK) {
		return AAudio_convertResultToText(r);
	}
	AAudioStreamBuilder_setDirection(b, in ? AAUDIO_DIRECTION_INPUT : AAUDIO_DIRECTION_OUTPUT);
	AAudioStreamBuilder_setSharingMode(b, exclusive ? AAUDIO_SHARING_MODE_EXCLUSIVE : AAUDIO_SHARING_MODE_SHARED);
	AAudioStreamBuilder_setPerformanceMode(b, AAUDIO_PERFORMANCE_MODE_LOW_LATENCY);
	AAudioStreamBuilder_setFormat(b, AAUDIO_FORMAT_PCM_FLOAT);
	AAudioStreamBuilder_setChannelCount(b, chans);
	AAudioStreamBuilder_setSampleRate(b, rate);
	if (device > 0) {
		AAudioStreamBuilder_setDeviceId(b, device);
	}
	if (!in) {
		// What the sound is for: on Bluetooth, Android asks the headset
		// for its quicker link for a game, where it has one. Both are
		// Android 9's, older than the API level gunim builds for, as
		// setInputPreset is.
		set_usage u = (set_usage)dlsym(RTLD_DEFAULT, "AAudioStreamBuilder_setUsage");
		set_content c = (set_content)dlsym(RTLD_DEFAULT, "AAudioStreamBuilder_setContentType");
		if (u && c) {
			u(b, quick ? AAUDIO_USAGE_GAME : AAUDIO_USAGE_MEDIA);
			c(b, AAUDIO_CONTENT_TYPE_MUSIC);
		}
	}
	if (in) {
		// Singing over a song: the preset for a live performance, low
		// latency and none of a call's processing, from Android 10.
		// AAudio's own is older than the API level gunim builds for.
		set_preset f = (set_preset)dlsym(RTLD_DEFAULT, "AAudioStreamBuilder_setInputPreset");
		if (f && android_get_device_api_level() >= 29) {
			f(b, AAUDIO_INPUT_PRESET_VOICE_PERFORMANCE);
		}
	}
	AAudioStreamBuilder_setDataCallback(b, in ? input : output, sh);
	AAudioStreamBuilder_setErrorCallback(b, failed, sh);
	r = AAudioStreamBuilder_openStream(b, out);
	AAudioStreamBuilder_delete(b);
	if (r != AAUDIO_OK) {
		return AAudio_convertResultToText(r);
	}
	if (!in) {
		// Two bursts in the device: the callback reads a ring Go keeps
		// ahead, so it is never late for want of the mixer.
		int32_t burst = AAudioStream_getFramesPerBurst(*out);
		if (burst > 0) {
			AAudioStream_setBufferSizeInFrames(*out, 2 * burst);
		}
	}
	return NULL;
}

int gunim_aa_timestamp(AAudioStream *s, int64_t *pos, int64_t *at, int64_t *now) {
	aaudio_result_t r = AAudioStream_getTimestamp(s, CLOCK_MONOTONIC, pos, at);
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	*now = (int64_t)ts.tv_sec * 1000000000 + ts.tv_nsec;
	return r;
}

// The Android driver's calls into Java, where it is linked in: weak, so
// a program without it links all the same, and finds them NULL.
extern int gunim_audio_route(int device, int *r, int n) __attribute__((weak));
extern int gunim_audio_probe(int usage, int *r, int n) __attribute__((weak));
extern int gunim_audio_watch(void) __attribute__((weak));
extern void gunim_audio_changes(long long *out, long long *in) __attribute__((weak));

int gunim_aa_route(int device, int *r, int n) {
	return gunim_audio_route ? gunim_audio_route(device, r, n) : 0;
}

int gunim_aa_probe(int quick, int *r, int n) {
	return gunim_audio_probe ? gunim_audio_probe(quick ? AAUDIO_USAGE_GAME : AAUDIO_USAGE_MEDIA, r, n) : 0;
}

int gunim_aa_watch(void) { return gunim_audio_watch ? gunim_audio_watch() : 0; }

void gunim_aa_changes(int64_t *out, int64_t *in) {
	long long o = 0, i = 0;
	if (gunim_audio_changes) {
		gunim_audio_changes(&o, &i);
	}
	*out = o;
	*in = i;
}
