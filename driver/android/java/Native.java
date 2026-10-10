package gunim.android;

import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.pm.PackageManager;
import android.os.Build;
import android.os.Environment;
import android.os.Handler;
import android.os.Looper;
import android.view.Surface;

/**
 * Native is the bridge to the Go half of gunim's Android driver. Its
 * native methods hand Go what happens on the UI thread; its static
 * methods are what Go calls, from threads of its own.
 */
public final class Native {
	static GunimActivity activity;
	static GunimView view;
	// app is the application's context, which outlives any activity.
	static Context app;

	private static final Handler ui = new Handler(Looper.getMainLooper());

	static native void start(String zone);
	static native void surfaceChanged(Surface surface, int width, int height);
	static native void surfaceDestroyed(Surface surface);
	static native void metrics(float density, float refreshRate);
	static native void touch(int action, float x, float y, long time);
	static native void pinch(int action, float x0, float y0, float x1, float y1);
	static native void key(boolean down, int code, int meta, int ch, int repeat);
	static native void focus(boolean focused);
	static native void shown(boolean shown);
	static native void keyboard(int px);
	static native void insets(int top, int right, int bottom, int left);
	static native void media(int action, long position);
	static native void edit(String with, int replaceStart, int replaceEnd, int selAnchor, int selCaret,
		int compStart, int compEnd, long seq);
	static native void text(String s);
	static native void composing(String s, int selStart, int selEnd);
	static native void answered(int code, boolean granted);
	static native void chosen(int code, String path);
	static native void heading(float[] rotation, int screen, int accuracy, float error);

	// Called from Go.

	static void nowPlaying(boolean on, boolean playing, String title, String artist, String album,
		long length, long position, byte[] art) {
		GunimService.State st = on ? new GunimService.State(playing, title, artist, album, length, position, art) : null;
		ui.post(() -> GunimService.show(app, st));
	}

	static void showKeyboard(boolean show) {
		ui.post(() -> {
			if (view != null) {
				view.showKeyboard(show);
			}
		});
	}

	static void caret(int x0, int y0, int x1, int y1) {
		ui.post(() -> {
			if (view != null) {
				view.setCaret(x0, y0, x1, y1);
			}
		});
	}

	static void textState(char[] text, int start, int selA, int selB, int compA, int compB,
		boolean multiline, boolean secret, long seq) {
		String s = new String(text);
		ui.post(() -> {
			if (view != null) {
				view.input.setState(s, start, selA, selB, compA, compB, multiline, secret, seq);
			}
		});
	}

	static void clearTextState() {
		ui.post(() -> {
			if (view != null) {
				view.input.clearState();
			}
		});
	}

	static String getClipboard() {
		GunimActivity a = activity;
		if (a == null) {
			return null;
		}
		ClipboardManager cm = (ClipboardManager) a.getSystemService(Context.CLIPBOARD_SERVICE);
		ClipData clip = cm.getPrimaryClip();
		if (clip == null || clip.getItemCount() == 0) {
			return null;
		}
		CharSequence s = clip.getItemAt(0).coerceToText(a);
		return s == null ? null : s.toString();
	}

	static void setClipboard(String s) {
		GunimActivity a = activity;
		if (a == null) {
			return;
		}
		ClipboardManager cm = (ClipboardManager) a.getSystemService(Context.CLIPBOARD_SERVICE);
		cm.setPrimaryClip(ClipData.newPlainText("text", s));
	}

	// permissionNames names permission p, as driver.Permission numbers
	// it, as this Android names it: reading music is its own
	// permission since Android 13, and part of reading storage before.
	static String[] permissionNames(int p) {
		if (p == 1) {
			return Build.VERSION.SDK_INT >= 33
				? new String[] {"android.permission.READ_MEDIA_AUDIO"}
				: new String[] {"android.permission.READ_EXTERNAL_STORAGE"};
		}
		return null;
	}

	static boolean permitted(int p) {
		String[] names = permissionNames(p);
		if (names == null || app == null) {
			return false;
		}
		for (String n : names) {
			if (app.checkSelfPermission(n) != PackageManager.PERMISSION_GRANTED) {
				return false;
			}
		}
		return true;
	}

	// ask asks the user for permission p with the system's prompt; the
	// answer goes to Go as answered(code, granted), at once where there
	// is no activity to ask over.
	static void ask(int p, int code) {
		ui.post(() -> {
			String[] names = permissionNames(p);
			GunimActivity a = activity;
			if (names == null || a == null) {
				answered(code, permitted(p));
				return;
			}
			a.requestPermissions(names, code);
		});
	}

	// chooseFolder shows the system's chooser of folders; the folder
	// chosen goes to Go as chosen(code, path), with null for none.
	static void chooseFolder(int code) {
		ui.post(() -> {
			GunimActivity a = activity;
			if (a == null) {
				chosen(code, null);
				return;
			}
			try {
				a.startActivityForResult(new android.content.Intent(android.content.Intent.ACTION_OPEN_DOCUMENT_TREE), code);
			} catch (android.content.ActivityNotFoundException e) {
				chosen(code, null);
			}
		});
	}

	// pathOf returns the path of a folder the chooser gave, where it
	// lies on the phone's storage or a card: the chooser names those
	// as their volume and the path within it, as primary:Download.
	// Other providers' folders have no path, and give null.
	static String pathOf(android.net.Uri tree) {
		if (tree == null || !"com.android.externalstorage.documents".equals(tree.getAuthority())) {
			return null;
		}
		String id = android.provider.DocumentsContract.getTreeDocumentId(tree);
		int colon = id.indexOf(':');
		if (colon < 0) {
			return null;
		}
		String volume = id.substring(0, colon), rest = id.substring(colon + 1);
		String root = "primary".equals(volume)
			? Environment.getExternalStorageDirectory().getAbsolutePath()
			: "/storage/" + volume;
		return rest.isEmpty() ? root : root + "/" + rest;
	}

	// userFolder returns the shared folder of kind f, as
	// driver.UserFolder numbers it, or null.
	static String userFolder(int f) {
		if (f == 1) {
			return Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_MUSIC).getAbsolutePath();
		}
		return null;
	}

	static void buzz() {
		ui.post(() -> {
			if (view != null) {
				view.performHapticFeedback(android.view.HapticFeedbackConstants.LONG_PRESS);
			}
		});
	}

	// share shows the system's share sheet with text, a subject and
	// files, the paths each ended by a NUL. It returns false where there
	// is no activity to show the sheet over.
	static boolean share(String text, String subject, String paths) {
		GunimActivity a = activity;
		if (a == null) {
			return false;
		}
		java.util.ArrayList<android.net.Uri> uris = new java.util.ArrayList<>();
		String type = null;
		for (String p : paths.split("\0")) {
			if (p.isEmpty()) {
				continue;
			}
			uris.add(GunimFiles.uriOf(a, p));
			type = GunimFiles.commonType(type, GunimFiles.typeOf(p));
		}
		android.content.Intent send;
		if (uris.size() > 1) {
			send = new android.content.Intent(android.content.Intent.ACTION_SEND_MULTIPLE);
			send.putParcelableArrayListExtra(android.content.Intent.EXTRA_STREAM, uris);
		} else {
			send = new android.content.Intent(android.content.Intent.ACTION_SEND);
			if (uris.size() == 1) {
				send.putExtra(android.content.Intent.EXTRA_STREAM, uris.get(0));
			}
		}
		send.setType(type != null ? type : "text/plain");
		if (!text.isEmpty()) {
			send.putExtra(android.content.Intent.EXTRA_TEXT, text);
		}
		if (!subject.isEmpty()) {
			send.putExtra(android.content.Intent.EXTRA_SUBJECT, subject);
		}
		if (!uris.isEmpty()) {
			// The clip carries the grant to read each file through the
			// chooser to the application the user picks.
			ClipData clip = ClipData.newRawUri(null, uris.get(0));
			for (int i = 1; i < uris.size(); i++) {
				clip.addItem(new ClipData.Item(uris.get(i)));
			}
			send.setClipData(clip);
			send.addFlags(android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION);
		}
		android.content.Intent chooser = android.content.Intent.createChooser(send, subject.isEmpty() ? null : subject);
		ui.post(() -> {
			try {
				a.startActivity(chooser);
			} catch (android.content.ActivityNotFoundException e) {
				// The system has no share sheet; the share goes nowhere.
			}
		});
		return true;
	}

	// openLink opens the web page at url in the browser the phone keeps
	// for the web. It returns false where there is no activity to start
	// it from. Android 11 and later hide which browsers there are from an
	// app that does not list them, so a phone with none opens nothing.
	static boolean openLink(String url) {
		GunimActivity a = activity;
		if (a == null) {
			return false;
		}
		android.content.Intent view = new android.content.Intent(android.content.Intent.ACTION_VIEW,
			android.net.Uri.parse(url));
		view.addCategory(android.content.Intent.CATEGORY_BROWSABLE);
		ui.post(() -> {
			try {
				a.startActivity(view);
			} catch (android.content.ActivityNotFoundException e) {
				// The browser went away since; the link opens nowhere.
			}
		});
		return true;
	}

	// vibrate runs the vibration motor in a pattern of milliseconds, on
	// and off in turn from on, and an empty one stops it. It returns
	// false where the device has no motor.
	@SuppressWarnings("deprecation")
	static boolean vibrate(long[] pattern) {
		if (app == null) {
			return false;
		}
		android.os.Vibrator v = (android.os.Vibrator) app.getSystemService(Context.VIBRATOR_SERVICE);
		if (v == null || !v.hasVibrator()) {
			return false;
		}
		long on = 0;
		for (int i = 0; i < pattern.length; i += 2) {
			on += pattern[i];
		}
		v.cancel();
		if (on == 0) {
			return true;
		}
		if (pattern.length == 1) {
			v.vibrate(android.os.VibrationEffect.createOneShot(pattern[0], android.os.VibrationEffect.DEFAULT_AMPLITUDE));
			return true;
		}
		// A waveform starts with a wait, which a pattern starts without.
		long[] timings = new long[pattern.length + 1];
		System.arraycopy(pattern, 0, timings, 1, pattern.length);
		v.vibrate(android.os.VibrationEffect.createWaveform(timings, -1));
		return true;
	}

	// hasCompass reports whether the phone has the sensors that tell
	// which way it faces.
	static boolean hasCompass() {
		return app != null && GunimCompass.of(app).has();
	}

	// watchHeading starts or stops those sensors; while they run, Go
	// hears each reading as heading.
	static void watchHeading(boolean on) {
		ui.post(() -> {
			if (app != null) {
				GunimCompass.of(app).watch(on);
			}
		});
	}

	static void finish() {
		ui.post(() -> {
			if (activity != null) {
				activity.finish();
			}
		});
	}
}
