# Plan: `driver/kms`, full-screen touch on a Raspberry Pi

The owner answered the open questions on 2026-11-13 (section 7), and the plan below follows those answers. K1 is
the first code.

**The point.** gunim gets a new driver, `driver/kms`. It draws one full-screen UI straight to a Raspberry Pi 5's
screen, with no X or Wayland, and takes touch from the kernel. The first user is Hjortron's touch console (M3 in
`marrasen/hjortron`, `docs/poc-plan.md`). It is built so any Pi project with the Touch Display 2 can use it with a
few lines.

**The short version.**

- Draw on the GPU, with gunim's own renderer, unchanged. The display path is DRM/KMS with atomic commits, GBM
  buffers and an EGL context for OpenGL ES 3.1. The libraries load at run time, as gunim already does on Linux, so
  the build stays `CGO_ENABLED=0`.
- Read touch straight from `/dev/input/event*` in pure Go. Reuse the Android driver's gesture code, so taps, scrolls,
  flings, pinches and long presses behave the same.
- Pace frames by the kernel's page-flip events: 60 frames a second on this panel, and none while nothing moves.
- For Hjortron, split the console in two processes. A UI process with no privileges draws and reads touch. A root
  process does the work and checks the PIN. They talk over one Unix socket with a small, fixed set of requests.
- The first thing to try on the trial Pi is a demo: an animated test pattern with a dot under each finger, and the
  frame rate and touch latency on screen.

Words used below, once:

- **DRM/KMS**: the Linux kernel's interface to graphics hardware. DRM is the whole interface; KMS (kernel mode
  setting) is the part that sets the screen mode and shows a buffer on it.
- **Atomic commit**: one request to the kernel that changes several display settings at once and either all apply
  or none do.
- **Page flip**: swapping the buffer the screen shows for a new one, at the moment between two refreshes.
- **vblank** (vertical blank): that moment between two refreshes.
- **GBM** (Generic Buffer Management): Mesa's library that allocates buffers the GPU draws into and KMS can show.
- **EGL**: the Khronos interface that makes an OpenGL ES context on top of GBM.
- **evdev**: the kernel's input event interface, one file per device under `/dev/input`.
- **Mesa**: the open-source OpenGL and Vulkan drivers. On the Pi 5 the GPU driver in Mesa is called V3D.

---

## 0. What exists today

### In gunim

Read for this plan: `README.md`, `WIDGETS.md`, `CLAUDE.md`, `driver/driver.go`, `driver/offscreen.go`,
`driver/android`, `driver/internal/compose`, `driver/internal/render`, `internal/gl`, `anim`, `input`, `touch.go`.

- **The driver seam** is `driver.Driver` (`Run`, `NewWindow`, `Monitors`) and `driver.Window` (`Present`,
  `Presented`, `Input`, `Size`, `Scale`, `RefreshRate`, `Clipboard`, `Close`). Extra powers are optional interfaces,
  such as `driver.Awaker`, `driver.FingerTaker` and `driver.KeyboardCoverer`.
- **Pacing.** The engine keeps one frame in flight. It draws, calls `Present`, and waits for the matching
  `driver.Frame` on `Presented`. `Frame.Shown` is when the frame went out and `Frame.Lag` how long until it lights
  the screen. So whatever reports `Presented` sets the frame rate.
- **The renderer** (`driver/internal/render`) already has an OpenGL ES 3.0 path (`#version 300 es`), used on Android
  and in the browser. Every shape is one quad with a signed distance field, so it is cheap on a small GPU.
- **`driver/internal/compose`** lays several gunim windows on one surface, for drivers with no window system. The
  first window fills the surface and popups lie over it. Android and the web driver use it. `driver/kms` is the third
  such driver.
- **Touch semantics live in the engine** (`touch.go`): slop, long press, fling, scroll versus drag, pinch. The
  Android driver only turns raw touches into `input.PointerDown`/`PointerMove`/`PointerUp` with `Touch: true`,
  `input.Pinch` and `input.Finger` (`driver/android/gesture.go`).
- **`internal/gl`** loads `libGLESv2.so.2` through purego when there is no window system
  (`internal/gl/procaddr_linbsd.go`). Its own comment names the Raspberry Pi as the reason.
- **Motion.** `anim` has springs (`Snappy`, `Gentle`, `Bouncy`, used as the theme's `Quick`, `Settle` and `Bounce`),
  tweens with `EaseOut` and `EaseInOut`, `Decay` and `Fling` for coasting, `Trail` for a finger's speed, `Moves` for
  steady-subtree keys, and a global time scale. `widget` has `Pages`, `Hero`, `Dialog`, `Toast`, `LiveGraph`,
  `Segmented`, `Switch` and keyed lists that animate rows in and out.

### In Hjortron

Read: `docs/poc-plan.md` (M3), `docs/design.md` §9 and §11, `docs/install-pi.md` "7. Harden the Pi" and
"Permissions", and `internal/console`.

- **Today's console** is `hjortron console`, a line prompt. It is the login shell of the user `hjortron-console`,
  and runs as root through one `sudo` rule. Every command that changes something asks for the PIN first (P14). A
  wrong PIN waits 3 seconds. The PIN is kept as a PBKDF2 hash in `/var/lib/hjortron/console/pin`.
- **Its known limit** (install-pi, "The limit of the PIN"): anyone with a shell at the console can get round the PIN.
  M3 removes the shell.
- **Design §9** already sketches this driver: DRM/KMS, GBM and EGL for OpenGL ES, evdev in pure Go, Mesa's libraries
  loaded at run time, `PrivateNetwork=yes`, the PIN before every change, and a screen that blanks and wakes on touch.
- **Design §11** wants the front, the store and the console as three separate users, each in its own sandbox, and
  "a single static Go binary" whose only runtime libraries are Mesa's, for the console.
- **The trial Pi** runs Raspberry Pi OS Lite on Debian 13, with kernel `6.18.50+rpt-rpi-2712`, and Mesa 26.2.6 from
  Raspberry Pi's archive (`libgbm1`, `libgl1-mesa-dri`). It does not have Mesa's EGL and OpenGL ES libraries
  (`libegl1`, `libegl-mesa0`, `libgles2`), so Hjortron's install adds them (section 5.2).

### The platform, from primary sources

**The Pi 5's display and GPU.**

- Display and GPU are separate kernel drivers. Mesa's V3D page says: "the kernel uses the VC4 DRM driver for display
  support", while rendering goes through the V3D kernel driver. Mesa's `kmsro` helpers share buffers between the
  two. [Mesa V3D]
- **On the trial Pi the DSI panel is not on vc4.** It is on the RP1 chip's DSI controller, with a driver of its own,
  `drm-rp1-dsi`. So there are three cards: `card0` is `drm-rp1-dsi` with the connector `DSI-2`, `card1` is v3d, and
  `card2` is `vc4-drm` with the two HDMI ports. The render node is v3d's `/dev/dri/renderD128`. Mesa has a `kmsro`
  entry for it (`drm-rp1-dsi_dri.so`). The card numbers are not fixed, so the driver finds the screen by a connected
  connector on a card that can set modes, not by a driver's name.
- `drm-rp1-dsi` is a `drm_simple_display_pipe`: one plane, linear buffers in contiguous memory, formats such as
  `XRGB8888` and `RGB565`, and no rotation property (`drivers/gpu/drm/rp1/rp1-dsi/rp1_dsi.c` on `rpi-6.18.y`).
- V3D is a "conformant GLES3.1 driver" on V3D 7.1, the Pi 5's GPU (VideoCore VII). [Mesa V3D] It was conformant
  for OpenGL ES 3.1 and Vulkan 1.2 at the Pi 5's launch [Igalia 2023], and Vulkan 1.3 since 2024 [Igalia XDC 2024].
  gunim's renderer needs only OpenGL ES 3.0, so it has room to spare.
- Only one process at a time is "DRM master", the one allowed to set modes. A process becomes master by opening the
  primary node "when no one else is the current master". Taking master from another process needs
  `CAP_SYS_ADMIN`. A render node needs no master and gives "immediately granted GPU access". [DRM uAPI]
- An atomic commit can ask for an event when it completes. The events "are per-CRTC" and carry a timestamp. A test
  commit checks a setting without applying it. The CRTC's `ACTIVE` property is "the simplified implementation for
  DPMS", which is how the screen turns off. [KMS]
- **Rotation.** vc4's planes support only `DRM_MODE_ROTATE_0`, `DRM_MODE_ROTATE_180`, `REFLECT_X` and `REFLECT_Y`
  (`drm_plane_create_rotation_property` in `drivers/gpu/drm/vc4/vc4_plane.c`, checked on the `rpi-6.18.y` branch).
  `drm-rp1-dsi` has no rotation at all. So a landscape UI on this portrait panel has to be drawn turned by gunim. The
  display hardware cannot do 90°.

**The Touch Display 2.** [Pi Touch Display 2]

- 720 × 1280 pixels on the 5" and 7" models, and 1200 × 1920 on the 10" model. Portrait is its natural way up.
- The 7" active area is 87 × 154.5 mm, about 210 pixels per inch. The 5" is 62 × 110.5 mm, about 295 pixels per
  inch.
- It runs at 60 Hz (`video=DSI-1:720x1280@60` in the docs). Video and touch come over the DSI ribbon. Power comes
  from the GPIO header. On the trial Pi the mode is 720 × 1280 with an 83 333 kHz clock, 937 by 1 482 in total:
  60.01 Hz.
- **The trial Pi has one fitted**: the 5" on `card0-DSI-2`, found by `display_auto_detect=1` with no `dtoverlay`
  line in `config.txt`. The firmware loads `raspberrypi,dsi-5inch`, and the connector reports 62 × 110 mm. Its touch shows up as `11-005d Goodix Capacitive TouchScreen`, and its backlight as
  `/sys/class/backlight/panel_backlight@1`, at 15 of 31.
- The kernel's `rotate=` on the command line "only affects the text-mode console". It does nothing for a KMS program.
- The device tree overlay is `vc4-kms-dsi-ili9881-7inch` (or `-5inch`), with `rotation`, `invx`, `invy`, `swapxy`,
  `sizex`, `sizey` and `disable_touch`. [Pi overlays]
- **Touch** is a Goodix GT911 on I²C at `0x5d` (the overlay's source, `vc4-kms-dsi-ili9881-7inch-overlay.dts`).
  The overlay gives it no interrupt line, so the kernel's `goodix` driver polls it every 17 ms
  (`GOODIX_POLL_INTERVAL_MS`). It reports the kernel's multi-touch protocol B, with slots, `INPUT_MT_DIRECT` and
  `INPUT_PROP_DIRECT` (`drivers/input/touchscreen/goodix.c`). The Pi's docs say five fingers on the 5" and 7".
  [MT protocol]
- **Brightness** goes through the panel's microcontroller. On `rpi-6.18.y` the overlay makes it a `pwm-backlight`
  with `brightness-levels = <0 31>`, so 32 steps, defaulting to 15. It shows up under `/sys/class/backlight`.
  [sysfs backlight]

**Go without cgo.**

- DRM ioctls, evdev reads, the backlight's sysfs files and the terminal ioctls are all plain system calls. They work
  in pure Go with `golang.org/x/sys/unix`. No C is needed for them.
- GBM, EGL and GLES are C libraries with no system-call interface. gunim reaches them through purego
  (`github.com/ebitengine/purego`, already a dependency), which loads them at run time with `CGO_ENABLED=0` on
  Linux arm64. That is how the X11 driver already reaches `libGL` and `libEGL`. [purego]
- The price: a binary that uses purego is dynamically linked. It needs the system's loader and `libc` at run time,
  even though it builds without a C compiler. That matters for Hjortron, whose server binary is meant to stay
  static (section 4).
- cgo is not needed anywhere. It would be needed only to use `libinput` or `libdrm` as C libraries, and this plan
  uses neither.

**What others do on KMS.** Studied for their approach only. No code is copied.

- `kmscube` (Mesa): the reference for GBM, EGL and atomic page flips in about a thousand lines of C. The shape this
  plan follows: lock the front buffer after `eglSwapBuffers`, add it as a framebuffer, commit it, and release the
  one before when the flip event comes.
- `flutter-pi`: Flutter on a Pi with DRM, GBM and EGL, and `libinput` for touch. It shows that a smooth touch UI on
  the Pi needs no compositor.
- Qt's EGLFS with its KMS backend, and SDL's `kmsdrm` backend: both run one full-screen surface, take DRM master
  directly, and handle the virtual terminal (VT) so the text console doesn't draw over them.
- LVGL's DRM driver: draws on the CPU into dumb buffers. It shows the CPU path works, for a simple look.
- In Go: `NeowayLabs/drm` does DRM ioctls in pure Go, and `holoplot/go-evdev` reads evdev in pure Go. Both confirm
  that the kernel side needs no cgo. gunim writes its own small versions, as it needs only a dozen ioctls each.

---

## 1. Goals in numbers

All figures are for a Pi 5 (4 GB) with the 5" Touch Display 2 at 60 Hz, running the heaviest Hjortron screen while it
animates, unless a line says otherwise. There are no power goals: there is no USB-C power meter (decision 11), so CPU
use and frames at rest stand in for power.

| Goal | Target | How it is measured |
|---|---|---|
| Frame rate while something moves | 60 fps; at most 1 missed flip in 1 000 frames | flip events counted against vblanks, in the driver |
| Frame time, layout + paint + draw | p50 ≤ 4 ms, p99 ≤ 8 ms | driver timeline, frame start to commit |
| Touch to flip | p50 ≤ 20 ms, p99 ≤ 30 ms | evdev timestamp to the flip event of the first frame that shows the touch |
| Touch to light (input-to-photon) | p50 ≤ 50 ms, p95 ≤ 70 ms | slow-motion video, once per milestone |
| Start to first frame | ≤ 1.0 s warm, ≤ 2.0 s straight after boot | process start to the first flip event |
| Boot to a touchable console | ≤ 20 s | `systemd-analyze` plus the first flip |
| Memory, Hjortron UI process | RSS ≤ 120 MB; no growth over a 1-hour run | `/proc/self/status`, heap after GC |
| Garbage per frame in the driver | 0 allocations | `testing.AllocsPerRun` |
| CPU at rest, screen on | ≤ 0.5 % of one core, 0 frames drawn | `/proc/self/stat` over 60 s |
| CPU while animating | recorded in each PR, no target yet | `/proc/self/stat` over 60 s |
| Screen blanked | backlight 0, CRTC off, 0 frames | the driver's state |
| The store's speed while the UI animates | at most 3 % slower | Hjortron's upload measurement with and without the demo running |

**Why these numbers.**

- The panel runs at 60 Hz, so a frame has 16.7 ms. gunim's own rule (`CLAUDE.md`) is 8 ms, for 120 Hz screens.
  Keeping to 8 ms here leaves half the frame spare for the GPU and for the store running beside it.
- The touch chip is polled every 17 ms, so a touch waits 0 to 17 ms before the kernel even sees it. Scan-out takes
  up to another 16.7 ms, and the LCD needs some milliseconds to change. So input-to-photon cannot get much under
  40 ms on this hardware. The goal is what the hardware allows, not a phone's figure.

**How each one is checked, and fails when it regresses.**

- **A timeline in the driver.** For every frame it keeps: frame start, ops handed over, swap done, commit sent,
  flip event time, and the evdev time of the oldest touch the frame answers. Touch times use `CLOCK_MONOTONIC`
  (set with `EVIOCSCLOCKID`), and so do flip events (`DRM_CAP_TIMESTAMP_MONOTONIC`), so the two compare directly.
  `GUNIM_DEBUG_FRAMES=1` adds p50 and p99 and missed flips to its line each second. `GUNIM_KMS_TRACE=file` writes
  the whole timeline as JSON.
- **`tools/kmsbench`, run on the Pi.** It opens a screen (the demo, or a Hjortron screen with `-screen status`),
  plays back a recorded touch script for 60 s, and checks the goals in a table kept in the repo. It exits non-zero
  if any is missed and prints which. The touch script plays through the driver's own input path from a recorded
  file, so it needs no `uinput` and no root.
- **The same run as a Go test**: `go test -tags kmspi ./driver/kms/...` on the Pi. It skips with a message where
  there is no screen to take. No CI machine has a Pi, so an agent runs it on the trial Pi over SSH at each
  milestone, and the figures go in that milestone's PR (decision 10).
- **Tests that run everywhere, in CI.** These need no Pi:
  - evdev decoding from byte streams recorded once on the trial Pi and checked in: taps, five fingers, a palm, and a
    `SYN_DROPPED` (events lost to a full buffer) with the resync that follows;
  - the touch resampler (section 2.5), against recorded streams, frame by frame;
  - turning touch points and screen space for each rotation;
  - the atomic request builder, against a fake ioctl layer that checks properties and flags;
  - the blank and wake state machine, with a fake clock;
  - allocations per frame in the driver loop: 0.
- **A virtual display.** The kernel's `vkms` module is a virtual KMS device with atomic commits and vblank
  events. With Mesa's software renderer on GBM, it runs the whole driver on a Linux CI machine. GitHub's
  `ubuntu-latest` runners load it (K2 tried it): CI's vkms job runs the bench there, from the modeset to blanking
  and the swallowed wake. [vkms]
- **Input-to-photon** needs a camera. Film the demo's touch dot and a finger at 240 fps with a phone, and count
  frames. It is done by hand, once per milestone, and the figures go in the milestone's PR.

---

## 2. Architecture

### 2.1 Where it sits

```
gunim engine (unchanged)  ── driver.Driver / driver.Window ──  driver/kms
                                                                 ├─ drm.go        modes, atomic commits, flip events (x/sys/unix)
                                                                 ├─ gbm.go, egl.go  buffers and the GL context (purego)
                                                                 ├─ evdev.go      touch, keys, hot-plug (x/sys/unix)
                                                                 ├─ vt.go         the virtual terminal: graphics mode, switching
                                                                 ├─ backlight.go  brightness and blanking (sysfs)
                                                                 └─ uses driver/internal/compose, render, touch
```

- `driver/kms` builds only for Linux (`//go:build linux`). It implements `driver.Driver` and `driver.Window`, and the
  optional interfaces it can honour: `Awaker` (keep the screen on), `FingerTaker`, `KeyboardCoverer` (for the
  on-screen keyboard), `SafeAreaer` (none), `Shooter` (screenshots, for bug reports).
- **Only 64-bit Linux** (`linux && (arm64 || amd64)`): `gbm_bo_get_handle` returns an 8-byte union, which comes back
  in a register there, and through a hidden pointer on 32-bit ARM and 386, which purego cannot read. So a Pi 4 or
  5 needs a 64-bit system. Elsewhere the `gunimkms` tag gives the desktop driver.
- **Choosing it.** `gunim.Main` picks a driver by build tags (`open_desktop.go`, `open_android.go`). Two ways in:
  - a build tag, `gunimkms`: `open_kms.go` makes `gunim.Main` open `driver/kms` with default options. The desktop
    driver is then not linked in, which keeps the binary smaller.
  - `kms.Open(kms.Options{...})` and then `gunim.MainWith(ctx, drv, fn)`, for a program that sets options.
    `MainWith` is today's `runApp`, exported. A `kms.Main` would import gunim, which imports `driver/kms` under the
    `gunimkms` tag: an import cycle, so there is none.
- **Windows.** As on Android, the first window fills the screen and popups lie over it, through `compose`. A
  menu, a dropdown and a dialog work unchanged.
- **Threads.** One render goroutine, locked to its OS thread, owns the EGL context and the DRM file. It draws,
  commits, and waits in `poll` on the DRM file for the flip event and on an `eventfd` for new work. Each input device
  has a goroutine that blocks in `read`. `Run` waits on the context and on signals. KMS has no main-thread rule, but
  `Run` keeps the interface's contract.

### 2.2 The render path: GPU, with the CPU as a fallback

| | GPU: GBM + EGL + GLES 3.1 | CPU: dumb buffers, pure Go |
|---|---|---|
| Renderer | gunim's own, unchanged: same look as on every other platform | a new rasterizer for gunim's SDF quads, gradients, blurs, text atlas, layers and 3D scenes |
| Speed | a few ms a frame for this UI | blurs and large fades too slow for 60 fps at 720 × 1280 on four A76 cores |
| Build | `CGO_ENABLED=0`, but dynamically linked; needs Mesa installed | static, no libraries |
| Attack surface | Mesa and the v3d kernel driver | the vc4 kernel driver only |
| Work | small: the renderer and `compose` exist | months |

**Recommendation: the GPU.** The renderer already exists and already runs on OpenGL ES. Writing a second renderer
would cost more than the whole driver.

**With no GPU.** If EGL or GBM fails on the hardware GPU, the driver tries Mesa's software renderer through the same
path, in the same process. `GBM_ALWAYS_SOFTWARE=1` gives Mesa's `kms_swrast` and llvmpipe on GBM, as K1 found on the
Pi. `LIBGL_ALWAYS_SOFTWARE=1` is not used: with it, Mesa 26.2's swap on GBM jumps to a null function. Where Mesa's
own loader cannot load the GPU's driver, it falls back to llvmpipe by itself; the driver reads GL's renderer name,
so `Software()` says so then too. The program looks the same and runs slower: 18 fps on the trial Pi, at 350 % of
one core.
The driver logs why, and sets a flag the app can read (`kms.Window.Software()`), so an app can show a small notice or
drop blurs. Hjortron's console shows a small notice that it draws without the GPU (decision 8). If that fails too, `kms.Open` returns the error and the program stops. systemd then shows it in the journal.
The driver never shows a blank screen while claiming to run.

### 2.3 Buffers, commits and vsync

1. **Find the display.** Open each `/dev/dri/card*` and keep the cards that can set modes (they have connectors;
   v3d has none). Of their connected connectors, the default prefers DSI, then the first connected HDMI.
   `kms.Options.Connector` can name one (`"DSI-2"`, `"HDMI-A-1"`). On the trial Pi that is `drm-rp1-dsi` on
   `card0`, and on a Pi with its panel on vc4 it is vc4.
2. **Take it.** Turn on `DRM_CLIENT_CAP_UNIVERSAL_PLANES` and `DRM_CLIENT_CAP_ATOMIC`. Read the connector's preferred
   mode, its CRTC and its primary plane. Opening the card first makes the process master with no capability.
3. **Make the context.** `gbm_create_device` on the card. `eglGetPlatformDisplay(EGL_PLATFORM_GBM_KHR, ...)`
   [EGL GBM], an ES 3 context, and a `gbm_surface` of the mode's size in `XRGB8888`, with
   `GBM_BO_USE_SCANOUT | GBM_BO_USE_RENDERING`. `drm-rp1-dsi` lists no format modifiers, so its buffers are linear,
   in memory the display can reach. Reading the plane's `IN_FORMATS`, and asking GBM for the modifiers it lists, is
   deferred: it matters for vc4's HDMI planes, which can scan out tiled buffers, and not for the trial Pi's panel.
4. **Each frame.** `compose` draws into the surface. Then `eglSwapBuffers`, `gbm_surface_lock_front_buffer`, and
   `DRM_IOCTL_MODE_ADDFB2` for that buffer, kept in a small cache keyed by buffer so each buffer is added once.
5. **Commit.** One atomic commit sets `FB_ID` on the primary plane, with `DRM_MODE_PAGE_FLIP_EVENT` and
   `DRM_MODE_ATOMIC_NONBLOCK`. The first commit also sets the mode, `CRTC_ID` and `ACTIVE`, with
   `DRM_MODE_ATOMIC_ALLOW_MODESET`, after a test commit with `DRM_MODE_ATOMIC_TEST_ONLY`.
6. **The flip event.** When it comes, release the buffer shown before, and send `driver.Frame` on `Presented`, a
   lead before the next vblank, not at once, so the next frame reads touches as late as it can and still makes that
   vblank. The driver learns the lead from the frames: it shortens while frames make their vblank and lengthens at a
   miss. The GPU's work per frame sets how late a frame can start:
   - `Shown` is the event's timestamp, the vblank the frame went out on;
   - `Lag` is half a refresh (scan-out reaching the middle of the panel) plus an estimate of the LCD's response, kept
     as one constant that the slow-motion video checks.
7. **Rate.** `RefreshRate` comes from the mode: `clock × 1000 / (htotal × vtotal)`. It is not taken as exactly 60.

**One frame in flight.** That matches the engine: it waits for `Presented` before it draws again. The surface has
three buffers, so the GPU never waits for one to come free.

**GPU fences, later.** On the first version the commit waits for the GPU through the kernel's implicit sync. If the
timeline shows the CPU waiting, the driver moves to an explicit fence: `EGL_ANDROID_native_fence_sync` gives a sync
file, and the plane's `IN_FENCE_FD` property takes it. That is an optimisation for K2, only if measured.

**Damage.** gunim already redraws only what changed, into the canvas `compose` keeps. The DSI panel refreshes in
full on every vblank, so `FB_DAMAGE_CLIPS` gains nothing here, and the driver leaves it out.

### 2.4 Input: evdev, multi-touch and gestures

- **Finding devices.** Scan `/dev/input/event*`. A touch screen has `INPUT_PROP_DIRECT` and `ABS_MT_SLOT`. A keyboard
  has `EV_KEY` with letter keys. A mouse has `EV_REL`. Watch `/dev/input` with inotify for devices that come and go.
  `kms.Options.Touch` can name a device by path or name, for a screen with two.
- **Reading touch.** Protocol B: `ABS_MT_SLOT` picks a finger's slot, `ABS_MT_TRACKING_ID` starts it (or ends it at
  `-1`), `ABS_MT_POSITION_X/Y` move it, and `SYN_REPORT` closes one report. After `SYN_DROPPED`, read the state again
  with `EVIOCGMTSLOTS` and lift any finger that is gone, so no node waits on a lost lift. [MT protocol]
- **Grab.** `EVIOCGRAB` on each device the driver uses, so nothing else on the system reads the same touches.
- **Into gunim.** Move the Android driver's gesture code (`driver/android/gesture.go`) into a shared
  `driver/internal/touch` package that both drivers use. The first finger drives the pointer as a touch, a second
  starts a pinch, and `TakeFingers` gives each finger its own `input.Finger`. The engine already makes taps,
  scrolls, flings, long presses and pinches from those. So behaviour is the same as on a phone by construction.
- **Momentum scrolling** is the engine's own fling (`touch.go`, `anim.Fling`). Its speed comes from `anim.Trail`.
  With touch arriving every 17 ms, the trail needs the resampled points below. Raw points would give jittery
  speeds.
- **Coordinates.** The GT911 reports 0 to 719 by 0 to 1279. The driver maps them to the screen through the
  rotation, and through `invx`, `invy` and `swapxy` if the overlay set them. Both come from the device's own
  `EVIOCGABS` ranges, so a 10" panel at 1200 × 1920 maps with no change.
- **Keyboards and mice.** For other projects, and for a repair at the Pi. v1 maps a USB keyboard's key codes to
  `input.Key` on a US layout, and a mouse to the pointer with a cursor drawn by `compose`. Other layouts need XKB
  keymaps, which is later work.

### 2.5 Smooth touch: resampling at frame time

**The problem.** The GT911 is polled every 17 ms and the screen refreshes every 16.7 ms. The two clocks drift past
each other. So some frames get two touch reports and some get none. A dragged list then moves in uneven steps: a
judder that people see at once.

**The fix,** as phones do it. Before each frame the driver works out where the finger was at one fixed time:
the frame's time less about 5 ms, or less the report gap less 8 ms on a screen that reports slower, so a move
is never carried on further than 8 ms: 9 ms on the Touch Display 2. It interpolates between the two reports either side of that time. If the newest
report is older than that, it extrapolates from the last two by at most 8 ms. Each frame then gets exactly one move,
at an even step. Down and up events pass through at once, untouched, so a tap is never delayed.

It is tested on recorded streams: a steady drag must give moves whose step varies by under 10 %, where raw reports
vary by 100 %.

### 2.6 On-screen keyboard and PIN pad

- **A PIN pad** is a widget, not a driver feature (section 3.3). Hjortron's every change needs it.
- **An on-screen keyboard** is for typing names, such as a device's or a guest's. It also lives in `widget`. The
  driver implements `driver.TextInputter` and `driver.KeyboardCoverer` as Android does, so `TextField` works as it
  does on a phone. A tap on a field slides the keyboard up. `Frame.Keyboard` says how far it reaches, and the
  engine slides the window to keep the field in view. All of this exists for Android already.

### 2.7 Rotation

- `kms.Options.Rotate` takes 0, 90, 180 or 270, and Hjortron uses 90 (decision 1).
- 180 can use the plane's rotation property where there is one, which costs nothing. `drm-rp1-dsi` has none, so there
  it is drawn turned like 90 and 270.
- 90 and 270 are drawn turned. gunim lays the window out at the turned size (1280 × 720 for landscape). `compose`
  draws it into its texture as usual, and its last pass lays that texture on the screen with a turned quad. That
  pass is one full-screen copy, under half a millisecond on V3D.
- Touch points and `Monitor.Bounds` turn the same way, so the app never sees the panel's own way up.
- Text stays greyscale. A turned panel's subpixels run the wrong way for subpixel text. gunim already draws
  greyscale on Linux.
- Rotation is fixed at start. A Pi on a shelf does not turn, and there is no sensor to ask.

### 2.8 Density and scale

- The connector reports the panel's size in millimetres. The driver works out pixels per inch and sets the scale to
  the nearest quarter of `ppi / 160`, as Android does. That gives **1.75** on the 5" Hjortron uses (about 731 × 411
  logical pixels in landscape), and **1.25** on the 7" (1024 × 576).
- `kms.Options.Scale` overrides it. An app that wants bigger targets and text on the 5" can set 2 (640 × 360).

### 2.9 Brightness and blanking

- **Brightness.** `kms.Window.SetBrightness(0..1)` writes `/sys/class/backlight/<dev>/brightness`. With only 32
  steps a fade would show its steps. So a change animates on a spring, and the driver uses both:
  - the backlight takes the nearest step at or above the value;
  - a black overlay in the final `compose` pass makes up the rest.
  The fade is smooth even though the hardware is not.
- **Blanking**, set by `kms.Options.Blank` (a dim time and an off time). Hjortron dims after 1 minute and turns off
  after 5 (decision 6):
  1. after the dim time, fade to 30 % of the brightness over 1 s;
  2. after the off time, fade to black over 600 ms, set the backlight to 0, then commit `ACTIVE = 0`. The panel and
     the CRTC are off. No frames run and no GPU work happens.
- **Waking.** The first touch on a blank or dim screen wakes it and is swallowed. It never reaches a node. This
  matters for Hjortron: a tap on a dark screen must never press "Revoke". The driver commits `ACTIVE = 1`, draws the
  latest state, and fades up.
- **The shade** is worked out in light, through the panel's gamma, not in pixel values.
- **Another VT** shares the backlight: on a switch away the driver puts it back at the app's brightness, and
  touches reach no window until the VT shows again.
- **The app's say.** `Awaker.KeepAwake(true)` holds the screen on, as while a long command runs. The app hears
  `input.WindowHidden` and `input.WindowShown` as the screen goes off and on, as on other platforms.
- **Errors.** A failed write to the backlight's file is an I/O error. `SetBrightness` returns it, and a failed write
  while blanking reaches the app as an error on the window's error channel. The driver does not log it and carry on.

### 2.10 The virtual terminal

The text console (`fbcon`) draws on the same screen. Left alone it would draw a cursor and kernel messages over the
UI. The driver:

- runs on its own VT, given by the systemd unit with `TTYPath=` (section 4.2);
- sets it to graphics mode (`KDSETMODE KD_GRAPHICS`), so fbcon stops drawing on it;
- turns off the VT's keyboard handling (`KDSKBMODE K_OFF`), so keys don't reach a login behind it;
- with `kms.Options.VTSwitch`, takes Ctrl+Alt+F1 to F12 itself and switches VT (`VT_ACTIVATE`). It lets go of DRM
  master while away (`VT_SETMODE VT_PROCESS`), and takes it back on return. This keeps root's login on another VT,
  with a USB keyboard, which Hjortron's install guide relies on (`Alt+F2`). It is a repair path only (decision 9);
- with `kms.Options.VT` set to "free", opens the first free VT and switches to it, and switches back on exit. That is
  how a test run over SSH leaves tty1 and its login alone;
- after a crash, takes the crashed run's VT again as it is, where that is the VT asked for: it switches to it no
  more than that run had, and where another VT shows, starts away and waits for a switch to its own (#418);
- puts the VT back as it found it on exit, on `SIGHUP`, `SIGINT`, `SIGTERM` and `SIGQUIT`, which it catches itself,
  and on a panic on the render thread. `SIGKILL`, `os.Exit`, a panic on another goroutine and a crash in Mesa's C
  code skip that. For those, it keeps a state file under `/run` while it runs, locked: the VT it took, the VT before,
  and the backlight's level. The next run finds the file unlocked and puts those back first. The unit's
  `TTYReset=yes` also covers the unit's own VT, but not a free VT, and never the backlight. A unit that is not
  root keeps the state file in its `RuntimeDirectory=`, which systemd removes as the unit stops, crash or not, so it
  needs `RuntimeDirectoryPreserve=restart` for the next run to find the file (section 4.2).

---

## 3. Animation and looks

The owner asked for "animated and smooth, and good looking", with "something extra". The engine gives the motion.
What a touch screen adds is listed below, then a small design language that holds it together.

### 3.1 What gunim already gives

- **Springs everywhere,** from theme tokens: `Quick` for direct feedback, `Settle` for coming to rest, `Bounce` for
  arriving. A spring retargeted mid-flight keeps its speed, so an interrupted motion never jumps.
- **Wall-clock time,** so motion is the same at 60 Hz and across a dropped frame.
- **Entering and leaving** (`Transitioner`): a dialog or a row animates out before it goes, and reverses with its
  speed if it comes back.
- **Keyed lists** (`widget.Sync`): rows grow in, collapse out, and slide to new places.
- **`Pages`** slides between pages. **`Hero`** flies an element between screens. **`Toast`** for results.
- **`LiveGraph`** for a graph that takes new samples and animates them in.
- **Scrolling** with fling, a spring past the ends, and a fade at an edge with more beyond.
- **Steady subtrees** and damage, so a still screen costs nothing and a small animation costs little.

### 3.2 What is missing for touch

1. **Resampled touch** (section 2.5). The largest single gain in smoothness on this screen.
2. **Press feedback on touch-down.** With no hover, the press is the only sign a tap landed. A control lights and
   squashes on the frame the finger lands, with a soft glow spreading from the touch point. Inside something that
   scrolls, it waits up to 80 ms for the finger to choose between tap and scroll, so a scroll doesn't flash every row
   it starts on. This goes in `widget.Control`, so every control has it.
3. **Navigation that follows the finger.** `Pages` animates a change, but a finger can't drag it. A `widget.Stack`
   pushes and pops screens. A swipe from the left edge drags the top screen off as the finger moves, and lets go on a
   spring with the finger's speed: back if it went less than a third of the way slowly, on otherwise.
4. **A touch theme.** `widget.Touch []theme.Entry`, applied as `widget.Dark().With(widget.Touch...)`, as `calendar`
   and `audioui` add their light values. It raises row heights, paddings and the least target size, and turns off
   what a finger can't use (tooltips, hover).
5. **A PIN pad, an on-screen keyboard and a hold-to-confirm button** (section 3.3).
6. **A font.** Raspberry Pi OS Lite has few fonts, and a sandbox may hide them. The touch kit embeds Inter, under
   the SIL Open Font License, at regular and semibold, about 600 KB (decision 12). gunim's
   shaping, hinting and glyph atlas then work as anywhere. At 1.75 scale on the 5"'s 295 ppi, body text of 17
   logical pixels is 30 device pixels: sharp with greyscale smoothing.
7. **Brightness fades,** smooth across 32 backlight steps (section 2.9).
8. **The "something extra",** kept cheap so it never costs a frame or power at rest:
   - waking fades up while the cards rise into place on `Bounce`, one after another, 30 ms apart;
   - the storage graph draws itself on from left to right when the screen opens, as the calculator's curves do;
   - a PIN dot fills with a small bounce for each digit. A wrong PIN shakes the row of dots, and the 3-second wait
     shows as a ring that drains;
   - a change that went through sends a short ripple out from the button it came from, using `paint.BlendAdd`.
   Nothing animates while nobody touches the screen and nothing changes. A still screen draws no frames.

### 3.3 New widgets

All follow `WIDGETS.md`: constructor, `On…` callbacks, state through a getter and setter taking `u`, theme tokens,
springs from the theme, the first layout jumps, tests on every frame from awkward states.

- **`widget.PinPad`**: digits 0 to 9, delete, and OK, in a 3 × 4 grid of large keys. Typed digits show as dots,
  never as digits, and never in `Access()`. `OnCommit` hands the PIN over once and clears it. `SetWaiting(d, u)`
  shows the wait after a wrong PIN, and the pad takes no digits until it ends. It also takes a USB keyboard's
  number keys.
- **`widget.Keyboard`**: letters, digits and a few signs, in a panel that slides up from the bottom. It feeds
  `TextField` through the same `input.TextEdit` path Android's keyboard uses.
- **`widget.HoldButton`**: for the gravest changes, on top of the PIN. Hjortron uses it to revoke a device and to
  remove a guest (decision 7). Press and hold for 1 s while a ring fills. Lifting early lets the ring drain back.
- **`widget.Stack`**: screens pushed and popped, with the edge swipe of 3.2.

### 3.4 A small design language

The rules for every screen on the touch kit, so Hjortron and the next project look like one family.

- **Size.** Every target is at least 48 logical pixels both ways: 84 device pixels, 7.2 mm, on the 5" at 1.75. Spacing is on a grid of
  8.
- **Type.** Four sizes only: 40 for one big figure on a screen, 24 for titles, 17 for body, 14 for captions.
  Semibold for titles and figures, regular for everything else.
- **Colour.** Dark by default. A Pi on a shelf is often in a dim room, and a dark screen glares less. One accent
  colour. Three status colours: green for fine, amber for needs a look, red for act now. Colour is never the only
  sign: each status also has an icon and a word.
- **Shape.** Cards at radius 16, buttons at 12, chips as pills. Shadows only under dialogs. No blurred layers: they
  make the damage the whole screen (`CLAUDE.md`).
- **Motion.** The three theme springs only, no tweens in widgets. A touch shows on the same frame. Things arrive
  with `Bounce`, leave with `Settle`, and answer with `Quick`. Nothing moves at rest.
- **Layout.** One task per screen. Navigation along the left edge in landscape, with at most five places. Back by
  the edge swipe or a back button at top left.
- **Touch only.** No hover, no tooltips, no right click. A long press is never the only way to reach something.
- **Words.** Plain English, as Hjortron's guides are written. A button says what it does: "Revoke laptop", not "OK".
  An error says what happened and what to do.

---

## 4. Security and robustness

### 4.1 Two processes for Hjortron

**Recommendation:** split the touch console into an unprivileged UI and a root service.

```
hjortron-touch  (user hjortron-touch, no shell)          hjortron console-service  (root, as today's console)
  gunim + driver/kms + Mesa                                 internal/console, the PIN check, the registry
  draws, reads touch                                        does the work, checks every request again
  holds no key, writes no file          ── one Unix socket, fixed requests ──
  PrivateNetwork=yes                                        PrivateNetwork=yes
```

**Why.** The UI process runs the most foreign code: Mesa, the GPU's kernel driver, font parsing, touch input. A bug
in any of those should not reach root. This is the same split as the front and the store (design §6.1). The UI is to
the console service what the front is to the store.

**The rules on the socket.**

- The service listens on `/run/hjortron-console/ui.sock`, mode `0660`, group `hjortron-touch`. It checks the peer's
  user with `SO_PEERCRED` too.
- The requests are a fixed list, in a small typed format, with size limits: read the status, list devices, shares
  and guests, and one request per console command. A change carries the PIN.
- **The service checks the PIN itself, every time,** with today's `requirePIN`. It keeps the 3-second wait after a
  wrong PIN on its own side, so a hacked UI that sends PINs in a loop still waits 3 s each time.
- The service validates every request again, as the store does the front's. A request outside the list is
  refused.
- The UI keeps a typed PIN only until it is sent, then clears it.

Decisions 3 and 5 settle this: two processes, with the UI never running as root, and a narrow protocol of
Hjortron's own.

**What a hacked UI can do:** show wrong figures on its own screen, and send requests, each still needing the PIN.
It cannot read the PIN's hash, write a file, or reach the network.

**A separate binary.** `hjortron-touch` links purego and Mesa, so it is dynamically linked. Keeping it apart leaves
`hjortron` (front, store and console service) a static binary, as design §11 asks. `hjortron upgrade` installs
`hjortron-touch` with `hjortron`, each with its signature, or installs neither (decision 4).

### 4.2 Running as the console user, and device permissions

The UI process runs as a system user `hjortron-touch`, shell `/usr/sbin/nologin`, and nobody logs in as it. It is
started by systemd at boot, on tty1. The existing hjortron-console login goes away in K6, which removes the shell
from the console, as M3 asks.

What it needs, and only that:

| Device | Why | How it gets it |
|---|---|---|
| `/dev/dri/card*` (vc4) | modesetting, DRM master | group `video`; first opener becomes master |
| `/dev/dri/renderD128` (v3d) | GPU rendering | group `render` |
| `/dev/input/event*` | touch, and a USB keyboard | group `input` |
| `/dev/tty1` | graphics mode, VT switching | `TTYPath=/dev/tty1` |
| `/sys/class/backlight/*/brightness` | brightness | a udev rule giving group `video` write access |

A sketch of the unit:

```ini
[Unit]
Description=Hjortron touch console
After=hjortron-console-service.service systemd-udev-settle.service
Conflicts=getty@tty1.service

[Service]
User=hjortron-touch
SupplementaryGroups=video render input
ExecStart=/usr/local/bin/hjortron-touch
TTYPath=/dev/tty1
TTYReset=yes
# driver/kms's state file, kept across a restart after a crash (section 2.10)
RuntimeDirectory=hjortron-touch
RuntimeDirectoryPreserve=restart
TTYVTDisallocate=yes
StandardInput=tty-force
Restart=always
RestartSec=1
WatchdogSec=10
PrivateNetwork=yes
IPAddressDeny=any
RestrictAddressFamilies=AF_UNIX
DevicePolicy=closed
DeviceAllow=char-drm rw
DeviceAllow=char-input r
DeviceAllow=/dev/tty1 rw
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
NoNewPrivileges=yes
CapabilityBoundingSet=
SystemCallFilter=@system-service
MemoryMax=256M
Nice=5
CPUWeight=50
IOWeight=10

[Install]
WantedBy=multi-user.target
```

`check-hardening.sh` and the sandbox check then cover it: no shell, no network, no capabilities, the device list
above and nothing more.

### 4.3 Never blocking the store

- **The UI never waits on the service.** gunim's `Client.Send` returns at once. A screen shows "Working…" until the
  answer comes. The frame loop does no I/O.
- **The service never holds the store up.** It reads status through the store's socket, as a client's status comes,
  with a 2 s deadline, at most every 10 s while the status screen shows and never while blanked. It never takes the
  store's `FORMAT` lock. It takes `registry.lock` only for the moment a write needs it, as today.
- **Store busy or stopped.** The screen says "The store is not answering, since 12:03" and keeps the last figures,
  with their time. It never shows figures as current when they are not.
- **The UI yields.** `Nice=5`, `CPUWeight=50` and `IOWeight=10` put the store first. A still screen draws no frames.
  The goals table holds the store to at most 3 % slower while the UI animates.

### 4.4 Crashes and restarts

- **The UI crashes.** The kernel drops DRM master as the file closes, and the screen shows the text console. systemd
  restarts the UI after 1 s, and it is back within the start-up goal. `gunim/crashlog` writes the panic to the
  journal. The PIN in progress is lost, which is fine: nothing was sent.
- **The UI hangs** (a GPU hang, a deadlock). The frame loop feeds the systemd watchdog (`WatchdogSec=10`). A hang
  past 10 s gets the process killed and restarted. A GPU reset that loses the EGL context makes the driver return an
  error, and the process exits for systemd to restart.
- **The service crashes.** The UI shows "The console service stopped" and tries the socket again every 2 s. A command
  already running in the service either finished or did nothing, as today's commands are written to be safe at any
  stop (install-pi's power-cut notes).
- **A bad upgrade.** After 3 failed starts in a minute (`StartLimitBurst=3`), `OnFailure=` starts
  `hjortron-touch.old`, the binary the last upgrade kept, as the line console falls back to `hjortron.old` today.
- **Restarts loop.** If even the old binary fails, systemd stops trying. tty1 shows the text console. Root's login on
  tty2 is still there for repair.

---

## 5. Reuse for other Pi projects

### 5.1 The API

```go
package kms // github.com/marrasen/gunim/driver/kms

// Options configures the screen. The zero value suits a Touch Display 2 in portrait.
type Options struct {
	Connector string          // "DSI-2", "HDMI-A-1"; empty prefers DSI, then the first connected HDMI
	Rotate    int             // 0, 90, 180 or 270, clockwise
	Scale     float32         // logical pixels; 0 works it out from the panel's size
	Touch     string          // an evdev path or device name; empty finds the touch screen
	Blank     Blank           // when the screen dims and turns off; the zero value never does
	Brightness float32        // 0..1 at start; 0 keeps the current level
	VTSwitch  bool            // take Ctrl+Alt+Fn to switch virtual terminals
	VT        string          // the VT to draw on: empty for the one we were started on, "free" for a free one
	Software  bool            // draw with Mesa's software renderer even where there is a GPU
}

// Blank is when an untouched screen dims, and when it turns off.
type Blank struct {
	Dim, Off time.Duration
}

// Open opens the screen. gunim.MainWith(ctx, d, fn) then runs fn on it, as gunim.Main does.
func Open(o Options) (*Driver, error)

// Close gives the screen, the VT and the backlight back, for a program that ends without running the driver.
func (d *Driver) Close() error

// Window adds to driver.Window.
func (w *Window) SetBrightness(v float32) error
func (w *Window) Brightness() float32
func (w *Window) Wake()          // as a touch would, without the touch
func (w *Window) Software() bool // drawn by the software fallback
```

What it says about the hardware it knows: `Monitors()` returns one monitor with its bounds, rate and scale. The panel's
name, size in millimetres and touch device go in the log at start.

### 5.2 An example program

`example/kmshello`: a plant-watering panel. Two cards, a switch and a slider. It shows the touch theme and the
pattern any project copies.

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver/kms"
	"github.com/marrasen/gunim/widget"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opts := kms.Options{Rotate: 90, Blank: kms.Blank{Dim: 1 * time.Minute, Off: 5 * time.Minute}}
	err := kms.Main(ctx, opts, func(a *gunim.App) error {
		w, err := a.NewWindow(gunim.WindowOptions{
			Title: "Plants",
			Root:  widget.NewSurface(),
			Theme: widget.Dark().With(widget.Touch...),
		})
		if err != nil {
			return err
		}
		gunim.RegisterView(w, "plants", newPlants, (*plants).Set)
		c := w.Client()
		if err := c.Mount(gunim.Root, "plants", "plants", PlantState{}); err != nil {
			return err
		}
		for in := range c.Intents() {
			switch in := in.(type) {
			case WaterNow:
				go water(c, in.Bed) // the work happens off the UI
			}
		}
		return c.Err()
	})
	if err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
```

Build and copy it from any machine:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o kmshello ./example/kmshello
```

On the Pi it needs Mesa (`libgbm1`, `libegl1`, `libegl-mesa0`, `libgles2`) and a user in the groups of section
4.2. Raspberry Pi OS Lite has `libgbm1` but not the other three, so they need `apt install`. `driver/kms/README.md` gives a generic systemd unit, the udev rule for the backlight, and the
`config.txt` line for each panel size.

### 5.3 What travels and what doesn't

- **Travels:** any Pi 5 or Pi 4 with a 64-bit system (vc4 and V3D both), the 5", 7" and 10" Touch Display 2, and an
  HDMI touch screen that speaks evdev multi-touch. Each needs a test on the hardware before the README claims it.
- **Doesn't, in v1:** a 32-bit system, several screens at once, hot-plugging HDMI, keyboard layouts other than US, sound (gunim's
  `audio/speaker` already plays through ALSA, so it may simply work), and screen readers (there is no AT-SPI bus
  without a desktop).

---

## 6. Milestones

Each is one or more PRs, small enough to review. Each ends with something the owner can run. K1 to K3 are in gunim;
K4 to K6 are in Hjortron. An agent runs `kmsbench` and the `kmspi` tests on the trial Pi over SSH for each one, and
the PR records the figures (decision 10).

### K1: the driver's core and the first demo on the trial Pi

`driver/kms` with its core, and `example/kmsdemo` on top:

- one window, no popups; DRM, GBM, EGL and an atomic flip each frame; evdev touch, protocol B;
- rotation, `-rotate 0|90|180|270`;
- the sysfs backlight: `SetBrightness`, put back as found on exit;
- the software fallback: Mesa's software renderer when the GPU fails, and `Software()`;
- the VT: graphics mode, a free VT for runs over SSH, giving it back, and switching with `VTSwitch`;
- `tools/kmsbench` and the `kmspi` test tag, with the CI tests that need no Pi;
- a full-screen animated test pattern:
  - a slow colour sweep, to show banding;
  - a spinning ring and a bouncing ball, on springs, to show smoothness;
  - a grid with 1-pixel lines at the edges, to show the scale and the rotation are right;
  - a `LiveGraph` of frame times;
- a dot under each finger, up to ten, each with a fading trail and its slot number;
- on screen and on stdout once a second: fps, missed flips, p50 and p99 frame time, p50 and p99 touch-to-flip;
- a three-finger hold of 3 s, or Ctrl+C, exits and gives the screen back to the text console.

To try it: build on any PC with the line in 5.2, copy it to the trial Pi, and run `sudo ./kmsdemo -rotate 90 -vt free`
from SSH. It takes a free VT, so tty1 and `pi-screen.service` stay as they are, and it switches back on exit. (As
root in K1 only, for the VT and the backlight. K2 runs it as an ordinary user.)

**Done when** the owner sees 60 fps with no missed flips, the dots sit under the fingers in all four rotations, and
the text console comes back on exit.

### K2: the driver, complete for one screen

- popups through `compose`; `Presented` paced by flip events with `Shown` and `Lag`;
- touch resampling; the shared `driver/internal/touch`, with Android's tests still passing;
- blanking, with brightness fades, waking with the touch swallowed;
- runs as an ordinary user, with the generic unit and the udev rule in `driver/kms/README.md`;
- the rest of section 1's goals in `kmsbench`, and the `vkms` try;
- `example/widgets` runs on the Pi, as a check that nothing in the widgets needs a desktop.

**Done when** `kmsbench` passes every goal on the demo and on `example/widgets`, and a `kill -9` mid-animation
brings the screen back within 2 s under systemd.

**Where it stands** (#419). The demo passes every goal but the touch ones (#417), as an ordinary user under
systemd, and a `kill -9` brings the screen back in 1.3 s. `example/widgets` misses its frame goals while it
scrolls, and spends CPU at rest on a spinner out of sight (#420). K2 found two things the GPU path needs on
V3D: no dual-source blending, which Mesa blends in the shader, and frames that change nothing left undrawn.

### K3: the touch kit

- `widget.Touch` theme entries and the embedded Inter, regular and semibold;
- press feedback on touch-down in `widget.Control`;
- `widget.Stack` with the edge swipe; `widget.PinPad`; `widget.Keyboard`; `widget.HoldButton`;
- `example/kmshello`, which also runs on the desktop with a mouse, for work away from the Pi;
- the design language of 3.4 written into `WIDGETS.md` as its own section.

**Done when** each widget passes `WIDGETS.md`'s checklist, and the owner has tried `kmshello` on the Pi.

### K4: Hjortron, the screens that only read

- `hjortron console-service` (root) with the socket and its read requests; `hjortron-touch` with the units;
- screens: status (disks, shares, alerts), the storage graph and forecast with the client's rules (design §8.3),
  devices, shares and guests as lists, and offline disk state;
- no change can be made yet.

**Done when** the trial Pi boots to the touch status screen, it matches `hjortron` status at the root login, a
stopped store shows as stopped, and `kmsbench -screen status` passes.

### K5: Hjortron, changes with the PIN

- one console command at a time over the socket, each behind the PIN pad: revoke a device, add a device to the USB
  stick, caps and quotas, shares, guests, keys pin. Revoking a device and removing a guest also take a hold to
  confirm;
- the 3-second wait enforced in the service, with a test that sends wrong PINs as fast as it can;
- `TestP14_EveryCommandAsksOrSaysWhyNot` extended to every request on the socket;
- an adversarial test UI that sends random and malformed requests and changes nothing without the PIN.

**Done when** every command of today's prompt that the console user may run has a screen or says why not. The
commands that need the store stopped still say "use root's login", as today.

### K6: Hjortron, the shell goes

- tty1 is the touch console; the `hjortron-console` login and its `sudo` rule are removed; root's login stays on
  tty2;
- `check-hardening.sh` and the sandbox check cover `hjortron-touch`;
- `docs/install-pi.md` and the P14 note updated: the limit of the PIN is closed;
- a 24-hour soak on the trial Pi: no growth in memory, no missed flips beyond the goal, blanking and waking 100 times.

**Done when** the owner signs off on the trial Pi, and the security review the other milestones have is done for the
touch console.

---

## 7. Decisions

The owner answered these on 2026-11-13, on gunim PR #379. The plan above follows them.

1. **Which way up for Hjortron?** **Landscape** (`Rotate: 90`). The storage graph and the lists read better wide,
   and the navigation fits down the left edge.
2. **Which size of display?** **5"**, the one fitted to the trial Pi, at scale 1.75 (the owner corrected the first
   answer, 7", on 2026-11-13). The driver works out the scale for 7" and 10" too, so another project can choose.
3. **Two processes, or one root process?** **Two** (section 4.1). The touch UI never runs as root.
4. **A separate `hjortron-touch` binary?** **Yes**. It keeps `hjortron` static. `hjortron upgrade` installs it with
   `hjortron`, or installs neither.
5. **The socket.** **A narrow protocol of Hjortron's own**, as the front and the store speak. A fixed list is easier
   to check than a generic transport.
6. **When does the screen dim and turn off?** Dim after **1 minute**, off after **5**. The waking touch is
   swallowed.
7. **Any confirm beyond the PIN?** **The PIN for every action, plus hold to confirm** for revoking a device and
   removing a guest (`widget.HoldButton`).
8. **With no GPU?** **Fall back to software drawing**, with a small notice on the screen. Stop only if that fails
   too.
9. **VT switching?** **Keep it**, for root's login on tty2. It is a repair path only.
10. **Testing on the Pi.** Each milestone runs `kmsbench` and the `kmspi` tests on the trial Pi, run over SSH by an
    agent, with the figures recorded in each PR. Try `vkms` in CI in K2.
11. **Power figures.** **None.** There is no USB-C power meter, so the two power goals are dropped. CPU use and
    frames at rest stay.
12. **The font.** **Inter**, regular and semibold, embedded in the touch kit.
13. **Is a Touch Display 2 on the trial Pi?** **Yes**: the 5", on `card0-DSI-2` at 720 × 1280, with a Goodix touch
    controller, found by `display_auto_detect=1` with no overlay line.
14. **The name.** **Keep `driver/kms`**.

---

## Sources

- [DRM uAPI] Linux kernel, "Userland interfaces": <https://docs.kernel.org/gpu/drm-uapi.html>
- [KMS] Linux kernel, "Kernel Mode Setting (KMS)": <https://docs.kernel.org/gpu/drm-kms.html>
- [vkms] Linux kernel, "VKMS": <https://docs.kernel.org/gpu/vkms.html>
- [MT protocol] Linux kernel, "Multi-touch (MT) Protocol": <https://docs.kernel.org/input/multi-touch-protocol.html>,
  and "Input event codes": <https://docs.kernel.org/input/event-codes.html>
- [sysfs backlight] Linux kernel ABI, `Documentation/ABI/stable/sysfs-class-backlight`:
  <https://docs.kernel.org/admin-guide/abi-stable.html>
- Raspberry Pi's kernel, branch `rpi-6.18.y`: `arch/arm/boot/dts/overlays/vc4-kms-dsi-ili9881-7inch-overlay.dts`,
  `drivers/input/touchscreen/goodix.c`, `drivers/gpu/drm/vc4/vc4_plane.c`:
  <https://github.com/raspberrypi/linux/tree/rpi-6.18.y>
- [Pi overlays] Raspberry Pi, overlays README: <https://github.com/raspberrypi/linux/blob/rpi-6.12.y/arch/arm/boot/dts/overlays/README>
- [Pi Touch Display 2] Raspberry Pi documentation: <https://www.raspberrypi.com/documentation/accessories/touch-display-2.html>
- [Mesa V3D] Mesa, "V3D": <https://docs.mesa3d.org/drivers/v3d.html>
- [Igalia 2023] "Raspberry Pi 5 announced": <https://www.igalia.com/2023/09/28/Raspberry-Pi-5-Announced.html>
- [Igalia XDC 2024] "Raspberry Pi graphics": <https://indico.freedesktop.org/event/6/contributions/300/attachments/236/317/XDC2024-RPI-Slides.pdf>
- [EGL GBM] Khronos, `EGL_KHR_platform_gbm`: <https://registry.khronos.org/EGL/extensions/KHR/EGL_KHR_platform_gbm.txt>
- [purego] <https://github.com/ebitengine/purego>
- Prior art: kmscube <https://gitlab.freedesktop.org/mesa/kmscube>, flutter-pi <https://github.com/ardera/flutter-pi>,
  NeowayLabs/drm <https://github.com/NeowayLabs/drm>, holoplot/go-evdev <https://github.com/holoplot/go-evdev>
