package gunim.android;

import android.app.Activity;
import android.os.Bundle;
import android.system.ErrnoException;
import android.system.Os;

/**
 * GunimActivity runs a gunim program. It loads the program, built as
 * libgunim.so, shows one GunimView, and starts the program's main
 * function the first time.
 */
public class GunimActivity extends Activity {
	@Override
	protected void onCreate(Bundle state) {
		super.onCreate(state);
		// Go looks for its home and caches where a desktop keeps them.
		try {
			Os.setenv("HOME", getFilesDir().getPath(), true);
			Os.setenv("TMPDIR", getCacheDir().getPath(), true);
			Os.setenv("XDG_CACHE_HOME", getCacheDir().getPath(), true);
			Os.setenv("XDG_CONFIG_HOME", getFilesDir().getPath(), true);
		} catch (ErrnoException e) {
			// Go falls back to its defaults.
		}
		System.loadLibrary("gunim");
		Native.app = getApplicationContext();
		Native.activity = this;
		GunimView v = new GunimView(this);
		Native.view = v;
		setContentView(v);
		edgeToEdge(v);
		if (android.os.Build.VERSION.SDK_INT >= 30) {
			// The view keeps its size as the keyboard opens; the driver
			// slides the drawing up instead, following the keyboard.
			getWindow().setSoftInputMode(android.view.WindowManager.LayoutParams.SOFT_INPUT_ADJUST_NOTHING
				| android.view.WindowManager.LayoutParams.SOFT_INPUT_STATE_ALWAYS_HIDDEN);
			v.watchKeyboard(getWindow().getDecorView());
		}
		if (android.os.Build.VERSION.SDK_INT >= 33) {
			watchBack();
		}
		v.requestFocus();
		Native.start(java.util.TimeZone.getDefault().getID());
		showBuild();
	}

	/**
	 * watchBack hands the back gesture and button to Go as the back key,
	 * which the driver takes as Escape. Android 13 and later tell an app
	 * of them through this callback rather than as a key, and Android 16
	 * only so, for an app built for it; GunimView takes the key on older
	 * Android.
	 */
	@android.annotation.TargetApi(33)
	private void watchBack() {
		getOnBackInvokedDispatcher().registerOnBackInvokedCallback(
			android.window.OnBackInvokedDispatcher.PRIORITY_DEFAULT, () -> {
				Native.key(true, android.view.KeyEvent.KEYCODE_BACK, 0, 0, 0);
				Native.key(false, android.view.KeyEvent.KEYCODE_BACK, 0, 0, 0);
			});
	}

	/**
	 * edgeToEdge lays the view under the status bar, the navigation bar
	 * and the camera's cutout, with the bars clear, and tells Go how far
	 * in from each edge they reach, so a program draws its background to
	 * the screen's edges and keeps what it shows clear of them.
	 */
	@SuppressWarnings("deprecation")
	private void edgeToEdge(GunimView v) {
		android.view.Window w = getWindow();
		w.setStatusBarColor(android.graphics.Color.TRANSPARENT);
		w.setNavigationBarColor(android.graphics.Color.TRANSPARENT);
		if (android.os.Build.VERSION.SDK_INT >= 29) {
			w.setStatusBarContrastEnforced(false);
			w.setNavigationBarContrastEnforced(false);
		}
		if (android.os.Build.VERSION.SDK_INT >= 28) {
			android.view.WindowManager.LayoutParams lp = w.getAttributes();
			lp.layoutInDisplayCutoutMode = android.os.Build.VERSION.SDK_INT >= 30
				? android.view.WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_ALWAYS
				: android.view.WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES;
			w.setAttributes(lp);
		}
		if (android.os.Build.VERSION.SDK_INT >= 30) {
			w.setDecorFitsSystemWindows(false);
		} else {
			w.getDecorView().setSystemUiVisibility(android.view.View.SYSTEM_UI_FLAG_LAYOUT_STABLE
				| android.view.View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN
				| android.view.View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION);
		}
		v.watchInsets(w.getDecorView());
	}

	/**
	 * showBuild says which build runs, in a debug build: gunimapk names
	 * each for its commit and the time it was built.
	 */
	private void showBuild() {
		if ((getApplicationInfo().flags & android.content.pm.ApplicationInfo.FLAG_DEBUGGABLE) == 0) {
			return;
		}
		try {
			String version = getPackageManager().getPackageInfo(getPackageName(), 0).versionName;
			android.widget.Toast.makeText(this, getApplicationInfo().loadLabel(getPackageManager()) + " " + version,
				android.widget.Toast.LENGTH_LONG).show();
		} catch (android.content.pm.PackageManager.NameNotFoundException e) {
			// The build has no name to show.
		}
	}

	// The program hears when the activity can no longer be seen, gone to
	// the background, and when it can again, to stop and start what only
	// matters while it is seen, as a game's music. Where Android replaces
	// the activity, as at start, the new one starts before the old one
	// stops, so only the activity the program runs in now speaks for it.
	@Override
	protected void onStart() {
		super.onStart();
		if (Native.activity == this) {
			Native.shown(true);
		}
	}

	@Override
	protected void onStop() {
		if (Native.activity == this) {
			Native.shown(false);
		}
		super.onStop();
	}

	@Override
	protected void onDestroy() {
		if (Native.activity == this) {
			Native.activity = null;
			Native.view = null;
		}
		super.onDestroy();
	}

	// The user answered a permission's prompt: granted where every
	// permission asked was.
	@Override
	public void onRequestPermissionsResult(int code, String[] permissions, int[] results) {
		boolean granted = results.length > 0;
		for (int r : results) {
			granted &= r == android.content.pm.PackageManager.PERMISSION_GRANTED;
		}
		Native.answered(code, granted);
	}

	// The system's chooser of folders closed: with the folder chosen,
	// or none.
	@Override
	protected void onActivityResult(int code, int result, android.content.Intent data) {
		String path = null;
		if (result == RESULT_OK && data != null) {
			path = Native.pathOf(data.getData());
		}
		Native.chosen(code, path);
	}
}
