package chat

import "time"

// groupFor is how long a message shares the heading of the one that started its group, by the same author.
const groupFor = 5 * time.Minute

// Timeline returns msgs, in the order they were written, as the timeline's rows, with a heading for each day. A
// message shares the heading of the one before when both are by the same author, it replies to nothing, it is not
// private, and its group started under five minutes ago, so even a steady stream from one person shows a heading
// every few minutes. The line over new messages goes before the message with the ID newFrom, keyed "new:" and that
// ID. Each message is keyed by its ID, and each day's heading by "day:" and the ID of the day's first message, so
// the heading that older messages push up is a new one, and the timeline holds the message the user was reading
// still.
func Timeline(msgs []Message, now time.Time, newFrom string) []Item {
	out := make([]Item, 0, len(msgs)+len(msgs)/20)
	var prev *Message
	var groupAt time.Time
	day := ""
	for i := range msgs {
		m := &msgs[i]
		if d := m.At.Format(time.DateOnly); d != day {
			day, prev = d, nil
			out = append(out, Item{Key: "day:" + m.ID, Day: DayName(m.At, now)})
		}
		if newFrom != "" && m.ID == newFrom {
			out = append(out, Item{Key: "new:" + m.ID, New: true})
			prev = nil
		}
		it := Item{Key: m.ID, Message: *m}
		it.Continued = prev != nil && prev.Author == m.Author && m.At.Sub(groupAt) < groupFor && m.Reply.ID == "" &&
			!m.Private
		if !it.Continued {
			groupAt = m.At
		}
		out = append(out, it)
		prev = m
	}
	return out
}

// DayName names the day of t, as seen at now: today, yesterday, or its weekday and date.
func DayName(t, now time.Time) string {
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "Today"
	case t.Add(24*time.Hour).Format(time.DateOnly) == now.Format(time.DateOnly):
		return "Yesterday"
	}
	return t.Format("Monday 2 January")
}
