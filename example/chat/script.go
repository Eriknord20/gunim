package main

import (
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/marrasen/gunim/chat"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// scriptPause is how long each step of a script waits for the one before it to show.
const scriptPause = 500 * time.Millisecond

// runScript runs the steps of a script from -do one after another, each after a pause. A step is "reply" to reply
// to the second latest message, "type:text" to type text, read as a Go string, in the message box, "send" to press
// Enter, "paste" to press Ctrl+V, "react:emoji" to react to the latest message, "vote:n" to vote for option n of
// its poll, "click:x,y" to click there, "offline" and "online" to drop and restore the connection, "typing:name" to
// have name type, "hover:x,y" to move the pointer there, "drag:x,y,x,y" to press at the first place and drag to
// the second, "theme:light" or "theme:dark" to switch the theme, "popout" to open the conversation in a window of
// its own, "files" to open the project's files, "folder:path" to open one of their folders, "drop:path|path" to drop files
// there as if from another program, "share:folder|name" to share a file in the conversation, and "shot:path" to
// write the newest window to a PNG file.
func (a *app) runScript(steps []string) {
	if len(steps) == 0 {
		return
	}
	a.after(scriptPause, func() {
		a.scriptStep(steps[0])
		a.runScript(steps[1:])
	})
}

func (a *app) scriptStep(step string) {
	verb, arg, _ := strings.Cut(step, ":")
	var err error
	switch verb {
	case "reply":
		if n := len(a.current.msgs); n >= 2 {
			a.handle(chat.ReplyAsked{ID: a.current.msgs[n-2].ID})
		}
	case "type":
		err = a.c.Input(a.ctx, input.TextInput{Text: unquote(arg)})
	case "send":
		err = a.c.Input(a.ctx, input.KeyPress{Key: input.KeyEnter})
	case "paste":
		err = a.c.Input(a.ctx, input.KeyPress{Key: input.KeyV, Mods: input.ModControl})
	case "offline", "online":
		if (a.link == chat.Online) == (verb == "offline") {
			a.handle(LinkToggled{})
		}
	case "typing":
		a.setTyping(arg)
	case "vote":
		if n := len(a.current.msgs); n > 0 {
			i, _ := strconv.Atoi(arg)
			a.handle(chat.PollVoted{ID: a.current.msgs[n-1].ID, Option: i})
		}
	case "react":
		if n := len(a.current.msgs); n > 0 {
			a.handle(chat.ReactionToggled{ID: a.current.msgs[n-1].ID, Emoji: unquote(arg)})
		}
	case "click":
		at := points(arg)[0]
		err = errors.Join(
			a.c.Input(a.ctx, input.PointerMove{Pos: at}),
			a.c.Input(a.ctx, input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1}),
			a.c.Input(a.ctx, input.PointerUp{Pos: at, Button: input.ButtonPrimary}))
	case "hover":
		err = a.c.Input(a.ctx, input.PointerMove{Pos: points(arg)[0]})
	case "drag":
		pts := points(arg)
		if len(pts) < 2 {
			log.Printf("script: %s: want two places", step)
			return
		}
		err = errors.Join(
			a.c.Input(a.ctx, input.PointerMove{Pos: pts[0]}),
			a.c.Input(a.ctx, input.PointerDown{Pos: pts[0], Button: input.ButtonPrimary, Clicks: 1}),
			a.c.Input(a.ctx, input.PointerMove{Pos: pts[1]}))
	case "files":
		a.handle(AreaChosen{Area: "files"})
	case "folder":
		a.handle(FolderOpened{Path: arg})
	case "share":
		folder, name, _ := strings.Cut(arg, "|")
		a.handle(FileShared{Folder: folder, Name: name, Conversation: a.current.ID})
	case "drop":
		a.handle(FilesDropped{Folder: a.path, Paths: strings.Split(arg, "|")})
	case "popout":
		a.handle(chat.PopOut{})
	case "shot":
		err = writeShot(a.ctx, a.windows[len(a.windows)-1].c, arg)
	case "theme":
		a.light = arg == "light"
		err = a.c.SetTheme(arg)
	default:
		log.Printf("script: no step %q", step)
	}
	if err != nil {
		log.Printf("script: %s: %v", step, err)
	}
}

// points reads places written as x,y,x,y and so on.
func points(s string) []geom.Point {
	var out []geom.Point
	parts := strings.Split(s, ",")
	for i := 0; i+1 < len(parts); i += 2 {
		x, _ := strconv.ParseFloat(strings.TrimSpace(parts[i]), 32)
		y, _ := strconv.ParseFloat(strings.TrimSpace(parts[i+1]), 32)
		out = append(out, geom.Pt(float32(x), float32(y)))
	}
	return out
}

// unquote reads s as a Go string reads, so \n breaks a line and \U0001F44D is an emoji, or leaves it as it is.
func unquote(s string) string {
	if u, err := strconv.Unquote(`"` + strings.ReplaceAll(s, `"`, `\"`) + `"`); err == nil {
		return u
	}
	return s
}
