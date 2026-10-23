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
| `Handle`, first | `c.showTip(e, u, node)` | the tooltip, on a disabled control too |
| `Handle`, when `Disabled` | `return c.handleDisabled(e, u, shut)` | focus let go, right clicks passed on, anything open shut |
| `Handle` | `c.ringFollows(e, th)` | the focus ring fading in and out |
| `Paint` | `defer c.faint(p, box)()`, then `c.paintRing(p, r, radius, th)` | the disabled fade, and the one focus ring |
| `Access` | `c.accessState()`, `c.accessName(label)` | disabled state and a name for screen readers |

An app's widget outside the package draws the same way with the exported helpers `widget.Faint`, `widget.FocusRing`
(round a control) and `widget.GroupRing` (round a view with a cursor of its own, such as a calendar).

## Input

- **Clicks.** A click is a primary-button press and its release over the same target. Use `widget.Clicker`:
  `Press(e, target)` on the press and `Release(e, target)` on the release, which reports whether the click lands.
  - A double click acts once. Set `Repeats` where every click counts, as on a button or a checkbox.
  - A finger that goes on to scroll lets go at `input.Away`, over no target, so scrolling a list picks nothing.
  - Act on the release, so a press dragged away cancels.
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
fades the whole control through `Faint`. Focus draws `FocusRing`. Selection uses `Accent`.

## Motion

Everything moves; nothing jumps once the user can see it.

- **Springs from the theme.** `Quick` is for direct feedback: hover, press, focus, highlight. `Settle` is for things
  coming to rest or going away. `Bounce` is for things arriving, with an overshoot.
- **The first layout jumps.** Keep a `laid` flag. Before the first layout, put every spring at its target, so a value
  set early shows at once instead of gliding up from zero. For an entrance the app asks for, give the widget a method
  that says so, as `Split.SlideFrom(v)` opens a split at share v and glides it to its share.
- **Arriving and leaving.** Implement `gunim.Transitioner` to animate in on `Entering` and out on `Exiting`; the
  engine keeps the node until it reports settled.
- **Frames only while moving.** `Step` reports true only while something moves. A widget out of sight (on a hidden
  tab, scrolled away) asks for no frames.
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
  Implement `gunim.Revealer` to scroll the focused part into view.

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
