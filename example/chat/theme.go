package main

import (
	"image/color"

	"github.com/marrasen/gunim/chat"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

func darkTheme() theme.Theme { return widget.Dark() }

func lightTheme() theme.Theme {
	return widget.Light().With(
		theme.Set(chat.RailFill, color.NRGBA{R: 0xdf, G: 0xe3, B: 0xea, A: 0xff}),
		theme.Set(chat.SidebarFill, color.NRGBA{R: 0xe9, G: 0xec, B: 0xf2, A: 0xff}),
		theme.Set(chat.SidebarHot, color.NRGBA{A: 0x0c}),
		theme.Set(chat.SidebarOn, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x26}),
		theme.Set(chat.PaneFill, color.NRGBA{R: 0xfa, G: 0xfb, B: 0xfd, A: 0xff}),
		theme.Set(chat.RowHot, color.NRGBA{A: 0x08}),
		theme.Set(chat.QuoteFill, color.NRGBA{A: 0x0a}),
		theme.Set(chat.Faint, color.NRGBA{R: 0x5f, G: 0x67, B: 0x75, A: 0xff}),
		theme.Set(chat.ErrorInk, color.NRGBA{R: 0xb4, G: 0x2a, B: 0x22, A: 0xff}),
		theme.Set(chat.WarnFill, color.NRGBA{R: 0xfb, G: 0xe8, B: 0xc0, A: 0xff}),
		theme.Set(widget.TextSize, 14),
		theme.Set(widget.ButtonHeight, 34),
		theme.Set(widget.ButtonPadding, 16),
		theme.Set(widget.ButtonRadius, 8),
		theme.Set(widget.FieldRadius, 8),
	)
}
