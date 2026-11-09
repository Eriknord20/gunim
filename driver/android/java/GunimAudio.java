package gunim.android;

import android.content.Context;
import android.media.AudioAttributes;
import android.media.AudioDeviceCallback;
import android.media.AudioDeviceInfo;
import android.media.AudioFormat;
import android.media.AudioManager;
import android.media.AudioTimestamp;
import android.media.AudioTrack;
import android.os.Handler;
import android.os.Looper;
import java.lang.reflect.Method;
import java.util.HashSet;

/**
 * GunimAudio tells package audio/speaker of the device AAudio plays or
 * records on: its type, the delay Android's mixer reports for the output
 * it plays through, which on Bluetooth holds the headset's own, and which
 * device is the phone's own microphone. Before Android 11 it tells too
 * of devices coming and going, as AAudio may not.
 * Go calls it from a thread of its own, never AAudio's.
 */
final class GunimAudio {
	private GunimAudio() {}

	// known holds the IDs of the devices Android lists, once watch
	// began: Android tells of each device listed as the callback is
	// registered, and only those it has not listed count as new.
	private static HashSet<Integer> known;

	/** devicesChanged tells Go of a device coming or going, an output, an input, or both. */
	static native void devicesChanged(boolean out, boolean in);

	/**
	 * watch has Android tell Go of audio devices coming and going, on
	 * the main thread, from now on. It returns false where the
	 * application is not ready yet, and true once it watches.
	 */
	static synchronized boolean watch() {
		if (known != null) {
			return true;
		}
		Context app = Native.app;
		if (app == null) {
			return false;
		}
		AudioManager am = (AudioManager) app.getSystemService(Context.AUDIO_SERVICE);
		known = new HashSet<>();
		for (AudioDeviceInfo d : am.getDevices(AudioManager.GET_DEVICES_ALL)) {
			known.add(d.getId());
		}
		am.registerAudioDeviceCallback(new AudioDeviceCallback() {
			@Override
			public void onAudioDevicesAdded(AudioDeviceInfo[] devices) {
				changed(devices, true);
			}

			@Override
			public void onAudioDevicesRemoved(AudioDeviceInfo[] devices) {
				changed(devices, false);
			}
		}, new Handler(Looper.getMainLooper()));
		return true;
	}

	// changed tells Go of the devices added or removed that change what
	// Android lists.
	private static void changed(AudioDeviceInfo[] devices, boolean added) {
		boolean out = false, in = false;
		synchronized (GunimAudio.class) {
			for (AudioDeviceInfo d : devices) {
				if (added ? known.add(d.getId()) : known.remove(d.getId())) {
					out |= d.isSink();
					in |= d.isSource();
				}
			}
		}
		if (out || in) {
			devicesChanged(out, in);
		}
	}

	// outputLatency is AudioManager's hidden getOutputLatency, or null
	// where it is missing.
	private static Method outputLatency;
	private static boolean looked;

	/**
	 * route returns, for the device whose ID is device: its
	 * AudioDeviceInfo type, or 0 where none has that ID; the latency in
	 * milliseconds Android reports for its output of media, or -1 where
	 * it reports none; and the ID of the phone's own microphone, or 0.
	 * A phone may list a microphone for each place it has one. The one
	 * at the bottom, for the mouth, is the one to sing into.
	 */
	static int[] route(int device) {
		int[] r = {0, -1, 0};
		Context app = Native.app;
		if (app == null) {
			return r;
		}
		AudioManager am = (AudioManager) app.getSystemService(Context.AUDIO_SERVICE);
		for (AudioDeviceInfo d : am.getDevices(AudioManager.GET_DEVICES_ALL)) {
			if (d.getId() == device) {
				r[0] = d.getType();
			}
			if (d.isSource() && d.getType() == AudioDeviceInfo.TYPE_BUILTIN_MIC
				&& (r[2] == 0 || "bottom".equals(d.getAddress()))) {
				r[2] = d.getId();
			}
		}
		r[1] = latency(am);
		return r;
	}

	// latency calls AudioManager.getOutputLatency(STREAM_MUSIC), hidden
	// but open to applications, as ExoPlayer calls AudioTrack's
	// getLatency. It is AudioFlinger's latency of the output media plays
	// through: the HAL's, which on Bluetooth counts the headset's delay.
	// It returns -1 where the call fails.
	private static synchronized int latency(AudioManager am) {
		if (!looked) {
			looked = true;
			try {
				outputLatency = AudioManager.class.getMethod("getOutputLatency", int.class);
			} catch (Exception e) {
				outputLatency = null;
			}
		}
		if (outputLatency == null) {
			return -1;
		}
		try {
			return (Integer) outputLatency.invoke(am, AudioManager.STREAM_MUSIC);
		} catch (Exception e) {
			// As where the audio service failed a moment: the next call
			// may answer.
			return -1;
		}
	}

	/**
	 * probe plays a second of silence through an AudioTrack, for the
	 * speaker's log, to compare with what AAudio reports. It returns how
	 * far behind the frames written the AudioTrack's public timestamp
	 * places the device, in microseconds, or -1; the hidden
	 * AudioTrack.getLatency in milliseconds, or -1; and the track's
	 * buffer in frames.
	 */
	static int[] probe(int usage) {
		int[] r = {-1, -1, 0};
		int rate = 48000;
		AudioTrack t;
		try {
			t = new AudioTrack.Builder()
				.setAudioAttributes(new AudioAttributes.Builder().setUsage(usage)
					.setContentType(AudioAttributes.CONTENT_TYPE_MUSIC).build())
				.setAudioFormat(new AudioFormat.Builder().setEncoding(AudioFormat.ENCODING_PCM_FLOAT)
					.setSampleRate(rate).setChannelMask(AudioFormat.CHANNEL_OUT_STEREO).build())
				.setTransferMode(AudioTrack.MODE_STREAM)
				.build();
		} catch (Exception e) {
			return r;
		}
		try {
			r[2] = t.getBufferSizeInFrames();
			float[] silence = new float[2 * 960];
			long written = 0;
			t.play();
			AudioTimestamp ts = new AudioTimestamp();
			long until = System.nanoTime() + 1_000_000_000L;
			while (System.nanoTime() < until) {
				int n = t.write(silence, 0, silence.length, AudioTrack.WRITE_BLOCKING);
				if (n < 0) {
					return r;
				}
				written += n / 2;
			}
			if (t.getTimestamp(ts)) {
				long behind = (written - ts.framePosition) * 1_000_000L / rate
					- (System.nanoTime() - ts.nanoTime) / 1000;
				r[0] = (int) behind;
			}
			try {
				r[1] = (Integer) AudioTrack.class.getMethod("getLatency").invoke(t);
			} catch (Exception e) {
				r[1] = -1;
			}
		} finally {
			t.release();
		}
		return r;
	}
}
