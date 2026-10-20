package main

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/marrasen/gunim/chat"
)

// activeFor is how long after someone last wrote they still count as active.
const activeFor = 10 * time.Minute

// membersOf returns the people of c as the panel lists them: the user first, then those active, then the rest,
// each part by name.
func (a *app) membersOf(c *conv, now time.Time) []chat.Member {
	seen := c.lastWrote
	out := make([]chat.Member, 0, len(c.people)+1)
	out = append(out, chat.Member{Name: me, Active: a.link == chat.Online, Status: "You", You: true})
	var active, away []chat.Member
	for _, p := range c.people {
		m := chat.Member{Name: p, Status: "Away"}
		if t, ok := seen[p]; ok {
			if now.Sub(t) < activeFor {
				m.Active, m.Status = true, "Active now"
			} else {
				m.Status = "Last wrote " + ago(now.Sub(t))
			}
		}
		if m.Active {
			active = append(active, m)
		} else {
			away = append(away, m)
		}
	}
	byName := func(a, b chat.Member) int { return strings.Compare(a.Name, b.Name) }
	slices.SortFunc(active, byName)
	slices.SortFunc(away, byName)
	return append(append(out, active...), away...)
}

// ago says how long ago something was, roughly.
func ago(d time.Duration) string {
	switch {
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " min ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " h ago"
	}
	return strconv.Itoa(int(d.Hours()/24)) + " days ago"
}
