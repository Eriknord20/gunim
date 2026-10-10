package gunim.android;

import android.content.Context;
import android.view.KeyEvent;
import android.view.MotionEvent;
import android.view.SurfaceHolder;
import android.view.SurfaceView;
import android.view.View;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputConnection;
import android.view.inputmethod.InputMethodManager;

/**
 * GunimView is the surface gunim draws every window into. It hands Go
 * the surface, the first finger's touches, the keys, and through
 * GunimInput the soft keyboard.
 */
final class GunimView extends SurfaceView implements SurfaceHolder.Callback {
	final GunimInput input;
	private final InputMethodManager imm;
	// caret is the text caret on the surface, in pixels, empty for none.
	private final android.graphics.Rect caret = new android.graphics.Rect();
	// pointer is the id of the finger that drives the pointer, or -1,
	// and second the finger pinching with it while pinching.
	private int pointer = -1;
	private int second = -1;
	private boolean pinching;

	GunimView(Context c) {
		super(c);
		imm = (InputMethodManager) c.getSystemService(Context.INPUT_METHOD_SERVICE);
		input = new GunimInput(this, imm);
		getHolder().addCallback(this);
		setFocusable(true);
		setFocusableInTouchMode(true);
	}

	@Override
	public void surfaceCreated(SurfaceHolder h) {}

	@Override
	public void surfaceChanged(SurfaceHolder h, int format, int width, int height) {
		float density = getResources().getDisplayMetrics().density;
		float rate = getDisplay() != null ? getDisplay().getRefreshRate() : 60;
		Native.metrics(density, rate);
		Native.surfaceChanged(h.getSurface(), width, height);
	}

	@Override
	public void surfaceDestroyed(SurfaceHolder h) {
		// The surface goes by name: a new activity's may have come
		// already, as when Android replaces the activity at start.
		Native.surfaceDestroyed(h.getSurface());
	}

	@Override
	public boolean onTouchEvent(MotionEvent e) {
		int action = e.getActionMasked();
		switch (action) {
		case MotionEvent.ACTION_DOWN:
			pointer = e.getPointerId(0);
			second = -1;
			pinching = false;
			requestFocus();
			Native.touch(0, e.getX(0), e.getY(0), e.getEventTime());
			return true;
		case MotionEvent.ACTION_POINTER_DOWN:
			if (pointer >= 0 && !pinching) {
				second = e.getPointerId(e.getActionIndex());
				pinching = true;
				pinch(0, e);
			}
			return true;
		case MotionEvent.ACTION_MOVE: {
			if (pinching) {
				pinch(1, e);
				return true;
			}
			int i = e.findPointerIndex(pointer);
			if (i >= 0) {
				Native.touch(1, e.getX(i), e.getY(i), e.getEventTime());
			}
			return true;
		}
		case MotionEvent.ACTION_POINTER_UP: {
			int id = e.getPointerId(e.getActionIndex());
			if (pinching && (id == pointer || id == second)) {
				pinch(2, e);
				pinching = false;
			} else if (!pinching && id == pointer) {
				int i = e.getActionIndex();
				Native.touch(2, e.getX(i), e.getY(i), e.getEventTime());
				pointer = -1;
			}
			return true;
		}
		case MotionEvent.ACTION_UP:
			// The last finger lifts: the touch ends, whatever it became.
			Native.touch(2, e.getX(0), e.getY(0), e.getEventTime());
			pointer = -1;
			pinching = false;
			return true;
		case MotionEvent.ACTION_CANCEL:
			Native.touch(3, 0, 0, e.getEventTime());
			pointer = -1;
			pinching = false;
			return true;
		}
		return true;
	}

	/** pinch hands Go the two pinching fingers. */
	private void pinch(int action, MotionEvent e) {
		int i0 = e.findPointerIndex(pointer);
		int i1 = e.findPointerIndex(second);
		if (i0 < 0 || i1 < 0) {
			return;
		}
		Native.pinch(action, e.getX(i0), e.getY(i0), e.getX(i1), e.getY(i1));
	}


	@Override
	public boolean onKeyDown(int code, KeyEvent e) {
		if (code != KeyEvent.KEYCODE_BACK && e.isSystem()) {
			return super.onKeyDown(code, e);
		}
		Native.key(true, code, e.getMetaState(), e.getUnicodeChar(), e.getRepeatCount());
		return true;
	}

	@Override
	public boolean onKeyUp(int code, KeyEvent e) {
		if (code != KeyEvent.KEYCODE_BACK && e.isSystem()) {
			return super.onKeyUp(code, e);
		}
		Native.key(false, code, e.getMetaState(), 0, 0);
		return true;
	}

	@Override
	public void onWindowFocusChanged(boolean focused) {
		super.onWindowFocusChanged(focused);
		// An old activity's view, losing the focus after its
		// replacement came, speaks no more for the program.
		if (Native.view == this) {
			Native.focus(focused);
		}
	}

	/**
	 * watchKeyboard tells Go how much of the view the soft keyboard
	 * covers, on each frame as it slides in and out, so the driver can
	 * slide the windows with it. The activity leaves the view its full
	 * size meanwhile.
	 *
	 * The window's decor view hears the insets, and passes nothing on,
	 * so the watch sits there and lets the decor go on as before. It
	 * tells of the system bars too, for watchInsets.
	 */
	@android.annotation.TargetApi(30)
	void watchKeyboard(View decor) {
		decor.setOnApplyWindowInsetsListener((v, insets) -> {
			reportKeyboard(insets);
			return v.onApplyWindowInsets(insets);
		});
		decor.setWindowInsetsAnimationCallback(new android.view.WindowInsetsAnimation.Callback(
			android.view.WindowInsetsAnimation.Callback.DISPATCH_MODE_CONTINUE_ON_SUBTREE) {
			@Override
			public android.view.WindowInsets onProgress(android.view.WindowInsets insets,
				java.util.List<android.view.WindowInsetsAnimation> running) {
				reportKeyboard(insets);
				return insets;
			}
		});
	}

	// covered is the keyboard's height over the view last reported.
	private int covered = -1;

	// safe is the system's bars' reach last reported, top, right,
	// bottom and left.
	private final int[] safe = {-1, -1, -1, -1};

	/**
	 * watchInsets tells Go how far in from each edge the system bars and
	 * the camera's cutout reach over the view, which lies under them.
	 * Before Android 11 it is the decor's own listener; from Android 11
	 * watchKeyboard's listener tells both.
	 */
	@SuppressWarnings("deprecation")
	void watchInsets(View decor) {
		if (android.os.Build.VERSION.SDK_INT >= 30) {
			return;
		}
		decor.setOnApplyWindowInsetsListener((v, insets) -> {
			reportSafe(insets.getSystemWindowInsetTop(), insets.getSystemWindowInsetRight(),
				insets.getSystemWindowInsetBottom(), insets.getSystemWindowInsetLeft());
			return v.onApplyWindowInsets(insets);
		});
		decor.requestApplyInsets();
	}

	private void reportSafe(int top, int right, int bottom, int left) {
		if (safe[0] == top && safe[1] == right && safe[2] == bottom && safe[3] == left) {
			return;
		}
		safe[0] = top;
		safe[1] = right;
		safe[2] = bottom;
		safe[3] = left;
		Native.insets(top, right, bottom, left);
	}

	@android.annotation.TargetApi(30)
	private void reportKeyboard(android.view.WindowInsets insets) {
		int ime = insets.getInsets(android.view.WindowInsets.Type.ime()).bottom;
		int nav = insets.getInsets(android.view.WindowInsets.Type.navigationBars()).bottom;
		android.graphics.Insets bars = insets.getInsets(
			android.view.WindowInsets.Type.systemBars() | android.view.WindowInsets.Type.displayCutout());
		reportSafe(bars.top, bars.right, bars.bottom, bars.left);
		// The view runs to the screen's bottom, under the navigation bar,
		// so the keyboard covers all of its own height.
		int px = Math.max(0, ime);
		GunimInput.debug("insets ime=" + ime + " nav=" + nav + " visible=" + insets.isVisible(android.view.WindowInsets.Type.ime()));
		if (px != covered) {
			covered = px;
			Native.keyboard(px);
		}
	}

	/**
	 * setCaret keeps where the text caret is. In the activity's pan mode,
	 * Android slides the window up, as the keyboard opens, far enough to
	 * show the focused view's focused rectangle above it; for a gunim
	 * window that is the caret, with a line's room below it.
	 */
	void setCaret(int x0, int y0, int x1, int y1) {
		caret.set(x0, y0, x1, y1 + (y1 - y0));
		invalidate();
	}

	@Override
	public void getFocusedRect(android.graphics.Rect r) {
		if (caret.isEmpty() || !input.hasState()) {
			super.getFocusedRect(r);
			return;
		}
		r.set(caret);
	}

	@Override
	public boolean onCheckIsTextEditor() {
		return input.hasState();
	}

	@Override
	public InputConnection onCreateInputConnection(EditorInfo out) {
		return input.connect(out);
	}

	/**
	 * showKeyboard shows the soft keyboard, for a text field the user has
	 * tapped, and hides it. A node with no text of its own, which takes
	 * keys from a hardware keyboard, leaves it down.
	 */
	void showKeyboard(boolean show) {
		if (show && input.hasState()) {
			// The keyboard starts from the text as it is now, as it would
			// for a field it had not seen, so a word it put away while
			// hidden, or text the program set, reads right.
			requestFocus();
			imm.restartInput(this);
			imm.showSoftInput(this, 0);
		} else if (!show) {
			imm.hideSoftInputFromWindow(getWindowToken(), 0);
		}
	}
}
