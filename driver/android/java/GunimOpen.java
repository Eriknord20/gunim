package gunim.android;

import android.content.ClipData;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ActivityInfo;
import android.content.pm.PackageManager;
import android.database.Cursor;
import android.net.Uri;
import android.os.Bundle;
import android.os.ParcelFileDescriptor;
import android.provider.OpenableColumns;
import android.webkit.MimeTypeMap;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * GunimOpen hands Go the files another application gives this one, and
 * those the document picker gives. Each arrives as a content address.
 * It opens them and reads their names apart from the UI thread, as a
 * file in the cloud can take a while, and Go copies them from there.
 */
final class GunimOpen {
	// Each batch has a thread of its own, so a provider that never answers
	// holds up no later file.
	private static final ExecutorService worker = Executors.newCachedThreadPool();

	private GunimOpen() {
	}

	/**
	 * opened hands over the files in an intent that started the activity
	 * or came to it. Android starts a task from Recents with the intent
	 * that first started it, whose files the program had already.
	 */
	static void opened(GunimActivity a, Intent i) {
		if (i == null || (i.getFlags() & Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY) != 0) {
			return;
		}
		// Any application can send this activity an intent, so a program
		// that opens no files reads nothing of it, and a malformed one is
		// dropped: its extras can hold anything, or classes this program
		// lacks.
		String opens = opensOf(a);
		if (opens.isEmpty()) {
			return;
		}
		try {
			List<Uri> uris = sentIn(i);
			if (!uris.isEmpty()) {
				hand(a, 0, uris, opens);
			}
		} catch (RuntimeException e) {
			android.util.Log.w("gunim", "reading a file sent to this program: " + e);
		}
	}

	/** chosen hands over the files the document picker gave for code. */
	static void chosen(GunimActivity a, int code, Intent data) {
		hand(a, code, data == null ? new ArrayList<>() : chosenIn(data), "");
	}

	/** sentIn returns the addresses another application sent in i. */
	@SuppressWarnings("deprecation")
	private static List<Uri> sentIn(Intent i) {
		List<Uri> uris = new ArrayList<>();
		String action = i.getAction();
		if (Intent.ACTION_VIEW.equals(action) && i.getData() != null) {
			uris.add(i.getData());
		} else if (Intent.ACTION_SEND.equals(action)) {
			Uri u = i.getParcelableExtra(Intent.EXTRA_STREAM);
			if (u != null) {
				uris.add(u);
			}
		} else if (Intent.ACTION_SEND_MULTIPLE.equals(action)) {
			ArrayList<Uri> many = i.getParcelableArrayListExtra(Intent.EXTRA_STREAM);
			if (many != null) {
				for (Uri u : many) {
					if (u != null) {
						uris.add(u);
					}
				}
			}
		}
		return uris;
	}

	/** chosenIn returns the addresses the picker gave: a clip for several. */
	private static List<Uri> chosenIn(Intent data) {
		List<Uri> uris = new ArrayList<>();
		ClipData clip = data.getClipData();
		if (clip != null) {
			for (int n = 0; n < clip.getItemCount(); n++) {
				Uri u = clip.getItemAt(n).getUri();
				if (u != null) {
					uris.add(u);
				}
			}
		} else if (data.getData() != null) {
			uris.add(data.getData());
		}
		return uris;
	}

	/** opensOf returns the extensions gunimapk's -opens named. */
	private static String opensOf(GunimActivity a) {
		try {
			ActivityInfo info = a.getPackageManager().getActivityInfo(a.getComponentName(), PackageManager.GET_META_DATA);
			Bundle meta = info.metaData;
			String opens = meta == null ? null : meta.getString("gunim.opens");
			return opens == null ? "" : opens;
		} catch (PackageManager.NameNotFoundException e) {
			return "";
		}
	}

	/**
	 * hand opens uris and gives Go each one's file descriptor and name.
	 * An address that fails to open goes as -1, for Go to report. Only a
	 * content address from another application opens: a file address, or
	 * one of this program's own provider, would let a caller read the
	 * program's private files through it.
	 */
	private static void hand(Context c, int code, List<Uri> uris, String opens) {
		Context app = c.getApplicationContext();
		worker.execute(() -> {
			int[] fds = new int[uris.size()];
			StringBuilder names = new StringBuilder();
			for (int n = 0; n < uris.size(); n++) {
				Uri u = uris.get(n);
				if (n > 0) {
					names.append('\0');
				}
				names.append(nameOf(app, u).replace('\0', '_'));
				fds[n] = open(app, u);
			}
			Native.files(code, fds, names.toString(), opens);
		});
	}

	private static int open(Context app, Uri u) {
		if (!"content".equals(u.getScheme()) || GunimFiles.authority(app).equals(withoutUser(u.getAuthority()))) {
			return -1;
		}
		try {
			ParcelFileDescriptor p = app.getContentResolver().openFileDescriptor(u, "r");
			return p == null ? -1 : p.detachFd();
		} catch (Exception e) {
			// A provider may throw anything back through the binder, and
			// one file's failure is told as that file's.
			android.util.Log.w("gunim", "opening " + u + ": " + e);
			return -1;
		}
	}

	/** withoutUser returns an authority without the user, as 0@, Android also reads. */
	private static String withoutUser(String authority) {
		return authority == null ? null : authority.substring(authority.lastIndexOf('@') + 1);
	}

	/** nameOf asks the provider for a file's name, or takes the address's last part. */
	private static String nameOf(Context app, Uri u) {
		if ("content".equals(u.getScheme())) {
			try (Cursor c = app.getContentResolver().query(u, new String[] {OpenableColumns.DISPLAY_NAME}, null, null, null)) {
				if (c != null && c.moveToFirst() && !c.isNull(0)) {
					return c.getString(0);
				}
			} catch (Exception e) {
				android.util.Log.w("gunim", "naming " + u + ": " + e);
			}
		}
		String last = u.getLastPathSegment();
		return last == null ? "" : last;
	}

	/**
	 * typesOf returns the MIME types of patterns as *.ext, each ended by a
	 * NUL, or null to show every file: for no patterns, for a pattern of
	 * another form, and for an extension Android knows no type for.
	 */
	static String[] typesOf(String patterns) {
		List<String> types = new ArrayList<>();
		for (String p : patterns.split("\0")) {
			if (p.isEmpty()) {
				continue;
			}
			String type = p.startsWith("*.")
				? MimeTypeMap.getSingleton().getMimeTypeFromExtension(p.substring(2).toLowerCase(Locale.ROOT))
				: null;
			if (type == null) {
				return null;
			}
			if (!types.contains(type)) {
				types.add(type);
			}
		}
		return types.isEmpty() ? null : types.toArray(new String[0]);
	}
}
