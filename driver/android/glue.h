// The C half of the Android driver: the JNI entry points Java calls,
// the calls back into Java, and EGL. The Go half is in jni.go.

#include <stdint.h>
#include <android/native_window.h>

// Calls into Java, from any thread.
void gunim_show_keyboard(int show);
void gunim_now_playing(int on, int playing, const uint16_t *title, int nt, const uint16_t *artist, int na,
	const uint16_t *album, int nl, long long length, long long position, const void *art, int nart);
void gunim_caret(int x0, int y0, int x1, int y1);
void gunim_text_state(const uint16_t *text, int n, int start, int selA, int selB, int compA, int compB,
	int multiline, int secret, long long seq);
void gunim_clear_text_state(void);
uint16_t *gunim_get_clipboard(int *n);
void gunim_set_clipboard(const uint16_t *s, int n);
void gunim_finish(void);
void gunim_buzz(void);
int gunim_share(const uint16_t *text, int nt, const uint16_t *subject, int ns, const uint16_t *paths, int np);
int gunim_vibrate(const long long *ms, int n);
void gunim_exclude_gestures(const int *xy, int n);
int gunim_has_compass(void);
void gunim_watch_heading(int on);
int gunim_open_link(const uint16_t *url, int n);
int gunim_permitted(int p);
void gunim_ask(int p, int code);
void gunim_choose_folder(int code);
uint16_t *gunim_user_folder(int f, int *n);

// EGL, from the render thread. Each returns 0, or the EGL error.
int gunim_egl_init(void);
int gunim_egl_attach(ANativeWindow *w);
void gunim_egl_detach(void);
int gunim_egl_swap(void);
void gunim_window_release(ANativeWindow *w);

// gunim_log writes a line to the system log.
void gunim_log(const char *line);
