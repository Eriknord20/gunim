# Writing widgets

Every widget in gunim follows the rules below, so that a new one looks, moves and reads like the rest. Tests enforce
most of them; the checklist at the end names each one and what to add for a new widget.

The rules hold for `widget`, `calendar` and `audioui`, and for an app's own widgets that want to fit in.

## The API

**A constructor.** `NewX(...)` returns `*X`. It takes what the widget shows (a label, items, a child); every other
setting is an exported field.

```go
d := widget.NewDropdown(widget.Labels("Low", "Medium", "High"))
d.Tooltip = "How hard the export works"
```

**One callback shape.** Every callback is an exported field named `On…`:

```go
OnChange func(i int, u *gunim.UI) gunim.Intent
```

It runs on the UI goroutine. It may act in the window through `u`. A non-nil result goes to the app as the widget's
intent; nil sends nothing. `widget.Sends(in)` makes a callback that only sends `in`:

```go
b.OnClick = widget.Sends(Saved{})
```

Events have these names:

| Name | When it runs |
|---|---|
| `OnClick` | a button-like press, by pointer, Space or Enter |
| `OnChange` | the user changed the value or the choice |
| `OnCommit` | a drag or an edit finished: a slider let go, Enter in a field |
| `OnActivate` | a row or an item opened: Enter, or a double click |
| `OnSelect` | the selection changed in a list-like view |
| `OnPick` | an item picked from a menu or a picker |
| `OnHighlight`, `OnRemove`, `OnSort`, `OnDismiss`, `OnAccept` | as they say |

Inside `widget`, run a callback through `act` (or `act0` for one that takes only `u`). It plays the widget's sound
cue, runs the callback and sends a non-nil result, so every widget sounds and acts alike.

**State through a getter and a setter.** State lives in unexported fields. Read it with a getter and set it with a
setter that takes the UI:

```go
Selected() int            SetSelected(i int, u *gunim.UI)
Value() float32           SetValue(v float32, u *gunim.UI)
Text() string             SetText(s string, u *gunim.UI)
Checked() bool            SetChecked(on bool, u *gunim.UI)
```

- A setter sends no intent: it is the app speaking, and the app already knows.
- Once the widget has been laid out, a setter animates to the new state with the theme's motion.
- Before the first layout it jumps, and the first frame shows the new state.
- `u` may be nil, as when a view builds and sets a widget before mounting it. The setter jumps, and must not panic.
  `UI.Invalidate` and `UI.Theme` are safe on a nil UI; a setter that inserts or removes nodes keeps them until the
  widget mounts, and reports them through `Children`.
- Plain shown text, such as `Button.Label` or `Label.Text`, is an exported field.

**Items as one struct each.** A list of items is a slice of structs, set with `SetItems` and read with `Items()`.
Menus use `[]MenuItem{Label, Hint, Icon, Swatch, Checked, Disabled, Caption, Break}`; `widget.Labels(...)` makes plain
ones. Use one struct per item for anything that gives each item several facts; parallel slices of different lengths
were a source of bugs.

**Keys keep state on its item.** Where items can move, give the item struct a `Key`, as `NavItem.Key`. Then, as in
`NavList`:

- `SetItems` keeps the chosen item and the focus ring on the items with those keys. An item with no `Key` keeps its
  place.
- When the ring's item or the chosen item goes, each keeps its place, kept inside the list, and no intent is sent.
- A repeated key follows the first item with it. Keys are meant to be unique.
- The screen reader's part for an item takes its key from the item's `Key`, so a hold on it follows the item.
- The ring glides with its item, and stays where the user left it when focus comes back.

A widget with a ring over its items gives `Cursor`, and `SetCursor` where the app moves the ring, which scrolls the
item into view. A view of many rows names the item by its `Key`: `List`, `Table` and `Tree` give `Cursor() (Key,
bool)`, and `Table` and `Tree` give `SetCursor(key, u)`. `NavList`, a short column of rows its app builds whole, names
the row by its place: `Cursor() int` and `SetCursor(i, u)`.

**Shared names.**

- `Items` holds the choices of a dropdown, a segmented control or a menu. `Titles` holds the titles of tabs.
- `Label` is a control's own text, and also its name for a screen reader.
- `Axis` says which way a widget runs (`Slider`, `Split`, `Group`).
- `Disabled`, `Tooltip` and `KeepFocus` mean the same on every control (see Control below).
- `KeepFocus` leaves the keyboard where it is on a click. `SkipFocus` takes the widget out of the Tab order.

**Doc comments.** A widget's doc comment says what it is, how it moves, and its keys, in that order. Write plain
English in short sentences. Say what a thing is; a sentence that opens with what a thing is not reads as a riddle.

## Control: the shared base

`widget.Control` holds what every interactive widget shares: `Disabled`, `Tooltip`, `KeepFocus`, and springs for
hover, press, the focus ring and the disabled fade. A control in `widget` embeds it and calls its methods:

| Where | Call | What it gives |
|---|---|---|
| constructor | `newControl()`, then add the widget's own springs to its group | one `Step` for all the springs |
| `Layout` | `c.follow(th)` | the disabled fade, jumping on the first layout |
| `Handle`, first | `c.showTip(e, u, node)` | the tooltip, on a disabled control too, and a finger's press lit and glowing |
| `Handle`, when `Disabled` | `return c.handleDisabled(e, u, shut)` | focus let go, right clicks passed on, anything open shut |
| `Handle` | `c.ringFollows(e, th)` | the focus ring fading in and out |
| `Handle`, on a press | `c.squashIn(th)` in place of animating `squash` | the squash, which waits as the light does inside something that scrolls |
| `Paint` | `defer c.faint(p, box)()`, then `c.paintRing(p, r, radius, th)` | the disabled fade, and the one focus ring |
| `Paint`, over the fill | `c.paintGlow(p, r, radius, th)` | the glow that spreads from a finger as it presses |
| `Access` | `c.accessState()`, `c.accessName(label)` | disabled state and a name for screen readers |

A finger's press shows on the frame it lands. `showTip` lights the control through its hover spring, `squashIn`
squashes it, and `paintGlow` draws a soft glow out from where the finger landed. Inside something that scrolls, all
three wait up to 80 ms for the finger to show whether it taps or scrolls, so a scroll lights none of the controls it
starts on. A finger has no hover otherwise: the engine sends no enter or leave as it moves.

An app's widget outside the package draws the same way with the exported helpers `widget.Faint`, `widget.FocusRing`
(round a control) and `widget.GroupRing` (round a view with a cursor of its own, such as a calendar).

## Input

- **Clicks.** A click is a primary-button press and its release over the same target. Use `widget.Clicker`:
  `Press(e, target)` on the press and `Release(e, target)` on the release, which reports whether the click lands.
  - A double click acts once. Set `Repeats` where every click counts, as on a button or a checkbox.
  - A finger that goes on to scroll lets go at `input.Away`, over no target, so scrolling a list picks nothing.
  - Act on the release, so a press dragged away cancels.
  - For a control drawn by hand, whose parts are something other than an index, use `widget.Click[T]`. Every press
    starts a click there, so check `Clicks` to act once on a double click.
- **Keys.** Space and Enter activate; arrows move; Home and End go to the ends; Escape closes what is open; Tab moves
  on. Ctrl+Shift+key is its own shortcut, apart from Ctrl+key. A disabled control takes no keys.
- **Focus.** A control is focusable while enabled. The ring follows `input.FocusRing` with a spring. When something
  hides, move the keyboard off it (`UI.FocusFirstLaidOut` reaches a node laid out this frame).
- **Hit tests use where things are drawn.** While rows, tiles or events spring to new places, test the pointer
  against their drawn position (`anim.Float.Value()`), never against where they are heading (`Target()`).
- **Hover follows content.** When content scrolls under a resting pointer, work out the hover again from the last
  pointer position on every frame of the scroll.
- **Every control works from the keyboard.** A part that takes a click (a chip's remove cross, a split's divider, a
  curve's point) has a key too.

## Look: the theme

**Every look comes from a token.** Colours, sizes, radii, paddings, fonts and springs are tokens, declared in the
package's `theme.go`. A literal colour or size in `Paint` is a bug; `TestTheLooksComeFromTheTheme` catches the ones it
knows.

```go
ChipFill   = theme.Color("chip.fill", color.NRGBA{...})        // a colour
ChipInk    = theme.Foreground("chip.ink", color.NRGBA{...})    // a colour drawn on top: text, icons
ChipRadius = theme.Length("chip.radius", 12)                   // a size in logical pixels
ChipSize   = theme.FontSize("chip.size", 12)                   // a size of text, which the user's text size scales
ChipHeight = theme.TextRow("chip.height", 24)                  // room for a line of text, which grows with it
```

- **Keys** read `<widget>.<part>[.<state>]`: `button.primary.hover`, `scroll.bar.width`, `menu.row.height`.
- **Defaults are the dark theme.** `widget.Dark()` is the defaults; `widget.Light()` sets a light value for every
  colour. `TestTheLightThemeGivesEveryColourALightValue` fails for a colour token that Light leaves dark; give it a
  light value, or add it to `bothThemes` with the reason.
- **Text and icons use `Foreground`.** It fades through clear as the theme switches, so text never smears into its
  background mid-switch.
- **Reuse before declaring.** `Ink`, `Accent`, `TextSize`, `Font`, `MonoFont`, `FocusRadius`, `KnobShadow`,
  `ScrollFade` and the fills of buttons, fields, menus and cards cover most needs. A new token is for a look that
  differs on purpose.
- **Another package's light values** go in an exported `Light []theme.Entry`, added as
  `widget.Light().With(calendar.Light...)`. `calendar` and `audioui` do this.

**States look alike.** Hover lightens the fill through the hover spring. Press squashes through its spring. Disabled
fades the whole control through `Faint`. Focus draws `FocusRing`. Selection uses `Accent`. In a column of rows, the
chosen row sits on a pill that glides to it, with an accent mark at its left, as in `Tree` and `NavList`.

## Motion

Everything moves; nothing jumps once the user can see it.

- **Springs from the theme.** `Quick` is for direct feedback: hover, press, focus, highlight. `Settle` is for things
  coming to rest or going away. `Bounce` is for things arriving, with an overshoot, as a `RadioGroup`'s dot
  springs in.
- **The first layout jumps.** Keep a `laid` flag. Before the first layout, put every spring at its target, so a value
  set early shows at once instead of gliding up from zero. For an entrance the app asks for, give the widget a method
  that says so, as `Split.SlideFrom(v)` opens a split at share v and glides it to its share.
- **A live graph's history jumps in.** `LiveGraph.Fill(samples)` swaps in a whole span at once, the head on the
  newest sample. This is the one jump on purpose: replaying the span through `Add` slides through it at its own
  pace, so old values show as current. Call `Fill` once for the span, when a page opens or a filter changes, and
  `Add` for each sample after it. A `Fill` on every tick steps the curve and never slides it.
- **Arriving and leaving.** Implement `gunim.Transitioner` to animate in on `Entering` and out on `Exiting`; the
  engine keeps the node until it reports settled.
- **Frames only while moving.** `Step` reports true only while something moves. A widget out of sight (on a hidden
  tab, scrolled away) asks for no frames.
- **Draw another clock at `Frame.Present`.** A frame lights the screen a few refreshes after it is drawn. A playhead,
  a meter or anything else that follows the sound or a video draws it as it is at `f.Present` (`ui.Present()` in
  `Step`): `Mixer.HeardAt(f.Present)`, `Voice.PositionAt(f.Present)`, or `Analyzer.At = f.Present` before it measures.
  Springs and fades keep to `Delta`.
- **Sound cues.** Play the cue for what happened: `CuePress` (an activation), `CueToggleOn`/`CueToggleOff`,
  `CueSelect` (a choice among several), `CueTick` (a step on a scale), `CueOpen`/`CueClose` (something shown or put
  away). `act` plays it for you.

## Room

**Draw inside the box.** Paint everything inside the size `Layout` returned, allowing a focus ring's 3 pixels round
it. The test helper `spills(ops, box, reach)` lists anything drawn outside.

- **Text fits its room.** Cut a line with `cutRun` against the width `Paint` receives, so it ends in "…". Lay out
  longer text with `MaxLines` and an ellipsis. Give a tooltip the whole text when it is cut.
- **Least width.** A node that wraps or shortens implements `widget.Shrinker` (`MinWidth`). `Row` and `Column` shrink
  their children in proportion to their size, and none below its `MinWidth`.
- **Popups fit the screen.** Content opened with `UI.OpenPopup` implements `gunim.PopupFitter`: it is told the room
  above, below, left and right of its anchor, and keeps its width and height inside it. A popup with a shadow
  implements `gunim.PopupPadder`.
- **Too much content scrolls.** Inside `widget`, embed `scrolling` (as `Scroll`, `VirtualList` and `Menu` do). It
  gives the wheel, a finger, the scroll bar, springs past the ends, and the fade at an edge with more past it.
  Implement `gunim.Revealer` to scroll the focused part into view. `Scroll.Reveal` takes a box measured from the
  top of what shows; a row's box measured from the top of the content goes to `Scroll.RevealContent`. Mixing them
  up scrolls past the row by however far the view was already scrolled.

**Work only on what shows.** A widget that holds a collection does work for the items in view, per frame, per key
and on open. 100 000 items must cost the same per frame as 100.

- Build and paint only rows in view; keep row tops in a structure with prefix sums (`VirtualList` uses a Fenwick
  tree).
- Hit-test by search, never by walking every item.
- Measure widths once when the items change, never per frame.
- Key caches on an edit version, never on a string rebuilt and compared every frame.
- Add a benchmark at 100 000 items for any widget that holds a collection.

## Accessibility

Every interactive widget implements `gunim.Accessible` (`Access()`) and, when a screen reader can act on it,
`gunim.AccessActor` (`AccessAct`). It reports a role, a name (its `Label`, or `Tooltip` when it shows none), its value,
and `StateDisabled` while disabled (`Control.accessState` gives it). A view of rows publishes the rows in view.
A widget that draws no name of its own takes a `Label` for screen readers, as `NavList` does, and passes it to
`accessName`. A name such as "Pages" then doesn't also pop up as a tooltip. `Tooltip` stays the name where `Label` is
empty.

## Tests

- **Every frame.** For anything that moves or scrolls, assert on every frame, starting from an awkward state: half
  scrolled, a popup opened under the pointer, items changed while open, a value set before the first layout. An end
  state that comes out right can hide a jump on the way.
- **Helpers in `widget`.** `stage(t, root)` mounts a node in an offscreen window and returns a function that runs
  frames. `painted(node, size)` returns what a node paints. `click`, `doubleClick`, `rightClick` and `fingerScroll`
  send pointer input. `spills` finds paint outside a box. For popups, send input with `popup.Input(...)` and set the
  screen's room with `w.Offscreen().SetWorkArea(rect)`.
- **Pixels.** `w.Pixels()`, or `gunimtest.Pixels(t, w)`, draws an offscreen window's last frame with the real
  renderer, in a hidden window, and returns the picture; `gunimtest.WritePNG` saves it to look at. Where no display or
  OpenGL is at hand, and on macOS and Android, the test skips. Assert on state first: a GPU rounds edges and blurs its
  own way, so a pixel check allows a little either side.

## Touch: the design language

The touch kit is for a screen worked by a finger, such as a Raspberry Pi with the Touch Display 2. These rules hold
for every screen built on it, so the apps that use it look like one family. `widget.Touch` sets the tokens that carry
them: lay it over a theme as `widget.Dark().With(widget.Touch()...)`. `example/kmshello` follows every rule here.

- **Size.** Every target is at least 48 logical pixels both ways: 84 device pixels, 7.2 mm, on the 5" display at
  scale 1.75. Spacing is on a grid of 8.
- **Type.** Inter, built into the program, in four sizes only: `FigureSize` (40) for one big figure on a screen,
  `HeadingSize` (24) for titles, `TextSize` (17) for body text and `CaptionSize` (14) for captions. Titles and
  figures are semibold (`BoldFont`), everything else regular.
- **Contrast.** Text reads at 4.5:1 or more on what it sits on, a caption too (`CaptionInk`), and an edge that shows
  where a control is, a field's border or a key's, at 3:1 or more. `TestTheTouchKitMeetsItsContrast` checks the
  kit's own, in both themes.
- **Colour.** Dark by default: a screen on a shelf is often in a dim room, and a dark one glares less. One accent
  colour. Three status colours: green for fine, amber for needs a look, and red for act now (`ToastSuccessInk`,
  `ToastWarningInk` and `ToastErrorInk`). Colour is never the only sign: each status also has an icon and a word.
- **Shape.** Cards are rounded by 16, buttons by 12, and chips are pills. Shadows go only under dialogs. Blurred
  layers are left out, since one makes the damage the whole screen (`CLAUDE.md`).
- **Motion.** The three theme springs only, and no tweens in widgets. A touch shows on the frame it lands. Things
  arrive with `Bounce`, leave with `Settle` and answer with `Quick`. Nothing moves at rest. A clock is the one tween:
  a `HoldButton`'s fill and a `PinPad`'s wait fill and drain at an even pace, as time passes.
- **Layout.** One task per screen. Navigation runs down the left edge in landscape, with at most five places. Back
  is a swipe from the left edge (`Stack`) or a back button at the top left.
- **Touch only.** No hover, no tooltips (`Tooltips` is off), no right click. A long press is never the only way to
  reach something.
- **Words.** Plain English. A button says what it does: "Revoke laptop", not "OK". An error says what happened and
  what to do.

The kit's own widgets:

- `Stack` pushes and pops screens. A swipe from the left edge drags the top one off, and lets go on a spring with
  the finger's speed.
- `PinPad` takes a PIN on large keys. Its digits show as dots, never reach a screen reader, and are cleared as they
  are handed over. `SetWaiting` holds it after a wrong PIN.
- `Keyboard` is a keyboard on the screen, under the content, for a screen with none of its own. It slides up while a
  field has the focus, and types into it through the same edits a phone's keyboard makes, through `UI.Type`.
- `HoldButton` acts only once held down for a second, for the gravest changes, on top of a PIN. It fills from start
  to end as it is held, and shows a tick once it has acted. A screen reader's press acts at once; the PIN is the
  guard there.
- `Keyboard` cannot tell a keyboard plugged in. An app that sees one sets `Keyboard.Disabled`, and the keyboard on
  the screen stays down.

## Checklist for a new widget

1. `NewX` returns `*X`; settings are exported fields; the doc comment says what it is, how it moves, and its keys.
2. Callbacks are `On…` fields of the one shape, run through `act`. Add the type to the `structs` list in
   `widget/callbackshape/callbacks_test.go`; `TestEveryStructIsListed` fails until you do.
3. State has a getter and a setter taking `u`; the setter animates once laid out, jumps before, and takes nil. Add the
   setter to `TestEverySetterTakesANilUI` (`widget/nilui_test.go`, or `calendar/nilui_test.go`).
4. An interactive widget embeds `Control`, uses `Clicker` for clicks, and works from the keyboard.
5. Every look is a token, with a light value in `Light()`; text and icons use `Foreground` tokens.
6. Springs come from `Quick`, `Settle` or `Bounce`; the first layout jumps; `Step` rests when nothing moves.
7. Paint stays inside the box; text is cut to fit; popups implement `PopupFitter`; long content scrolls.
8. A collection does work only for what shows, with a benchmark at 100 000 items.
9. `Access()` reports role, name, value and disabled.
10. Tests assert on every frame, from awkward states.
