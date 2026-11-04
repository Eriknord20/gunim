# gunim

`WIDGETS.md` has the rules for every widget: the API, input, look, motion, room, accessibility and tests. Read it
before writing or changing a widget or a `Paint`. This file adds the rules for drawing that stays fast and looks
good.

## Frames stay at the monitor's rate

gunim draws at the monitor's rate, 120 Hz and more, and an app built on it must keep up with its heaviest
screen. A frame steps, lays out and paints the tree, and then the renderer draws it, all on the window's thread. At
120 Hz all of that gets 8 ms. A widget that drops frames is unfinished, the same as one that jumps.

### Paint less: steady subtrees

- **Give heavy views a key.** A view of many parts implements `Steady`. `SteadyKey` returns a comparable value
  that holds everything the subtree draws: a version of its data, its scroll, and a counter of the frames its
  springs moved in. While the key, the constraints and the place stay the same, the engine carries the subtree's
  ops from the frame before, with no `Layout` or `Paint`. A view without a key is laid out and painted on every
  frame anything in the window moves. `widget.NewSteady(child, key)` and `widget.NewSteadyPaint(paint, key)` make one
  without a node type of its own.
- **Keep what moves apart from what stands.** A subtree is carried only while no node in it moves (`Step` true,
  or `WakeIn`), enters or leaves. A meter, a cursor or a lit step goes in a small node of its own, next to the still
  body, not inside it. One live meter inside a panel has the whole panel painted again on every frame.
- **Keep a view's springs in its own group.** A spring counter that goes into other views' keys must count only
  springs that change what those views draw. One that moves on every frame (a meter falling, or anything stepped
  "while playing") voids every key that holds the counter. `anim.Moves` is that counter: end `Step` in
  `return v.moves.Note(v.Group.Step(dt))`, and put `v.moves.Count()` in the key. It counts the frame the springs land
  in too.
- **`Step` is true only while something moves.** Don't keep it true for a state such as "playing". Keep it true
  for the parts that change pixels.
- **Check the key.** `GUNIM_DEBUG_STEADY=1` paints every carried subtree again and logs each key that said "same"
  when the drawing was not.

### Draw less: count the quads

The renderer draws every op that touches the frame's damage, and that includes carried ops. Damage is one
rectangle. Two moving parts far apart, such as a playhead at the top and a meter at the bottom, make it nearly the
whole window. So what shows must be cheap to draw, not only cheap to paint.

- **Every `RRect`, stroke and glyph run is a quad.** Aim for a few thousand in a view. Tens of thousands is a bug,
  whatever the profile says about paint time.
- **A curve is one ribbon.** Draw an arc, a wire, an envelope or a knob's track as one `Painter.Ribbon`, sampled at
  about one point per 3 px of its length. Don't build it from short rotated `RRect`s (`audioui.Segment` in a loop).
  Each of those is an op, a transform push and a quad. Their rounded ends overlap, and with a faded colour every
  joint shows a darker bead. A ribbon is smooth, even in colour and cheap. Its width and colour can change along
  it, for a taper or a fade.
- **A circle is one `RRect`** with its radius at half its size, or one `RRectStroke` for a ring.
- **A level is a few runs, not a column each.** Draw a waveform, a meter or a spectrum as one ribbon, or as runs
  of a colour. Never draw one `RRect` per pixel column.
- **Leave out what can't show.** Skip parts outside `Painter.Visible()`, parts faded to nothing and parts a later
  op covers. Blurred and tilted layers make the damage the whole window, so use them only where they show.
- **Shape text once.** Keep shaped runs (`audioui.Shaped`, or a cache keyed on the text), never shape per frame.

### Measure

- `GUNIM_DEBUG_FRAMES=1` logs a line each second for each window. Read frames late, `layout and paint` in ms,
  `draw` in ms, frames left undrawn as unchanged, and `quads` and MB sent each frame.
- Profile the heaviest real screen in a wide window while it animates, not a demo at rest. Check paint and draw
  together against 8 ms.
- A widget that holds a collection gets a benchmark at 100 000 items (`WIDGETS.md`, "Room").
