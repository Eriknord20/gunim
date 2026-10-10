# gunim

An animation-first GUI framework for Go. Pure Go, GPU rendered, driven by
the display's refresh rate.

## Status

gunim runs on Windows 11, on Linux under X11, and on Android. On Windows
each window draws at its own monitor's rate, with 60 Hz and 120 Hz
monitors side by side. macOS builds and is still untested. The API
still changes as the examples ask more of it.

Text shapes and wraps in pure Go, including right-to-left and mixed
scripts. Sound plays in pure Go on the desktop. The examples below are
full programs: a file manager, a calendar and a chat client among
them.

`example/twowindows` is where gunim started: two windows, each
animating a rounded rectangle on its own render thread, built with
`CGO_ENABLED=0`.

```sh
CGO_ENABLED=0 go run ./example/twowindows -for 5s
```

## Examples

`example/tutorial` teaches how to build a gunim application, six
lessons in a window: the node tree, a button and the two halves, a
keyed list, a node of your own, themes, and a dialog. Each lesson has
something to try, and its own source in a code editor under it. Edit
the source and press Run: the tutorial builds again with the file as
edited and opens on that lesson, and a build error marks its line.
[example/tutorial/README.md](example/tutorial/README.md) walks the
same lessons in prose.

```sh
CGO_ENABLED=0 go run ./example/tutorial
```

`example/calculator` shows the animation: a calculator with a graph, in
a window that draws its own title bar.

```sh
CGO_ENABLED=0 go run ./example/calculator
```

Keys squash and spring back with a ripple, typed digits roll into the
display, and a sum worked out flies in an arc to the tape. On the graph,
curves draw themselves on, the one being typed morphs with each key, the
grid thickens and thins as the wheel zooms, a flick coasts, and a dot
traces the curve under the pointer on a spring. A sum with no answer
sends a red echo out past the window's edges, onto the desktop, and a
curve kept on the graph a green one.

![The calculator](example/calculator/calculator.png)
![The graph](example/calculator/graph.png)

`example/chat` is a chat client with a pretend server behind it, for trying the widgets in a chat. The
timeline opens at its latest message and stays there as messages arrive. Colleagues type, reply, edit and withdraw,
some sends fail, and the button in the header drops the connection so messages wait until it is back.

```sh
CGO_ENABLED=0 go run ./example/chat
CGO_ENABLED=0 go run ./example/chat -history 50000
```

`example/calendar` is a calendar with a pretend back end, built on the `calendar` package: a day, a week or a
month, events that repeat, and invitations from colleagues. Drag on free time to draw out an event, drag an event
to move it, and drag its bottom edge to change its length.

```sh
CGO_ENABLED=0 go run ./example/calendar
```

`example/themeedit` shows the `themeedit` package: a theme editor beside
a few widgets and a grid of cells whose cursor moves along a line. Each
edit shows at once. The Chosen tab picks out the cursor, which glides or
jumps, the accent and the gap; All values lists every token.

```sh
CGO_ENABLED=0 go run ./example/themeedit
CGO_ENABLED=0 go run ./example/themeedit -file mytheme.json
```

`example/files` is a file manager that does real work: it copies, moves,
renames, makes folders, and moves items to the trash, each in the
background with progress, cancel and undo. It is a thin program over the
`filemanager` package, which any gunim program can open on a file system
of its own, such as a server's, with its own places and favourites.

```sh
CGO_ENABLED=0 go run ./example/files
CGO_ENABLED=0 go run ./example/files -demo
```

`-demo` opens a folder of sample files made in a temporary folder, and
keeps its settings there too. Going into a folder slides its listing in
from the right, and going back slides it in from the left. The preview
crossfades between items, the progress panel slides up while something
runs, and each finished operation leaves a toast with Undo. A folder of
a hundred thousand files lists in the background and scrolls at once,
as the grid draws only the rows in view.

Ctrl+2 shows a folder as icons, and each row flies to its tile. Pictures
show thumbnails, decoded in the background for the tiles in view. Space
opens a picture large: it grows out of its tile, the arrow keys slide
to the next, the wheel zooms about the pointer, and Escape flies it back.

Items drag as a stack of cards that trails the pointer and says what a
drop will do: move or copy to the folder under it, pin it to the
favourites, or nothing, with a shake. A folder the drag rests on springs
open. Files drag between windows, out to other programs, and in from
them; files from another program light the folder they would drop in
while they are still being dragged. On Windows an item drags from a window lying behind another, which
stays behind, as Explorer's do; a click there brings the window to the
front. Ctrl+N opens another window on the same folder.

[Skiva](https://github.com/skalarit-ab/skiva) is a music player, for
gunim's sound and animation together. The track playing is a picture
disc that spins while it plays, ringed by bars that move with the
music, and lights in the cover's colours drift behind everything. It
started here as `example/music` and is now Skalarit AB's, on Google
Play for Android.

[Marras Mastering Studio](https://github.com/marrasen/mastering-studio)
masters an album, an EP or a single, through chains of VST3 plugins. It
started here as `example/mastering` and is now a project of its own,
built on gunim's `audio`, `audio/vst3` and `audioui`.

Bonbon Sudoku, a sudoku of candies made for a phone, started here as
`example/sudoku`, to see how far the animation goes. It is now an app of
Skalarit AB's, on its way to Google Play.

Three smaller examples show one thing each. `example/widgets` is a
gallery of the widgets, with a dark and a light theme to switch
between. `example/controls` has popups, pictures that fly to fill the
window, a list of a hundred thousand items, rows to put in order by
dragging, and a second window to drag pictures to. `example/paragraph`
springs a column of English, Hebrew and Arabic between narrow and wide,
wrapping it again every frame.

```sh
CGO_ENABLED=0 go run ./example/widgets
CGO_ENABLED=0 go run ./example/controls
CGO_ENABLED=0 go run ./example/paragraph
```

## Sound

Package `audio` plays sound: a `Mixer` sums the sounds playing into one
stream at 48 kHz, and `audio/speaker` plays it through the computer's
speakers, in pure Go on Linux, Windows and macOS. A voice's volume and
pan move with `anim`'s springs and tweens, stepped in time with the
sound. `Decode` reads WAV, MP3, Ogg Vorbis and FLAC, and an MP3 drops
its encoder's silence, so an album plays without gaps. An `Analyzer`
measures the sound as it is heard, for visuals that keep time with it,
and its `Spectrum` reads the sound before a voice's inserts as well as
after. An `EQ` is such an insert: a parametric equalizer of bells,
shelves, cuts and notches, whose bands glide to new settings without
clicks. A `LoudnessMeter` measures integrated loudness in LUFS as
ITU-R BS.1770 defines it, as EBU R128 and ReplayGain 2 use it, and
`FormatOf` says what a decoded sound was stored as, an `FFT` takes a
sound's spectrum block after block, and a `WAVWriter`
writes 16 or 24-bit WAV, dithered, or 32-bit float, tagged. Its `Range` reads
the loudness range, LRA, as EBU Tech 3342 defines it, and several
sounds' readings pooled measure an album as one.

Package `audio/vst3` hosts VST3 plugins, in pure Go. It finds the
plugins in the system's folders and loads one. It runs stereo sound
through an effect in realtime or offline, and saves and restores its
state. It shows the effect's own editor in a window, on Windows and
Linux.

Package `audioui` draws sound for audio programs: level meters and
faders, a spectrum and a spectrogram, a waveform, and loudness readings
and curves, in the colours of a dark studio.

`audio/speaker` plays through a fork of oto,
[marrasen/oto](https://github.com/marrasen/oto), which keeps a
Bluetooth headset on Android from crackling and says how long the
device takes to play what it is handed, so visuals keep time with the
sound as heard. gunim requires the fork by its own module path, so a
program built on gunim plays through it too.

Widgets play cues as the user works them: a press, a switch turning on
or off, a menu opening. They are silent until an application chooses
the sounds; `audio/cues` has a quiet set made in code:

```go
mix := audio.NewMixer()
if _, err := speaker.Open(mix, speaker.Options{Name: "My app"}); err == nil {
	app.SetCues(cues.New(mix))
}
```

`example/widgets` plays them; `-sound=false` turns them off.

Package `audio/band` plays songs made for programs, as a game's music.
A `band.Song` starts a `band.Player`, which plays without end; a song
that can do more offers each feature as an interface its player
implements. The first kind of song is
`band.Wander`: each part comes in with its intro, plays its loop a few
times and leaves with its outro, in 16-bar phrases, while how many play
wanders. A solo part plays through now and then. `band.Tiers` plays its
parts in tiers, as a game's music grows with its combo: its player is a
`band.Tiered`, whose `SetTier` adds or takes away parts from the next
phrase on. `band.Load` finds the
parts in files cut at the song's bars, named as `blade-intro.ogg`,
`blade-loop.ogg` and `blade-outro.ogg`.
[gunim-game-audio](https://github.com/marrasen/gunim-game-audio) is a
library of songs for it, ready to play:

```go
song, err := music.Song(music.GreekThemes)
...
mix.Play(song.Play(seed), audio.Options{Volume: 0.3, FadeIn: 2 * time.Second})
```

An application plays cues of its own with `UI.Cue`, for what happens
while the user looks elsewhere: `CueConnected` and `CueDisconnected`,
`CueDone` and `CueFailed` for work left running, and `CueBell` for
something that wants the user, as a terminal's bell. Each cue sounds
from where its node is across the window, a little left or right.

## The desktop

A window looks finished with no work from the application. By default
gunim draws its title bar, rounded corners, border and shadow, and the
window fades in as it opens and fades out as it closes. An application
that puts `widget.WindowControls` in its own tree draws its own title
bar instead. `WindowOptions.SystemFrame` keeps the system's frame, and
`WindowOptions.Instant` turns the fade off.

`WindowOptions.UnderTitleBar` lets an application draw the whole
window, with gunim's title bar over its top, the way an Android app
draws under the status bar. The bar's height comes to the application
as `Frame.Safe`, as a phone's bars do, so the same layout keeps its
content clear of both. A `widget.TitleBar` with a `Name` shows that
name whatever the window's title says, and `TitleAtStart` puts it at
the left.

`Window.Placement` says where a window is and how big, for the
application to keep as it closes, and `WindowOptions.Place` opens it
there next time. A placement whose monitor is gone opens centred on the
primary monitor, and one larger than its monitor shrinks to fit. A
window also goes full screen, stays above other windows with
`WindowOptions.Pinned`, zooms with Ctrl and +, - and 0 or the wheel
with `WindowOptions.ZoomKeys`, and asks for the user's attention in the
taskbar.

An application reaches the rest of the desktop through `App`, `Client`
and `UI`:

- **The tray.** `App.SetTray` shows an icon with a menu, and
  `App.TrayNotify` a message from it. With `App.StayOpen` the
  application runs on with no window open. Linux speaks
  StatusNotifierItem.
- **Keys from every program.** `App.RegisterHotKey` hears a key
  whatever program has the keyboard, on Windows and on X11.
- **Media controls.** `App.SetNowPlaying` tells the system what plays,
  over MPRIS on Linux and the System Media Transport Controls on
  Windows. Their buttons arrive as media keys, and a move along their
  bar as `input.MediaSeek`.
- **Files.** `Client.ChooseFiles` and `Client.SaveFile` show the
  system's dialogs. `Client.Open` hands a file to the program the
  system keeps for it, and `Client.Reveal` shows it in the system's file
  manager.
- **The clipboard.** `UI.SetClipboard` and `UI.ReadClipboard` carry
  text. `UI.ClipboardImage` reads a picture, and `UI.SetClipboardImage`
  and `Client.SetClipboardImage` put one there, on Windows, X11 and
  macOS.
- **Drag and drop.** Files and pictures drag between windows, out to
  other programs and in from them, and a drop target lights while
  another program's files hover over it. With
  `WindowOptions.DragFromBehind`, an item drags from a window lying
  behind another on Windows, as from Explorer's windows.

## Installing

A gunim program can be its own installer, so the program is all there
is to download. Package `install` does it, in one call first in `main`:

```go
install.Run(install.App{
	Name:    "Marras Mastering Studio",
	Version: version,
	Icon:    icon,
	Updates: install.GitHub{Repo: "marrasen/mastering-studio"},
	UpdateKey: "…", // as gunimsign -keygen prints it
})
```

Started from anywhere but its own folder, the program opens the
installer in place of its own window. The program's icon stands large
on a glow in its colours, and floats while the user chooses: a
shortcut on the desktop, a start with the computer, the kinds of file it
opens, and keeping it up to date. Installing raises the icon to the
middle of the window and draws a ring round it as the work goes; at the
end the ring closes and flies out, the icon jumps, a tick pops onto its
corner, confetti falls, and a green ring goes out past the window's
edges. It installs for the user alone, with no administrator: on
Windows into `%LOCALAPPDATA%\Programs` with a Start menu shortcut and
an entry under Installed apps, on Linux into `~/.local/share` with a
desktop file, its icon and a link in `~/.local/bin`. A newer copy
updates the one installed, and one already installed offers to open it.

Installed, the program starts as itself. If the user chose to keep it
up to date, it looks for a newer release on GitHub now and then, checks
it against the release's `SHA256SUMS` and their signature, and puts it
in place for the next start. `tools/gunimsign` makes the signing key
once, and signs each release's `SHA256SUMS`; the program holds the
public key, so a release only its maker signed ever runs. `program -install` installs from a script, and
`program -uninstall`, as the system's list starts it, asks in a window
and takes it all away.

Every field past `Name` and `Version` changes one thing and leaves the
rest: more files to install beside the program, the kinds of file, the
program's own offers and hooks, the folders of the user's data an
uninstall offers to take, the colours and the words, or a window of the
program's own driving the same `install.Session`.

![The installer](install/installer.png)
![Installed](install/installed.png)

## Android

The same programs build for Android. `tools/gunimapk` turns one into an
APK with the Android SDK's own tools, and `-run` installs and starts it
on the device or emulator adb sees. `-name` and `-icon` give it a label
and a launcher icon:

```sh
go run ./example/calculator -icon calc.png
go run ./tools/gunimapk -run -name Calculator -icon calc.png ./example/calculator
```

The launcher gets the icon as an adaptive one, cut to its own shape:
the picture, its clear margin cut off, fills the part the launcher
shows, over the colour of its own edge or `-icon-background`. A circle
cuts off what lies in the picture's corners.

A program turns with the phone. `-orientation` holds the screen one
way, by Android's name for it: `-orientation portrait` keeps one laid
out for a phone held upright from turning, and stretching, as the phone
tips over, and `userPortrait` lets it turn upside down too where the
user lets the screen rotate.

A build is a debug build, signed with the debug key, unless
`-keystore` names a key of your own. Then it is a release build. Android
installs an update only over an APK signed with the same key, so one
key, kept safe, signs every build from any machine. `-genkey` makes
the key the first time:

```sh
go run ./tools/gunimapk -keystore ~/keys/calculator.jks -genkey ./example/calculator
GUNIMAPK_STORE_PASS=... go run ./tools/gunimapk -keystore ~/keys/calculator.jks -install ./example/calculator
```

gunimapk finds the SDK where `ANDROID_HOME` or `~/.androidrc` points,
or where Android Studio puts it, as `~/Android/Sdk`. Google's `android`
tool installs what it needs:
`android sdk install platform-tools platforms/android-36 build-tools/36.0.0 ndk/29.0.14206865`.

Android loads a Go program as a library, which needs cgo, so the
Android build uses cgo and the NDK's compiler. The desktop builds stay
pure Go.

A tap is a click, and a popup opens over the window that opened it. A
finger that moves scrolls what it came down on and flings as it lifts,
unless what it pressed drags by touch, as a slider or the calculator's
plot does. Two fingers zoom and pan a node that zooms with a pinch
smoothly, about the point between them, as they do the calculator's
plot, and zoom anything else that zooms with Ctrl and the wheel a
notch at a time. A finger held still is a right click, which opens a context
menu; in text it selects a word, drags on over more words, and shows
handles for the selection's ends and the edit menu.

An application asks the user for a permission with `App.Ask`, as
`driver.PermissionMusic` to read their music: Android's prompt asks,
and a desktop has it already. `gunimapk -permissions music` declares
what the APK may ask for. `App.UserFolder` finds the user's Music
folder, on a phone or a desktop, and a folder chooser picks a folder on
the phone's storage or a card as a path.

`Client.Share` hands text and files to other apps through Android's
share sheet, and `Client.CanShare` says where there is one to show.
`Client.Vibrate` runs the phone's vibration motor in a pattern of on
and off times, as the web's `navigator.vibrate` does.

A node that implements `gunim.HeadingWatcher` hears which way the phone
faces, as `input.Heading`: degrees clockwise from magnetic north, where
the top edge of the screen points with the phone flat and where its
back looks with it upright, with the sensor's accuracy, so a program can
ask for a figure eight where the compass wants calibrating. The engine
runs the sensors only while such a node is shown and watching, and
stops them while the program is in the background. `Client.HasCompass`
says whether there is a compass; a desktop has none, and its watchers
hear nothing. The reading comes raw, a dozen or so times a second, for
the program to smooth, and indoors it can be off by tens of degrees.
`go run ./tools/gunimapk -run ./example/compass` shows a needle that
points north.

A tap on a text field opens the soft keyboard, whose edits, autocorrect
and composition reach the field as `input.TextEdit`s, and the window
slides up with the keyboard to keep the field in view. `Frame.Keyboard`
says how far up the keyboard reaches, frame by frame as it slides, so
a view can keep more than the field in sight, such as the button below
it, by laying itself out above the keyboard.

Go's clock runs in the phone's time zone, which the driver sets as the
program starts. `Client.Shot` takes a picture of the window, with its
popups, as on the desktop.
[driver/android](driver/android/android.go) says how it fits together.

The examples lay themselves out for a phone where the window is narrow;
the calculator and the chat take `-size 390x800` to try it on the
desktop.

## The split

A gunim program is two halves that speak only in values.

The **window** owns the widget tree, the springs, the focus ring, and
how a dialog arrives and leaves. The **application** owns the work.
Between them run commands one way and intents the other, and every one
of them is a plain value.

```go
// Window half: the wiring is data.
b := widget.NewButton("Delete everything")
b.OnClick = widget.Sends(DeleteRequested{Target: "everything"})

// Application half: reached only through Client, which returns at once.
c.Mount(gunim.Root, "confirm", "confirm", ConfirmState{Title: "Delete everything?"})
```

That keeps application code off the goroutine that draws frames, by
construction: the application holds a `Client`, and a `Client` takes
values, so values are all it can hand the window.

In one process those values cross as they are, uncopied, so a state
holding an image gives the window the image itself.
The price is the rule channels already teach: sending a value hands it
over, so leave it unchanged afterwards.

Both halves are Go, so one type declaration serves them both, and
`CheckWire` proves in a test that each one would also survive a socket.
Put a socket transport where the queue is, and the application moves
to another machine with both halves unchanged.

Local interaction stays local. A dialog closes itself on the frame the
button is released, and tells the application afterwards.

### What the split gives every application

Each of these comes from the split itself, so every gunim application
has it with no code of its own:

- **A window out of sight rests while the application works on.** A
  window draws nothing and holds its animations while it is minimized,
  covered whole by other windows, on another virtual desktop, on a
  screen that is off or locked, or in a phone application gone to the
  background. The application half runs apart from the frames, so music
  keeps playing, timers keep firing and files keep loading. Back in
  sight, the window draws the latest state at once. The application
  hears of a window minimized, as `input.WindowHidden`, and goes on
  unaware of one merely covered.
- **The window stays smooth while the application is busy.** Slow
  disks, networks and long computations happen in the application half,
  and the window keeps drawing at the display's rate meanwhile.
- **The application pushes state as fast as it likes.** The window
  draws only the latest state each frame; see [Push as hard as you
  like](#push-as-hard-as-you-like).
- **Interaction answers on the next frame.** A press, a drag or a
  dialog closing happens in the window, and the application hears of it
  afterwards.
- **Each half is tested on its own.** Tests drive the application half
  with intents and read the state it publishes, and drive the window
  half with input and state in an offscreen window, frame by frame.
- **The application can move to another machine.** The halves trade
  plain values, so a socket can carry them; see above.

## Topics and patches

Views watch topics. One publish reaches every view showing that data.

```go
c.Mount(gunim.Root, "jobs",    "joblist",  nil, "jobs")   // watches "jobs"
c.Mount(gunim.Root, "sidebar", "jobcount", nil, "jobs")   // watches it too

c.Publish("jobs", JobList{Jobs: jobs})                    // both update
c.Patch("jobs", JobProgress{ID: id, Progress: 0.4})       // one spring retargets
```

`Publish` and `Patch` draw the same line aprot draws between a refresh
trigger and `PatchSubscription`, and here that line is an animation
distinction: **a publish reconciles structure, a patch retargets a
value.** An aprot refresh lands as a `Publish`, and a pushed patch lands
as a `Patch`.

`Update(id, state)` is `Publish` to the topic named after a view's own
ID, so there is one delivery path.

## Push as hard as you like

`Client.Send` returns at once and never blocks on the window. The
application pushes at whatever rate it produces state, and the window
draws once per display refresh.

Two things make that true. A window keeps one frame in flight and
draws the next only once the display has taken it, so the display sets
the rate. And a `Publish` that a later
one supersedes is replaced in the queue before it ever reaches a view,
so 101 publishes between two frames cost one reconciliation:

```go
for i := range 100 { c.Publish("jobs", state[i]) }
c.Publish("jobs", latest)
// one frame, one view update, and latest is what it renders
```

`Patch` is left alone, because two patches on one topic may aim at
different rows. Coalescing also stops at `Mount`, `Unmount` and `Focus`,
so state queued ahead of one still reaches the views that were already
there.

`Window.Stats()` reports `Frames`, `Commands` and `Coalesced`, so the
behaviour is something you can check rather than trust.

## Keyed lists

Published state is worth animating only if the list can tell what
changed. `Sync` does that from keys.

```go
widget.Sync(l, u, s.Jobs,
    func(j Job) widget.Key { return widget.Key(j.ID) },
    newJobRow,
    (*jobRow).Set)
```

A row that arrived grows into place. A row that went collapses while its
neighbours close the gap. Everything else springs to where it belongs
now. A row that comes back mid-exit is revived with the velocity it had.

## The three ideas

**The engine owns the widget tree and runs each node through a
lifecycle.** A node is `Entering`, then `Present`, then `Exiting`. Remove
a dialog and it moves to `Exiting`, animates itself out, and tells the
engine when to unlink it. Put it back mid-dismiss and it reverses,
keeping its velocity. Over the wire that is `Mount` with the ID of a
view that is still leaving.

**Animation runs on wall-clock time.** Every frame carries a timestamp
and a delta, so motion looks the same at 60 Hz, at 144 Hz, and across a
dropped frame.

**A window draws while something moves, and sleeps the rest of the
time.** Each node reports whether its values are still in flight. Once
they all report settled, the window blocks until the next keystroke or
click.

## What a widget looks like

```go
func (d *Dialog) Transition(p gunim.Presence, f gunim.Frame) bool {
    switch p {
    case gunim.Entering:
        d.in.Animate(1, Bounce.Get(f.Theme))
    case gunim.Exiting:
        d.in.Animate(0, Settle.Get(f.Theme))
    }
    return !d.in.Active()
}
```

That is the whole exit-animation contract. The engine calls `Transition`
every frame while the node is entering or leaving. `Animate` ignores a
target it is already heading for, so the repeated call is free.

[WIDGETS.md](WIDGETS.md) holds the rules every widget follows, for its API, look, motion and tests, and a checklist
for writing a new one.

## Themes

A theme sets how widgets look and move: colours, but also paddings,
radii, fonts, text sizes and the springs that animations run on. A widget
declares each value as a token with a default, and reads it every frame:

```go
var ButtonPadding = theme.Length("button.padding", 16)

size := run.Advance + 2*ButtonPadding.Get(f.Theme)
```

A theme gives tokens values, and a window keeps one animated value per
token. Switching themes retargets them all, so going from dark to light
moves colours, sizes and motion together:

```go
w.RegisterTheme(widget.Light())
c.SetTheme("light")
```

Widgets animate state and look up style: a button animates how hovered
it is, from 0 to 1, and blends the theme's idle and hover colours by it
each frame. A theme switch in the middle of a hover then just works.
`example/widgets` switches between a dark and a light theme with the
Switch theme button in its header.

Colours blend through Oklab, so a switch keeps its brightness and its
hues. Text colours are declared with `theme.Foreground`: they fade out
in the old colour and back in with the new one. Text and background
swap brightness in a dark-to-light switch, and fading through keeps
the text from smearing into a matching grey where they cross.

A subtree can wear a theme of its own. `widget.NewThemed(child, th)`
sets th's tokens inside it, and the rest come from the theme around it
and move with it:

```go
callout := widget.NewThemed(card, theme.Make("callout",
    theme.Set(widget.CardFill, blue),
    theme.Set(widget.Ink, white)))
```

## Encoding

Encoding belongs to a socket transport. It calls `MarshalCommand` and
`MarshalEnvelope`, which use `encoding/json/v2` with one set of options
in `codec.go`:

```go
var wireOptions = json.JoinOptions(
	json.OmitZeroStructFields(true),
	json.Deterministic(true),
)
```

`OmitZeroStructFields` is why this repo has zero struct tags. A zero
field is left out and comes back zero, with no annotation on it. v2
matches member names case-sensitively, so the Go field name is the wire
name. Each carried value goes with the name `RegisterType` gave it, so
the far end knows what to decode it into:

```json
{"Command":"mount","Parent":"root","ID":"jobs","View":"joblist","Watch":["jobs"],"Value":{"Kind":"job.list","Data":{"Jobs":[{"ID":"1","Title":"Reindex archive","Status":"pending"}]}}}
```

## Icons

Package `icon` holds Lucide's icons, one variable each, named as
lucide-react names them: `icon.Funnel`, `icon.Columns3`, and older names
such as `icon.Filter` and `icon.Loader2`. A program links only the icons
it uses; `icon/byname` looks any of them up by name, and links them all.

```go
refresh := widget.NewIconButton(icon.RefreshCw, "Refresh") // the tooltip names it
copyBtn := widget.NewButton("Copy")
copyBtn.Icon = icon.Copy
spinner := widget.NewIcon(icon.Loader2, "Loading")
spinner.Spin = true
done := widget.NewIcon(icon.CircleCheck, "Saved")
done.DrawOn(600 * time.Millisecond) // strokes draw on in order
```

An icon is strokes, drawn as a coverage mask. The mask is rasterized
once per icon, pixel size and stroke width, and kept in the glyph
atlas. It is tinted as it draws, so a theme's colour animates without
drawing it again. Links, menus, drop-downs, tabs, chips, the palette, dialogs
and rich text take icons too. A text field shows one at its start, and
`Clearable` gives it an X that empties it. A toast's `Kind` picks an icon
that draws itself on as the toast arrives. The widgets' own marks, such
as a menu's tick, a drop-down's chevron and a chip's cross, are Lucide
icons too. `go generate
./icon` with `LUCIDE_REACT` set to a lucide-react package directory
writes them again from a newer Lucide.

## Vector art

Package `shape` draws vector art: SVG path data filled or stroked, and
whole SVG files, as a vector editor such as Inkscape or Figma saves
them. A shape is a coverage mask, as an icon is: rasterized once for
each size in pixels it is drawn at, kept, and tinted as it draws, so a
drawing of a dozen parts costs a dozen quads a frame. Move, turn or scale
one by a transform rather than by its rect, and it is never drawn again.

A mask past 256 device pixels across gets a texture of its own and
draws as sharp as a small one. While its size keeps changing, as in a
window being resized, it draws its last size stretched, and sharp again
once the size holds for a frame.

```go
//go:embed fox.svg
var foxSVG []byte

fox, err := shape.ParseSVG(foxSVG) // read once, as the program starts
if err != nil {
	return err
}

func (v *view) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	v.fox.Paint(p, shape.Fit(v.fox.ViewBox, geom.Rect{Max: box.Point()}))
}
```

A single path is a `shape.Path`, filled by the nonzero or the even-odd
rule, its edges smoothed, or stroked with round caps and joins:

```go
leaf, err := shape.NewPath("M12 2C6 8 6 16 12 22C18 16 18 8 12 2Z")
if err != nil {
	return err
}
grid := geom.Rc(0, 0, 24, 24) // the units the path is written in
p.Mask(leaf.Fill(), leaf.Fill().In(grid, r), green)
p.Mask(leaf.Stroke(1), leaf.Stroke(1).In(grid, r), darkGreen)
```

An SVG file's paths, rects, circles, ellipses, lines, polylines and
polygons are read, in groups with their transforms, with fill, stroke,
their opacities, the fill rule, and linear and radial gradients. Text,
images, filters, masks and clip paths are left out. A `<style>` sheet
with rules, and `<use>`, are errors that name the element: save from
the editor with styles kept on the elements (Illustrator's
"Presentation Attributes") and clones and symbols unlinked. `go run
./example/vectorart` draws a fox from a file and a leaf from a line of
path data.

## Packages

| Package | What it is |
| --- | --- |
| `gunim` | `Node`, the presence lifecycle, the window and its frame loop, and its zoom with Ctrl and +, - and 0 or the wheel |
| `gunim/anim` | `Animated[T]`, springs, tweens, easings |
| `gunim/audio` | A mixer, voices with animated volume and pan, clips, decoders for WAV, MP3, Ogg Vorbis and FLAC, and an analyzer for visuals |
| `gunim/audio/speaker` | Plays a mixer through the speakers, with oto |
| `gunim/audio/cues` | Quiet sounds made in code for the widgets' cues |
| `gunim/audio/band` | Plays songs made for programs, as a game's music, such as looping parts that come and go |
| `gunim/audio/vst3` | Hosts VST3 effects: their sound, state, parameters and editors |
| `gunim/audioui` | Meters and faders, a spectrum and a spectrogram, a waveform, and loudness readings for audio programs |
| `gunim/paint` | The per-frame draw list: rounded rects, shadows, gradients, text, images, masks, light that adds, layers that clip and tilt, and 3D scenes |
| `gunim/icon` | Lucide's icons as strokes, drawn as tinted masks; `icon/byname` looks them up by name |
| `gunim/shape` | Vector art: SVG path data filled or stroked as a mask, and SVG files read into figures |
| `gunim/geom` | float32 points, sizes, rectangles, and 3D vectors and matrices |
| `gunim/syntax` | Splits source code into tokens to colour; `syntax.Go` reads Go with `go/scanner`, and `Bash`, `PowerShell` and `Batch` read what is typed at bash, PowerShell and the Command Prompt |
| `gunim/markdown` | Shows Markdown as gunim text that the reader can select and copy |
| `gunim/match` | Finds a typed query in lines of text, ranked as a command palette ranks them |
| `gunim/emoji` | The emoji in a picker's groups, with their names for searching |
| `gunim/input` | Pointer, keyboard and focus events, keys, buttons, modifiers, and the compass's heading |
| `gunim/theme` | Tokens, themes, and animated theme switching |
| `gunim/text` | Fonts and fallback, shaping, paragraph layout, glyph rasterizing |
| `gunim/access` | What a window says to screen readers: roles, names, states, actions |
| `gunim/driver` | The seam with the operating system, and an offscreen window |
| `gunim/driver/desktop` | The driver for Linux, Windows and macOS, on GLFW and OpenGL |
| `gunim/driver/android` | The driver for Android: every window drawn into the activity's one surface, touch, and the soft keyboard |
| `gunim/install` | Makes a program its own installer: installs it for the user, with its shortcuts, kinds of file and uninstall entry, and keeps it up to date from its releases |
| `gunim/gunimtest` | Offscreen windows for tests, which fail a test that sent from or focused a node out of the tree |
| `gunim/filemanager` | A file manager to open in any program, on the local disk or on a file system of its own, such as a server's |
| `gunim/themeedit` | A theme editor to show in any program: the values the program picks out, every token in the theme with a search, a control for each kind of value, live edits and JSON files |
| `gunim/calendar` | A calendar's views: a day or a week by the hour, a month, and a small month to pick a day from |
| `gunim/widget` | `Row`, `Column`, `Scroll`, a `TileGrid` that builds only the tiles in view and selects with a band, `Label` whose text the mouse can select and copy when `Selectable`, `TextField`, `NumberField` that sits at the right, steps with the arrows and the wheel, drags up and down as in a design tool, and holds itself to its bounds, `TextArea`, `Card`, `Button`, `Icon`, `IconButton`, `Checkbox`, `Switch`, `Slider`, which runs up the height as a fader when its `Axis` is vertical, fills from a resting value a double click glides back to, colours its track with a `Gradient` and drags finely with Shift, a `SliderRow` that labels one with a readout counting along and a reset mark, a `ToneCurve` that edits a monotone curve through points, a `Histogram` of red, green and blue mixed as light, `Tabs`, a `Segmented` control whose pill springs to the option chosen, `Dropdown`, a `ColorPicker` of a square, a hue strip, an opacity strip and a hex field, and a `ColorButton` that opens one in a popup, `ContextMenu`, `Tooltip`, `Image`, `Hero` for elements that fly between screens, `Draggable` and `DropTarget` for drag and drop within and between windows and to and from file managers, a `DragGhost` that trails the pointer and says what a drop would do, a `DropZone` that lights the spot under a drag and springs it open, `Dialog`, a keyed `List`, a `VirtualList` that builds only the rows in view, a `DataGrid` that draws millions of rows of styled cells by index and drags the rows selected, an `Overview` strip that maps and scrolls it, a `MenuButton` whose menu can stay open to tick several items, removable `Chip`s and a `Wrap` that flows them onto lines, `RichText` that wraps text in several styles with links, a `CodeEditor` with line numbers, tab stops, highlighting and marks such as a compiler's errors, and their theme tokens |

Commands, intents, topics and the `Client` live in `wire.go` and
`view.go`. `driver.Offscreen` plus `Window.Frame` run a window with no
display, which is how the tests step an interface a frame at a time.

A node implements `Node`, which is `Layout` and `Paint`. Three further
interfaces are opt-in: `Handler` for input, `Animator` for animated
values, `Transitioner` for enter and exit.

## The platform layer

gunim takes it from Ebitengine 2.10, which ships a complete
reimplementation of GLFW in pure Go: X11 with GLX and EGL on Linux and
the BSDs, Win32 with WGL on Windows, Cocoa with NSGL on macOS, all
through purego. Its window constructor is the real GLFW one,
`CreateWindow(w, h, title, monitor, share)`, so several windows, a chosen
monitor and shared GL objects are all available down there. Ebitengine
confines itself to a single window in `internal/ui`, one layer up, and
that is the layer gunim replaces.

`internal/glfw` and `internal/gl` are copies of Ebitengine's, with the
changes listed in `internal/README.md`. The main one tracks the current
GL context per thread, so each window can render on a thread of its
own. Another adds a popup window: one the window manager leaves where
it is put, which never takes the keyboard.

`driver/desktop` pumps GLFW events on the main thread, and gives each
window a render thread that owns its GL context. The render thread
replays the frame's `paint` ops, swaps buffers, and reports the frame
shown once the swap returns. That report is what paces the window, so
two windows on two monitors keep two refresh rates. On Windows, GLFW's
swap waits for the compositor, which follows the fastest monitor, so
each render thread waits for its own monitor's vertical blank instead,
the way Chromium does. Where the swap does
not wait for the display, as under a remote desktop, the render thread
sleeps out the rest of the refresh itself.

On Windows the frames reach the screen through DXGI, as Direct3D
programs' do: the renderer draws with OpenGL into a Direct3D 11 texture
shared through `WGL_NV_DX_interop2`, and a flip-model swap chain shows
it through DirectComposition. That path works on every display,
including one Windows puts in place of a monitor that is switched off,
and over remote desktop tools. Where a machine lacks a piece of it, the
window presents through OpenGL, and `GUNIM_PRESENT=gl` chooses that
too.

Every shape is one quad and one signed distance field, so rounded
rectangles, strokes, gradients and shadows stay crisp at any scale.
Shapes, glyphs, images and layer composites all go through one shader
program, and each vertex carries what its pixels need, so a run of ops
is one draw call: the widgets gallery draws in three. Every window
shares the one program, built once. A layer draws into an offscreen
texture and is composited back with its opacity and rounded clip.

One `RRectOp` can carry a drop shadow and two inset shadows, as a dark
core and a light rim that stay inside the shape. A gradient runs along a
line or out in circles, through as many colour stops as it needs, and
colours a mask as well as a shape, as for a hat or a lock of hair.
Gradients with stops take their colours from a row of a small texture,
drawn once per set of colours. An opaque layer clipped to an ellipse,
`LayerOpts.Ellipse`, draws in place, with no offscreen pass: each quad
inside it is cut to the ellipse as it draws.

Shapes, masks, images, text and whole layers can add their light to
what is beneath them rather than cover it, as a game's sparks, trails
and glows do: where two of them overlap they brighten each other, up
to white. `p.Blend(paint.BlendAdd)` draws so until the function it
returns is called. Each op records the blend in force as it records
its transform, unless it asks for its own: an `RRectOp` or
`LayerOpts` with `Blend: paint.BlendNormal` covers what is beneath
even inside an added stretch. A shape's shadow adds too, which makes
a halo. Under the premultiplied blending everything draws with, a
colour with an alpha of 0 adds itself and hides nothing, so the shader
gives an added quad's colour that alpha, and it shares the batch of
the quads around it. Inside a layer the light adds to what lies
beneath the layer as it composites, times the layer's opacity. A layer
with `Blend: paint.BlendAdd` adds all it holds, times its opacity.
Cells and scenes draw normally whatever the blend.

```go
defer p.Blend(paint.BlendAdd)()
for _, s := range v.sparks {
	p.ShadowRRect(s.Rect, s.Rect.Size().W/2, paint.Solid(s.Core), paint.Shadow{Blur: 12, Color: s.Glow})
}
```

`LayerOpts.Tilt` turns a layer in depth, in perspective, about the
middle of its bounds: a card turning over, a page leaning back. A
one-sided layer shows only its front, so two of them back to back make
a card with two faces. Input follows the tilt: a tap lands on what
shows under it, and popups, the caret and accessibility bounds go
where the tilted node shows.

`Painter.Scene` draws a 3D view into a rectangle: meshes placed in a
world, a camera, and a light from far off with an even light under it.
`paint.NewMesh` makes a mesh from triangles, and `NewSphere` and
`NewBox` make the simple solids; `geom.Mat4` places, turns and scales
them. A scene draws in depth, with four samples a pixel for smooth
edges, into a texture of its own, which the frame lays in like an
image, so a scene shows inside clips, ellipses and tilts. A mesh
uploads to the GPU once and stays while frames draw it.

`Scene.Pick` finds the item under a tap, and where on it, by following
the line of sight through the tap into the scene's triangles on the
CPU, so it answers the same in a test as on any GPU. An item whose tint
or colours are less than opaque is see-through: the solid items draw
first, then the see-through ones from the furthest in, each with its
back faces before its front. `example/scene` turns a head in a view
above a card that turns over; a tap on the hat changes its colour, and a
tap on the head puts a glass bubble round it.

The painter compares each frame with the one before, and the driver
redraws only the part that changed into a canvas it keeps, then copies
the canvas to the window. A button easing into its hover colour costs
the button. The painter records each frame into the storage of the
frame before last, which the driver is done with: shapes, text, masks
and scenes, a scene's items too. So a game that draws a scene of
thousands of items and hundreds of masks every frame makes no garbage
for the collector, which on a phone would otherwise cost frames. Images upload to the GPU once, with mipmaps, and stay while
frames draw them. A layer's `Blur` and `Backdrop` are Gaussian
blurs, run at half or a quarter of the resolution when they are wide.
Text is shaped and wrapped by go-text/typesetting, a
pure-Go port of HarfBuzz, and drawn from a glyph atlas, shared by every window, that keeps four
quarter-pixel shifts of each glyph, so text sits sharp at any
fractional position. While a transform scales it, as when a dialog
grows into place, the glyphs keep their resting size and scale with
the quads, and they sharpen again when the motion settles.

Small text is hinted: each glyph's baseline, x-height, cap height and
the tops and bottoms of its strokes land on whole pixels, while widths
and positions across stay as shaped. Where Windows has ClearType on,
glyphs are drawn on the panel's red, green and blue subpixels, and
the shader blends each channel on its own. Text that is scaled or
rotated, that fades in a layer, or that sits in a transparent window
is drawn in greyscale. Linux and macOS get greyscale by default.
`WindowOptions.Text` chooses otherwise.

A character the font lacks comes from the face's fallbacks, then from
the fonts installed on the system, found on first need. So Hebrew,
Arabic or Japanese show wherever a font for them is installed, with no
font shipped by the application. `text.ParseCollection` reads `.ttc`
collections, which is how Windows ships most of its Chinese, Japanese
and Korean fonts.

Menus, drop-down lists and tooltips open in popup windows, so they
reach past the edge of the window that opened them. A popup's content
is an ordinary node in the opener's tree, run by the same goroutine,
and it animates in and out like any other. The popup opens below its
anchor, or above where the screen runs out. Where the display server
blends windows, it has round corners and a shadow.

Screen readers can read and use a window. A node that means something
to a person, such as a button, says what it is, and the engine gathers
what the drawn nodes say into a tree after each frame, while a screen
reader listens. On Linux the driver answers over AT-SPI, which Orca
reads: the tabs, fields, checkboxes, sliders and buttons of the
examples read out, and a screen reader can press, check, set and move
focus through them. On Windows it answers UI Automation, which
Narrator reads, on 64-bit Windows. macOS comes later.

A Go program for Windows is a console program, and started from
Explorer or a shortcut it gets a console window of its own. The driver
lets go of a console the program was given alone, as it starts, and
keeps one it shares with the terminal that started it, so output there
still shows. The console may show for an instant first. A release build
avoids it entirely with `go build -ldflags -H=windowsgui`, at the cost
of output in a terminal; the build tag `gunimconsole` keeps the console
in every case.

On Linux that port speaks X11, so a Wayland desktop runs gunim through
XWayland. That suits popups: X11 lets a client place a window at an
absolute screen position, and Wayland keeps a popup anchored to its
parent.

## Next

In no set order:

- Linux with a real GPU, where the swap waits for vblank. Two monitors
  at different rates run on a virtual display from
  `tools/multimon/start.sh`: each window takes its own monitor's rate,
  paced by the fallback timer, as a virtual display has no vblank.
- macOS, which builds and has never run. Its popups are borderless
  floating windows, with no popup type of their own yet, and it keeps
  the system's title bar. Package `install` lets a program run as it is
  there, until it writes the program's app bundle into ~/Applications.
- Input-method compositions on macOS, which the GLFW port reports only
  on X11 and, with a gunim change, on Windows.
- Bluetooth headsets on Windows: whether they play cleanly, and whether
  the system counts their delay (issue #26).

## Licence

gunim is under the Apache License 2.0, in `LICENSE`. The code copied from
Ebitengine keeps its own notices; see `internal/README.md`. The icons in
`icon/lucide.go` are generated from Lucide's, under the ISC licence in
`icon/LICENSE-lucide`.
