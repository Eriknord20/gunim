package main

import (
	"image/color"
	"strconv"
	"time"

	"github.com/marrasen/gunim/calendar"
)

// people are the colleagues who invite the user to things.
var people = []string{"Anna Berg", "Erik Lund", "Sara Nyström", "Johan Ek", "Lena Holm"}

// meetings are what the simulated work week is made of.
var meetings = []struct {
	title, location string
	minutes         int
}{
	{"Design review", "Room Atlas", 60},
	{"Sprint planning", "Room Beacon", 90},
	{"Customer call", "Video call", 45},
	{"Architecture sync", "Room Cedar", 60},
	{"Interview", "Room Atlas", 60},
	{"Release check", "Video call", 30},
	{"Pairing", "", 120},
	{"Budget follow-up", "Room Beacon", 30},
	{"Demo rehearsal", "Main hall", 60},
	{"Bug triage", "Video call", 45},
}

// invites are what colleagues ask the user to.
var invites = []string{
	"Coffee and a chat", "Workshop: faster builds", "Farewell lunch", "Board game night", "Planning offsite",
	"Book club", "Hack afternoon",
}

// seed fills the calendars with a few months of events around now.
func (a *app) seed(now time.Time) {
	a.cals = []Calendar{
		{ID: "work", Name: "Work", Color: color.NRGBA{R: 0x4f, G: 0x8c, B: 0xf0, A: 0xff}, Shown: true},
		{ID: "home", Name: "Personal", Color: color.NRGBA{R: 0x3f, G: 0xb9, B: 0x84, A: 0xff}, Shown: true},
		{ID: "team", Name: "Team", Color: color.NRGBA{R: 0xe0, G: 0x8a, B: 0x3a, A: 0xff}, Shown: true},
		{ID: "holidays", Name: "Holidays", Color: color.NRGBA{R: 0xd9, G: 0x5c, B: 0x8a, A: 0xff}, Shown: true},
	}
	today := calendar.Day(now)
	monday := calendar.WeekStart(today, time.Monday)
	start := calendar.AddDays(monday, -7*8)
	at := func(day time.Time, h, m int) time.Time {
		return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
	}
	add := func(s *series) {
		a.nextID++
		s.id = "s" + strconv.Itoa(a.nextID)
		a.series = append(a.series, s)
	}

	add(&series{title: "Stand-up", cal: "team", start: at(start, 9, 15), length: 15 * time.Minute, repeat: Weekdays,
		location: "Video call", notes: "What you did, what you will do, what is in the way."})
	add(&series{title: "Planning", cal: "team", start: at(start, 10, 0), length: time.Hour, repeat: Weekly,
		location: "Room Beacon"})
	add(&series{title: "1:1 with Erik", cal: "work", start: at(calendar.AddDays(start, 2), 14, 0), length: 30 * time.Minute,
		repeat: Weekly})
	add(&series{title: "Gym", cal: "home", start: at(calendar.AddDays(start, 1), 17, 30), length: time.Hour,
		repeat: Weekly})
	add(&series{title: "Gym", cal: "home", start: at(calendar.AddDays(start, 3), 17, 30), length: time.Hour,
		repeat: Weekly})
	add(&series{title: "Retrospective", cal: "team", start: at(calendar.AddDays(start, 4), 15, 0), length: time.Hour,
		repeat: Weekly, location: "Room Cedar"})
	add(&series{title: "Pay rent", cal: "home", start: calendar.MonthStart(start).AddDate(0, 0, 24), length: 24 * time.Hour,
		allDay: true, repeat: Monthly})
	add(&series{title: "Lunch with Anna", cal: "home", start: at(calendar.AddDays(monday, 3), 12, 0), length: time.Hour,
		location: "The corner café"})
	add(&series{title: "Conference", cal: "work", start: calendar.AddDays(monday, 9), length: 3 * 24 * time.Hour,
		allDay: true, location: "Congress hall", notes: "Talks on tooling and build systems. Badge at the desk."})
	add(&series{title: "Dentist", cal: "home", start: at(calendar.AddDays(monday, 1), 8, 0), length: 45 * time.Minute})
	add(&series{title: "Flight home", cal: "home", start: at(calendar.AddDays(monday, 11), 18, 40),
		length: 2*time.Hour + 20*time.Minute, location: "Gate 12"})

	// Holidays, a day each, that stay where they are.
	for _, h := range []struct {
		m    time.Month
		d    int
		name string
	}{
		{time.January, 1, "New Year's Day"}, {time.May, 1, "May Day"}, {time.June, 6, "National Day"},
		{time.December, 24, "Christmas Eve"}, {time.December, 25, "Christmas Day"}, {time.December, 26, "Boxing Day"},
		{time.December, 31, "New Year's Eve"}, {time.October, 31, "Company day off"}, {time.November, 11, "Company day off"},
	} {
		for _, y := range []int{now.Year() - 1, now.Year(), now.Year() + 1} {
			add(&series{title: h.name, cal: "holidays", start: time.Date(y, h.m, h.d, 0, 0, 0, 0, now.Location()),
				length: 24 * time.Hour, allDay: true, fixed: true})
		}
	}

	// A working day of meetings, a few a day, some of them overlapping.
	for day := start; day.Before(calendar.AddDays(monday, 7*10)); day = calendar.AddDays(day, 1) {
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		for range a.rng.IntN(4) {
			mt := meetings[a.rng.IntN(len(meetings))]
			slot := 20 + a.rng.IntN(15) // 10:00 to 17:00, by the half hour
			add(&series{title: mt.title, cal: "work", start: at(day, slot/2, 30*(slot%2)),
				length: time.Duration(mt.minutes) * time.Minute, location: mt.location})
		}
	}

	// Two invitations waiting for an answer.
	add(&series{title: "Workshop: faster builds", cal: "work", start: at(calendar.AddDays(monday, 2), 13, 0),
		length: 2 * time.Hour, from: "Sara Nyström", answer: NoAnswer, location: "Main hall"})
	add(&series{title: "Farewell lunch", cal: "team", start: at(calendar.AddDays(monday, 4), 12, 0),
		length: 90 * time.Minute, from: "Johan Ek", answer: NoAnswer, location: "The corner café"})
}

// invite has a colleague invite the user to something in the next two weeks, now and then.
func (a *app) invite() {
	day := calendar.AddDays(calendar.Day(time.Now()), 1+a.rng.IntN(13))
	if day.Weekday() == time.Saturday {
		day = calendar.AddDays(day, 2)
	} else if day.Weekday() == time.Sunday {
		day = calendar.AddDays(day, 1)
	}
	slot := 18 + a.rng.IntN(18)
	a.nextID++
	s := &series{id: "s" + strconv.Itoa(a.nextID), title: invites[a.rng.IntN(len(invites))],
		cal: "work", start: day.Add(time.Duration(slot) * 30 * time.Minute), length: time.Hour,
		from: people[a.rng.IntN(len(people))], answer: NoAnswer}
	a.series = append(a.series, s)
	a.publish()
	a.patch(Notice{Title: s.from + " invited you", ID: s.id,
		Body: s.title + " · " + s.start.Format("Monday 2 January 15:04")})
	a.after(a.between(45*time.Second, 90*time.Second), a.invite)
}
