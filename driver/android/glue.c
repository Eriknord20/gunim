//go:build android

#include <jni.h>
#include <stdlib.h>
#include <string.h>
#include <android/native_window_jni.h>
#include <android/log.h>
#include <EGL/egl.h>
#include <EGL/eglext.h>
#include "_cgo_export.h"
#include "glue.h"

static JavaVM *vm;
static jclass nativeClass;
static jmethodID midPermitted, midAsk, midUserFolder, midChooseFolder;
static jmethodID midNowPlaying, midShowKeyboard, midCaret, midBuzz, midShare, midVibrate, midOpenLink, midTextState, midClearTextState, midGetClipboard, midSetClipboard, midFinish;
static jmethodID midHasCompass, midWatchHeading;

// JNI_OnLoad runs on the thread that loads the library, which has the
// application's class loader, so it looks up the class Go calls back.
JNIEXPORT jint JNI_OnLoad(JavaVM *v, void *reserved) {
	vm = v;
	JNIEnv *env;
	if ((*vm)->GetEnv(vm, (void **)&env, JNI_VERSION_1_6) != JNI_OK) {
		return -1;
	}
	jclass c = (*env)->FindClass(env, "gunim/android/Native");
	if (c == NULL) {
		return -1;
	}
	nativeClass = (*env)->NewGlobalRef(env, c);
	midShowKeyboard = (*env)->GetStaticMethodID(env, c, "showKeyboard", "(Z)V");
	midNowPlaying = (*env)->GetStaticMethodID(env, c, "nowPlaying",
		"(ZZLjava/lang/String;Ljava/lang/String;Ljava/lang/String;JJ[B)V");
	midCaret = (*env)->GetStaticMethodID(env, c, "caret", "(IIII)V");
	midBuzz = (*env)->GetStaticMethodID(env, c, "buzz", "()V");
	midShare = (*env)->GetStaticMethodID(env, c, "share", "(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;)Z");
	midVibrate = (*env)->GetStaticMethodID(env, c, "vibrate", "([J)Z");
	midOpenLink = (*env)->GetStaticMethodID(env, c, "openLink", "(Ljava/lang/String;)Z");
	midHasCompass = (*env)->GetStaticMethodID(env, c, "hasCompass", "()Z");
	midWatchHeading = (*env)->GetStaticMethodID(env, c, "watchHeading", "(Z)V");
	midTextState = (*env)->GetStaticMethodID(env, c, "textState", "([CIIIIIZZJ)V");
	midClearTextState = (*env)->GetStaticMethodID(env, c, "clearTextState", "()V");
	midGetClipboard = (*env)->GetStaticMethodID(env, c, "getClipboard", "()Ljava/lang/String;");
	midSetClipboard = (*env)->GetStaticMethodID(env, c, "setClipboard", "(Ljava/lang/String;)V");
	midFinish = (*env)->GetStaticMethodID(env, c, "finish", "()V");
	midPermitted = (*env)->GetStaticMethodID(env, c, "permitted", "(I)Z");
	midAsk = (*env)->GetStaticMethodID(env, c, "ask", "(II)V");
	midUserFolder = (*env)->GetStaticMethodID(env, c, "userFolder", "(I)Ljava/lang/String;");
	midChooseFolder = (*env)->GetStaticMethodID(env, c, "chooseFolder", "(I)V");
	return JNI_VERSION_1_6;
}

// envGet returns the calling thread's JNIEnv, attaching the thread to
// the VM when it is a thread of Go's, and envPut detaches it again.
static JNIEnv *envGet(int *attached) {
	JNIEnv *env = NULL;
	*attached = 0;
	if ((*vm)->GetEnv(vm, (void **)&env, JNI_VERSION_1_6) == JNI_EDETACHED) {
		(*vm)->AttachCurrentThread(vm, &env, NULL);
		*attached = 1;
	}
	return env;
}

static void envPut(int attached) {
	if (attached) {
		(*vm)->DetachCurrentThread(vm);
	}
}

void gunim_show_keyboard(int show) {
	int a;
	JNIEnv *env = envGet(&a);
	(*env)->CallStaticVoidMethod(env, nativeClass, midShowKeyboard, (jboolean)(show != 0));
	envPut(a);
}

void gunim_now_playing(int on, int playing, const uint16_t *title, int nt, const uint16_t *artist, int na,
	const uint16_t *album, int nl, long long length, long long position, const void *art, int nart) {
	int a;
	JNIEnv *env = envGet(&a);
	jstring jt = (*env)->NewString(env, (const jchar *)title, nt);
	jstring jr = (*env)->NewString(env, (const jchar *)artist, na);
	jstring jl = (*env)->NewString(env, (const jchar *)album, nl);
	jbyteArray ja = NULL;
	if (nart > 0) {
		ja = (*env)->NewByteArray(env, nart);
		(*env)->SetByteArrayRegion(env, ja, 0, nart, (const jbyte *)art);
	}
	(*env)->CallStaticVoidMethod(env, nativeClass, midNowPlaying, (jboolean)(on != 0), (jboolean)(playing != 0),
		jt, jr, jl, (jlong)length, (jlong)position, ja);
	(*env)->DeleteLocalRef(env, jt);
	(*env)->DeleteLocalRef(env, jr);
	(*env)->DeleteLocalRef(env, jl);
	if (ja != NULL) {
		(*env)->DeleteLocalRef(env, ja);
	}
	envPut(a);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_media(JNIEnv *env, jclass c, jint action, jlong ms) {
	goMedia(action, ms);
}

void gunim_caret(int x0, int y0, int x1, int y1) {
	int a;
	JNIEnv *env = envGet(&a);
	(*env)->CallStaticVoidMethod(env, nativeClass, midCaret, x0, y0, x1, y1);
	envPut(a);
}

void gunim_text_state(const uint16_t *text, int n, int start, int selA, int selB, int compA, int compB,
	int multiline, int secret, long long seq) {
	int a;
	JNIEnv *env = envGet(&a);
	jcharArray chars = (*env)->NewCharArray(env, n);
	(*env)->SetCharArrayRegion(env, chars, 0, n, (const jchar *)text);
	(*env)->CallStaticVoidMethod(env, nativeClass, midTextState, chars, start, selA, selB, compA, compB,
		(jboolean)(multiline != 0), (jboolean)(secret != 0), (jlong)seq);
	(*env)->DeleteLocalRef(env, chars);
	envPut(a);
}

void gunim_clear_text_state(void) {
	int a;
	JNIEnv *env = envGet(&a);
	(*env)->CallStaticVoidMethod(env, nativeClass, midClearTextState);
	envPut(a);
}

uint16_t *gunim_get_clipboard(int *n) {
	int a;
	JNIEnv *env = envGet(&a);
	jstring s = (jstring)(*env)->CallStaticObjectMethod(env, nativeClass, midGetClipboard);
	uint16_t *out = NULL;
	*n = 0;
	if (s != NULL) {
		*n = (*env)->GetStringLength(env, s);
		out = malloc((*n + 1) * sizeof(uint16_t));
		(*env)->GetStringRegion(env, s, 0, *n, (jchar *)out);
		(*env)->DeleteLocalRef(env, s);
	}
	envPut(a);
	return out;
}

void gunim_set_clipboard(const uint16_t *s, int n) {
	int a;
	JNIEnv *env = envGet(&a);
	jstring js = (*env)->NewString(env, (const jchar *)s, n);
	(*env)->CallStaticVoidMethod(env, nativeClass, midSetClipboard, js);
	(*env)->DeleteLocalRef(env, js);
	envPut(a);
}

int gunim_permitted(int p) {
	int a;
	JNIEnv *env = envGet(&a);
	jboolean ok = (*env)->CallStaticBooleanMethod(env, nativeClass, midPermitted, p);
	envPut(a);
	return ok ? 1 : 0;
}

void gunim_ask(int p, int code) {
	int a;
	JNIEnv *env = envGet(&a);
	(*env)->CallStaticVoidMethod(env, nativeClass, midAsk, p, code);
	envPut(a);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_answered(JNIEnv *env, jclass c, jint code, jboolean granted) {
	goAnswered(code, granted ? 1 : 0);
}

void gunim_choose_folder(int code) {
	int a;
	JNIEnv *env = envGet(&a);
	(*env)->CallStaticVoidMethod(env, nativeClass, midChooseFolder, code);
	envPut(a);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_chosen(JNIEnv *env, jclass c, jint code, jstring path) {
	if (path == NULL) {
		goChosen(code, NULL, 0);
		return;
	}
	jsize n = (*env)->GetStringLength(env, path);
	const jchar *s = (*env)->GetStringChars(env, path, NULL);
	goChosen(code, (uint16_t *)s, n);
	(*env)->ReleaseStringChars(env, path, s);
}

uint16_t *gunim_user_folder(int f, int *n) {
	int a;
	JNIEnv *env = envGet(&a);
	jstring s = (jstring)(*env)->CallStaticObjectMethod(env, nativeClass, midUserFolder, f);
	uint16_t *out = NULL;
	*n = 0;
	if (s != NULL) {
		*n = (*env)->GetStringLength(env, s);
		out = malloc((*n + 1) * sizeof(uint16_t));
		(*env)->GetStringRegion(env, s, 0, *n, (jchar *)out);
		(*env)->DeleteLocalRef(env, s);
	}
	envPut(a);
	return out;
}

void gunim_buzz(void) {
	int a;
	JNIEnv *env = envGet(&a);
	(*env)->CallStaticVoidMethod(env, nativeClass, midBuzz);
	envPut(a);
}

int gunim_share(const uint16_t *text, int nt, const uint16_t *subject, int ns, const uint16_t *paths, int np) {
	int a;
	JNIEnv *env = envGet(&a);
	jstring jt = (*env)->NewString(env, (const jchar *)text, nt);
	jstring js = (*env)->NewString(env, (const jchar *)subject, ns);
	jstring jp = (*env)->NewString(env, (const jchar *)paths, np);
	jboolean ok = (*env)->CallStaticBooleanMethod(env, nativeClass, midShare, jt, js, jp);
	(*env)->DeleteLocalRef(env, jt);
	(*env)->DeleteLocalRef(env, js);
	(*env)->DeleteLocalRef(env, jp);
	envPut(a);
	return ok ? 1 : 0;
}

int gunim_vibrate(const long long *ms, int n) {
	int a;
	JNIEnv *env = envGet(&a);
	jlongArray jm = (*env)->NewLongArray(env, n);
	if (n > 0) {
		(*env)->SetLongArrayRegion(env, jm, 0, n, (const jlong *)ms);
	}
	jboolean ok = (*env)->CallStaticBooleanMethod(env, nativeClass, midVibrate, jm);
	(*env)->DeleteLocalRef(env, jm);
	envPut(a);
	return ok ? 1 : 0;
}

int gunim_open_link(const uint16_t *url, int n) {
	int a;
	JNIEnv *env = envGet(&a);
	jstring ju = (*env)->NewString(env, (const jchar *)url, n);
	jboolean ok = (*env)->CallStaticBooleanMethod(env, nativeClass, midOpenLink, ju);
	(*env)->DeleteLocalRef(env, ju);
	envPut(a);
	return ok ? 1 : 0;
}

int gunim_has_compass(void) {
	int a;
	JNIEnv *env = envGet(&a);
	jboolean ok = (*env)->CallStaticBooleanMethod(env, nativeClass, midHasCompass);
	envPut(a);
	return ok ? 1 : 0;
}

void gunim_watch_heading(int on) {
	int a;
	JNIEnv *env = envGet(&a);
	(*env)->CallStaticVoidMethod(env, nativeClass, midWatchHeading, (jboolean)(on != 0));
	envPut(a);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_heading(JNIEnv *env, jclass c, jfloatArray rotation, jint screen,
	jint accuracy, jfloat error) {
	float r[9];
	(*env)->GetFloatArrayRegion(env, rotation, 0, 9, r);
	goHeading(r, screen, accuracy, error);
}

void gunim_finish(void) {
	int a;
	JNIEnv *env = envGet(&a);
	(*env)->CallStaticVoidMethod(env, nativeClass, midFinish);
	envPut(a);
}

// The JNI methods of gunim.android.Native, which Java calls on its UI
// thread. Each hands over to Go.

JNIEXPORT void JNICALL Java_gunim_android_Native_start(JNIEnv *env, jclass c, jstring zone) {
	jsize n = (*env)->GetStringLength(env, zone);
	const jchar *s = (*env)->GetStringChars(env, zone, NULL);
	goStart((uint16_t *)s, n);
	(*env)->ReleaseStringChars(env, zone, s);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_surfaceChanged(JNIEnv *env, jclass c, jobject surface, jint w, jint h) {
	goSurfaceChanged(ANativeWindow_fromSurface(env, surface), w, h);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_surfaceDestroyed(JNIEnv *env, jclass c, jobject surface) {
	goSurfaceDestroyed(surface != NULL ? ANativeWindow_fromSurface(env, surface) : NULL);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_metrics(JNIEnv *env, jclass c, jfloat density, jfloat rate) {
	goMetrics(density, rate);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_touch(JNIEnv *env, jclass c, jint action, jfloat x, jfloat y, jlong ms) {
	goTouch(action, x, y, ms);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_pinch(JNIEnv *env, jclass c, jint action, jfloat x0, jfloat y0, jfloat x1, jfloat y1) {
	goPinch(action, x0, y0, x1, y1);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_key(JNIEnv *env, jclass c, jboolean down, jint code, jint meta, jint ch, jint repeat) {
	goKey(down, code, meta, ch, repeat);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_keyboard(JNIEnv *env, jclass c, jint px) {
	goKeyboard(px);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_insets(JNIEnv *env, jclass c, jint top, jint right, jint bottom, jint left) {
	goInsets(top, right, bottom, left);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_shown(JNIEnv *env, jclass c, jboolean shown) {
	goShown(shown);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_focus(JNIEnv *env, jclass c, jboolean focused) {
	goFocus(focused);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_edit(JNIEnv *env, jclass c, jstring with, jint r0, jint r1,
	jint s0, jint s1, jint c0, jint c1, jlong seq) {
	jsize n = (*env)->GetStringLength(env, with);
	const jchar *chars = (*env)->GetStringChars(env, with, NULL);
	goEdit((uint16_t *)chars, n, r0, r1, s0, s1, c0, c1, seq);
	(*env)->ReleaseStringChars(env, with, chars);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_text(JNIEnv *env, jclass c, jstring s) {
	jsize n = (*env)->GetStringLength(env, s);
	const jchar *chars = (*env)->GetStringChars(env, s, NULL);
	goText((uint16_t *)chars, n);
	(*env)->ReleaseStringChars(env, s, chars);
}

JNIEXPORT void JNICALL Java_gunim_android_Native_composing(JNIEnv *env, jclass c, jstring s, jint selA, jint selB) {
	jsize n = (*env)->GetStringLength(env, s);
	const jchar *chars = (*env)->GetStringChars(env, s, NULL);
	goComposing((uint16_t *)chars, n, selA, selB);
	(*env)->ReleaseStringChars(env, s, chars);
}

// EGL. One context serves every window: they all draw on the render
// thread, into the one surface Android gives the activity.

static EGLDisplay dpy = EGL_NO_DISPLAY;
static EGLConfig cfg;
static EGLContext ctx = EGL_NO_CONTEXT;
static EGLSurface surf = EGL_NO_SURFACE;
// idle is a 1×1 pbuffer the context is current on while there is no
// window surface: not every EGL can make a context current on none.
static EGLSurface idle = EGL_NO_SURFACE;

int gunim_egl_init(void) {
	dpy = eglGetDisplay(EGL_DEFAULT_DISPLAY);
	if (dpy == EGL_NO_DISPLAY || !eglInitialize(dpy, NULL, NULL)) {
		return eglGetError();
	}
	const EGLint attribs[] = {
		EGL_RENDERABLE_TYPE, EGL_OPENGL_ES3_BIT_KHR,
		EGL_SURFACE_TYPE, EGL_WINDOW_BIT | EGL_PBUFFER_BIT,
		EGL_RED_SIZE, 8, EGL_GREEN_SIZE, 8, EGL_BLUE_SIZE, 8, EGL_ALPHA_SIZE, 8,
		EGL_NONE,
	};
	EGLint n;
	if (!eglChooseConfig(dpy, attribs, &cfg, 1, &n) || n < 1) {
		return eglGetError() != EGL_SUCCESS ? eglGetError() : EGL_BAD_CONFIG;
	}
	const EGLint ctxAttribs[] = {EGL_CONTEXT_CLIENT_VERSION, 3, EGL_NONE};
	ctx = eglCreateContext(dpy, cfg, EGL_NO_CONTEXT, ctxAttribs);
	if (ctx == EGL_NO_CONTEXT) {
		return eglGetError();
	}
	const EGLint pbAttribs[] = {EGL_WIDTH, 1, EGL_HEIGHT, 1, EGL_NONE};
	idle = eglCreatePbufferSurface(dpy, cfg, pbAttribs);
	if (idle == EGL_NO_SURFACE) {
		return eglGetError();
	}
	if (!eglMakeCurrent(dpy, idle, idle, ctx)) {
		return eglGetError();
	}
	return 0;
}

int gunim_egl_attach(ANativeWindow *w) {
	gunim_egl_detach();
	surf = eglCreateWindowSurface(dpy, cfg, w, NULL);
	if (surf == EGL_NO_SURFACE) {
		return eglGetError();
	}
	if (!eglMakeCurrent(dpy, surf, surf, ctx)) {
		return eglGetError();
	}
	eglSwapInterval(dpy, 1);
	return 0;
}

void gunim_egl_detach(void) {
	if (surf == EGL_NO_SURFACE) {
		return;
	}
	eglMakeCurrent(dpy, idle, idle, ctx);
	eglDestroySurface(dpy, surf);
	surf = EGL_NO_SURFACE;
}

int gunim_egl_swap(void) {
	if (!eglSwapBuffers(dpy, surf)) {
		return eglGetError();
	}
	return 0;
}

void gunim_window_release(ANativeWindow *w) {
	ANativeWindow_release(w);
}

void gunim_log(const char *line) {
	__android_log_write(ANDROID_LOG_INFO, "gunim", line);
}
