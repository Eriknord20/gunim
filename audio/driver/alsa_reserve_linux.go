//go:build linux && !android && (amd64 || arm64)

package driver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// The device reservation protocol: a program that wants a sound card to
// itself asks whoever holds it on the session bus to let go, as JACK and
// the sound servers all do. PipeWire and PulseAudio give the card up, and
// take it back when the name is free.
const (
	reservePriority = 30
	reserveWait     = time.Second
)

// reserveObject answers others asking for the card back: no, while it
// plays.
type reserveObject struct{}

// RequestRelease implements org.freedesktop.ReserveDevice1.
func (reserveObject) RequestRelease(int32) (bool, *dbus.Error) { return false, nil }

// reserveLinger is how long a card stays reserved after it closes. A
// program opens it again at once to change its buffer or start recording,
// and a sound server that took the card back in between would hold it, its
// buffers set, as the card opens again.
const reserveLinger = 2 * time.Second

// reservation is a card's reservation, as held.
type reservation struct {
	conn  *dbus.Conn
	users int
	timer *time.Timer
}

var (
	reserveMu sync.Mutex
	reserved  = map[int]*reservation{}
)

// reserveCard asks to have a card to itself, and returns what lets it go.
// Where the session bus can't say, as with no desktop, it goes on: opening
// the card tells whether it is free. It says what it does to log.
func reserveCard(card int, log func(string, ...any)) (release func()) {
	reserveMu.Lock()
	defer reserveMu.Unlock()
	r := reserved[card]
	if r == nil {
		conn := reserveName(card, log)
		if conn == nil {
			return func() {}
		}
		r = &reservation{conn: conn}
		reserved[card] = r
	}
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	r.users++
	return sync.OnceFunc(func() {
		reserveMu.Lock()
		defer reserveMu.Unlock()
		r.users--
		if r.users > 0 {
			return
		}
		r.timer = time.AfterFunc(reserveLinger, func() {
			reserveMu.Lock()
			defer reserveMu.Unlock()
			if r.users == 0 && reserved[card] == r {
				delete(reserved, card)
				_ = r.conn.Close()
			}
		})
	})
}

// reserveName takes the card's name on the session bus, and returns the
// connection that holds it, nil where there is no bus.
func reserveName(card int, log func(string, ...any)) *dbus.Conn {
	say := func(format string, args ...any) {
		if log != nil {
			log(format, args...)
		}
	}
	conn, err := dbus.SessionBusPrivate()
	if err != nil {
		say("alsa: no session bus to reserve the card by: %v", err)
		return nil
	}
	if err := conn.Auth(nil); err != nil {
		_ = conn.Close()
		say("alsa: no session bus to reserve the card by: %v", err)
		return nil
	}
	if err := conn.Hello(); err != nil {
		_ = conn.Close()
		say("alsa: no session bus to reserve the card by: %v", err)
		return nil
	}
	name := fmt.Sprintf("org.freedesktop.ReserveDevice1.Audio%d", card)
	path := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/ReserveDevice1/Audio%d", card))
	if err := conn.Export(reserveObject{}, path, "org.freedesktop.ReserveDevice1"); err != nil {
		_ = conn.Close()
		return nil
	}
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err == nil && reply != dbus.RequestNameReplyPrimaryOwner {
		ctx, cancel := context.WithTimeout(context.Background(), reserveWait)
		var freed bool
		call := conn.Object(name, path).CallWithContext(ctx, "org.freedesktop.ReserveDevice1.RequestRelease", 0, int32(reservePriority))
		cancel()
		if call.Err != nil || call.Store(&freed) != nil || !freed {
			say("alsa: the program holding the card would not let go: %v", call.Err)
		}
		reply, err = conn.RequestName(name, dbus.NameFlagDoNotQueue|dbus.NameFlagReplaceExisting)
	}
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		// Not held, so the next open asks again.
		say("alsa: reserving the card: %v", err)
		_ = conn.Close()
		return nil
	}
	return conn
}
