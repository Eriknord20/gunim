package gunim.android;

import android.content.Context;
import android.media.AudioAttributes;
import android.media.AudioDeviceInfo;
import android.media.AudioFormat;
import android.media.AudioManager;
import android.media.AudioTimestamp;
import android.media.AudioTrack;
import java.lang.reflect.Method;

/**
 * GunimAudio tells package audio/speaker of the output device AAudio
 * plays on: its type, and the delay Android's mixer reports for the
 * output it plays through, which on Bluetooth holds the headset's own.
 * Go calls it from a thread of its own, never AAudio's.
 */
final class GunimAudio {
	private GunimAudio() {}

	// outputLatency is AudioManager's hidden getOutputLatency, or null
	// where it is missing.
	private static Method outputLatency;
	private static boolean looked;

	/**
	 * route returns, for the output device whose ID is device: its
	 * AudioDeviceInfo type, or 0 where none has that ID; 1 where it is
	 * Bluetooth, else 0; and the latency in milliseconds Android reports
	 * for its output of media, or -1 where it reports none.
	 */
	static int[] route(int device) {
		int[] r = {0, 0, -1};
		Context app = Native.app;
		if (app == null) {
			return r;
		}
		AudioManager am = (AudioManager) app.getSystemService(Context.AUDIO_SERVICE);
		for (AudioDeviceInfo d : am.getDevices(AudioManager.GET_DEVICES_OUTPUTS)) {
			if (d.getId() == device) {
				r[0] = d.getType();
				r[1] = bluetooth(d.getType()) ? 1 : 0;
			}
		}
		r[2] = latency(am);
		return r;
	}

	static boolean bluetooth(int type) {
		switch (type) {
		case AudioDeviceInfo.TYPE_BLUETOOTH_A2DP:
		case AudioDeviceInfo.TYPE_BLUETOOTH_SCO:
		case AudioDeviceInfo.TYPE_HEARING_AID:
		case 26: // TYPE_BLE_HEADSET, Android 12
		case 27: // TYPE_BLE_SPEAKER, Android 12
		case 30: // TYPE_BLE_BROADCAST, Android 13
			return true;
		}
		return false;
	}

	// latency calls AudioManager.getOutputLatency(STREAM_MUSIC), hidden
	// but open to applications, as ExoPlayer calls AudioTrack's
	// getLatency. It is AudioFlinger's latency of the output media plays
	// through: the HAL's, which on Bluetooth counts the headset's delay.
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
			outputLatency = null;
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
