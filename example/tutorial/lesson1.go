package main

import (
	_ "embed"
	"strconv"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Lesson 1: a label in a card.
//
// A window shows a tree of nodes. A node is anything with two methods:
// Layout, which measures it and places its children, and Paint, which
// draws it. The widget package has the everyday ones, and Row and
// Column lay them out.
//
// This lesson is static, so its view takes an empty struct and its
// update function is nil.

//go:embed lesson1.go
var lesson1Source string

var lesson1 = lesson{
	Title:  lessonTitles[0],
	File:   lessonFile(0),
	Source: lesson1Source,
	View:   "lesson1",
	Register: func(w *gunim.Window) {
		gunim.RegisterView(w, "lesson1", buildLesson1, nil)
	},
	State: func(*app) any { return struct{}{} },
}

const lesson1Intro = `A window holds a tree of nodes. Each node has two methods: **Layout** measures it and places its children, and **Paint** draws it. The engine calls both once per frame, on the window's own goroutine.

The ` + "`widget`" + ` package has the everyday nodes. ` + "`widget.Row`" + ` and ` + "`widget.Column`" + ` lay children out along a line, with the theme's gap between them. ` + "`Grow`" + ` gives one child the space the others leave. ` + "`widget.NewCard`" + ` paints a rounded panel behind its child, and ` + "`widget.NewPad`" + ` adds a margin.

Below is a card with a heading, a paragraph that wraps, a row of cards each wearing a colour of its own, and a row whose spacer pushes the two ends apart. Resize the window: the paragraph rewraps, the swatches share the width, and the row keeps its ends at the edges.`

// buildLesson1 builds the page. The card is a column: a heading, a
// paragraph, and a row with a spacer that grows to keep its ends apart.
func buildLesson1(struct{}) *page {
	heading := widget.NewLabel("Hello from gunim")
	heading.Size = widget.HeadingSize
	body := widget.NewLabel("A label wraps to the width it is given, and a column gives each child the width " +
		"it has. Give the window a narrower shape and watch this paragraph take more lines.")
	left := widget.NewLabel("Left")
	right := widget.NewLabel("Right")
	spacer := widget.NewSpacer()
	ends := widget.Row(left, spacer, right).Grow(spacer, 1)
	col := widget.Column(heading, body, swatches(), ends)
	col.Cross = widget.CrossStretch
	return newPage(0, lesson1Intro, col, lesson1Source)
}

// swatches is a row of cards, one in each lesson's colour, sharing
// the width. Each wears a theme of its own that sets only its fill;
// lesson 5 says more about that.
func swatches() *widget.Flex {
	row := widget.Row()
	for i, c := range hueColors {
		label := widget.NewLabel(strconv.Itoa(i + 1))
		label.Color = widget.ButtonStrongInk
		card := widget.NewThemed(widget.NewCard(label), theme.Make("swatch", theme.Set(widget.CardFill, c)))
		row = widget.Row(append(row.Children(), card)...)
	}
	for _, k := range row.Children() {
		row.Grow(k, 1)
	}
	return row
}
