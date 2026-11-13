# driver/kms

`driver/kms` draws one full-screen gunim UI straight to a Linux screen through the kernel's DRM/KMS,
with touch read from `/dev/input`. There is no X and no Wayland. It is made for a Raspberry Pi 5
with the Touch Display 2, and works on any 64-bit Linux screen with KMS and Mesa.

It runs as an ordinary user. It needs no root, and should not have it.

## Build and run

```go
d, err := kms.Open(kms.Options{Rotate: 90, Blank: kms.Blank{Dim: time.Minute, Off: 5 * time.Minute}})
if err != nil {
	log.Fatal(err)
}
err = gunim.MainWith(ctx, d, fn)
```

Build on any machine, and copy the binary over:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o myapp .
```

Mesa's libraries load at run time. Raspberry Pi OS Lite has `libgbm1`; the rest need installing:

```sh
sudo apt install libgbm1 libegl1 libegl-mesa0 libgles2
```

A program built with `-tags gunimkms` opens the driver from `gunim.Main` with the default options.

## Permissions

The user the program runs as needs these, and nothing more:

| What | Why | How |
|---|---|---|
| `/dev/dri/card*` | sets the screen's mode; the first program to open a card does | group `video` |
| `/dev/dri/renderD*` | draws on the GPU | group `render` |
| `/dev/input/event*` | reads the touch screen, and a keyboard for `VTSwitch` | group `input` |
| its VT, as `/dev/tty1` | graphics mode, and switching VT | systemd's `TTYPath=` |
| `/sys/class/backlight/*/brightness` | brightness and blanking | the udev rule below |

The backlight's file belongs to root. This udev rule gives group `video` write access to it:

```
# /etc/udev/rules.d/60-backlight.rules
SUBSYSTEM=="backlight", ACTION=="add", RUN+="/bin/chgrp video $sys$devpath/brightness", RUN+="/bin/chmod g+w $sys$devpath/brightness"
```

Raspberry Pi OS ships this same rule as `/lib/udev/rules.d/60-backlight.rules`. On a system without
it, add the file and run `sudo udevadm trigger --subsystem-match=backlight --action=add`, or reboot.
Without write access, the driver leaves the backlight alone, as on a screen with none: it logs so
at start, `Window.SetBrightness` and blanking dim with a black shade over the picture alone, and a
blanked screen is black with its backlight still lit.

## A systemd unit

This unit runs the program at boot as the user `mytouch`, on tty1, in place of the login there.
It starts the program again a second after it ends, even after a crash. On the trial Pi 5, a
`kill -9` brought the screen back 1.3 s later, 1 s of it the wait `RestartSec=` sets. Change the
names and the path to suit.

```ini
# /etc/systemd/system/mytouch.service
[Unit]
Description=My touch screen
# The login on tty1 lets go of it first. And systemd never stops starting
# the program again, however often it ends.
After=getty@tty1.service
Conflicts=getty@tty1.service
StartLimitIntervalSec=0

[Service]
User=mytouch
SupplementaryGroups=video render input
ExecStart=/usr/local/bin/mytouch
# The VT: the driver draws on the one the program runs on.
TTYPath=/dev/tty1
TTYReset=yes
TTYVTDisallocate=yes
StandardInput=tty-force
StandardOutput=journal
StandardError=journal
# The driver keeps a state file here, so a run after a crash puts back
# what the crashed run took. systemd must keep it across the restart.
RuntimeDirectory=mytouch
RuntimeDirectoryPreserve=restart
# Mesa keeps the shaders it compiled here, for a faster start: the user
# has no home to keep them in.
CacheDirectory=mytouch
Environment=MESA_SHADER_CACHE_DIR=%C/mytouch
Restart=always
RestartSec=1
# The sandbox: only the devices above, no network, no new privileges.
DevicePolicy=closed
DeviceAllow=char-drm rw
DeviceAllow=char-input r
DeviceAllow=/dev/tty1 rw
PrivateNetwork=yes
NoNewPrivileges=yes
CapabilityBoundingSet=
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
```

Make the user with no shell and no home, then turn the unit on:

```sh
sudo useradd --system --no-create-home --shell /usr/sbin/nologin mytouch
sudo systemctl daemon-reload
sudo systemctl enable --now mytouch.service
```

`StandardInput=tty-force` makes tty1 the program's controlling terminal, which is what lets an
ordinary user put it in graphics mode. `TTYReset=yes` puts tty1 back in text mode with a working
keyboard before and after each run. Leave out `ProtectKernelTunables=`: it makes `/sys` read-only,
and the backlight with it.

An ordinary user cannot take a free VT (`Options.VT` "free"): that goes through `/dev/tty0`, which
only root and the group `tty` open. A service runs on the VT systemd gives it, as above.

The driver catches SIGHUP, SIGINT, SIGTERM and SIGQUIT, and gives the screen back on each. After a
`kill -9` or a crash in Mesa's C code, the next run finds the state file and puts the VT and the
backlight back first. It takes the crashed run's VT again as it is: it never switches the screen
away from someone at a login on another VT. It waits until they switch back to its VT, and the
journal says so: "VT 1 is not showing; waiting for a switch to it". Until then the screen shows the
other VT. To bring the program back, switch to its VT: Ctrl+Alt+F1 on a keyboard for tty1, or from
SSH:

```sh
sudo chvt 1
```

## The screen

`display_auto_detect=1` in `/boot/firmware/config.txt`, Raspberry Pi OS's default, finds a Touch
Display 2 on its own. To name it, the overlays are:

| Panel | `config.txt` | Pixels | Scale | Tested |
|---|---|---|---|---|
| Touch Display 2, 5" | `dtoverlay=vc4-kms-dsi-ili9881-5inch` | 720 × 1280 | 1.75 | yes, on a Pi 5 |
| Touch Display 2, 7" | `dtoverlay=vc4-kms-dsi-ili9881-7inch` | 720 × 1280 | 1.25 | no |

Use `Options.Rotate`, not the overlay's `rotation`, to turn the picture: the panel can't turn it
itself, so the driver draws it turned, and turns the touches with it. Rotation 90 shows the panel in
landscape. The kernel's `rotate=` on the command line turns only the text console.

The scale comes from the panel's size: the nearest quarter of its pixels per inch over 160.
`Options.Scale` sets another.

## Brightness and blanking

`Window.SetBrightness` fades to a new brightness over a quarter of a second. The backlight has 32
steps on the Touch Display 2; the driver takes the step at or above the brightness, and lays a
shade of black over the picture for the rest, so a fade is smooth.

`Options.Blank` dims an untouched screen to 30 % of its brightness after `Dim`, over a second, and
turns it off after `Off`: it fades to black over 600 ms, sets the backlight to 0 and turns the
display controller off, and no frames are drawn. A window hears `input.WindowHidden`. The first
touch on a dim or dark screen wakes it, and reaches no window, so a tap on a dark screen never
presses what lies under it. `Window.KeepAwake(true)` holds the screen on; `Window.Wake` wakes it
with no touch. A backlight write that fails while the screen fades reaches `Driver.Errors`.

## Measuring

- `tools/kmsbench` checks the goals of [docs/plans/kms-touch.md](../../docs/plans/kms-touch.md) on
  the screen it runs on, on the demo's test pattern or on the gallery of `example/widgets`
  (`-screen widgets`). It exits non-zero if a goal is missed.
- `go test -tags kmspi ./driver/kms` runs the same as a test on the screen.
- `GUNIM_DEBUG_FRAMES=1` logs a line each second: frames, missed vblanks, frame times, touch to
  flip, and what the renderer sent the GPU.
- `GUNIM_KMS_TRACE=file` writes each frame's times as JSON lines.
- `Options.Record` writes the touch screen's events to a file, and `Options.Replay` plays such a
  file through the driver's own input path, for a measure with no finger.
