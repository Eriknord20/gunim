package filemanager

import (
	"strings"
	"testing"

	"github.com/marrasen/gunim/access"
)

// interactive reports whether a screen reader user can do something with
// n.
func interactive(n *access.Node) bool {
	switch n.Role {
	case access.RoleButton, access.RoleCheckbox, access.RoleSwitch, access.RoleSlider, access.RoleTextField,
		access.RoleTab, access.RoleMenuItem, access.RoleComboBox, access.RoleLink, access.RoleRow,
		access.RoleColumnHeader, access.RoleScrollBar, access.RoleRadioButton:
		return true
	case access.RoleGroup, access.RoleWindow, access.RoleLabel, access.RoleHeading, access.RoleImage,
		access.RoleList, access.RoleListItem, access.RoleTabList, access.RoleMenu, access.RoleDialog,
		access.RoleTooltip, access.RoleScrollArea, access.RoleTable, access.RoleCell, access.RoleMenuBar,
		access.RoleProgressBar, access.RoleStatus, access.RoleAlert:
	}
	return len(n.Actions) > 0
}

// unnamed lists the interactive nodes under n that have no name.
func unnamed(n *access.Node, path string, out *[]string) {
	path += "/" + n.Role.String()
	if interactive(n) && strings.TrimSpace(n.Name) == "" {
		*out = append(*out, path)
	}
	for _, k := range n.Children {
		unnamed(k, path, out)
	}
}

// find returns the first node under n with role and a name holding name.
func findNode(n *access.Node, role access.Role, name string) *access.Node {
	if n.Role == role && strings.Contains(n.Name, name) {
		return n
	}
	for _, k := range n.Children {
		if f := findNode(k, role, name); f != nil {
			return f
		}
	}
	return nil
}

func TestEveryControlHasANameAndTheListingIsATable(t *testing.T) {
	h := newHarness(t, "report.txt", "notes/")
	h.w.Offscreen().ListenForAccess()
	h.until("the rows and the places arrive", func() bool { return len(h.shown()) == 2 && len(h.b.side.placeKeys()) > 0 })
	h.frames(30)
	tree := h.w.Offscreen().AccessTree()
	var missing []string
	unnamed(tree.Root, "", &missing)
	if len(missing) > 0 {
		t.Fatalf("these have no name:\n%s", strings.Join(missing, "\n"))
	}
	table := findNode(tree.Root, access.RoleTable, "")
	if table == nil {
		t.Fatal("the listing is not a table")
	}
	for _, want := range []struct {
		role access.Role
		name string
	}{
		{access.RoleColumnHeader, "Name"}, {access.RoleRow, "notes"}, {access.RoleRow, "report.txt"},
		{access.RoleCell, "report.txt"},
	} {
		if findNode(table, want.role, want.name) == nil {
			t.Fatalf("the table has no %s %q", want.role, want.name)
		}
	}
	for _, want := range []struct {
		role access.Role
		name string
	}{
		{access.RoleButton, "Back"}, {access.RoleButton, "Up"}, {access.RoleMenuItem, "File"},
		{access.RoleTextField, "Filter this folder"}, {access.RoleLink, "Home"},
	} {
		if findNode(tree.Root, want.role, want.name) == nil {
			t.Fatalf("the window has no %s %q", want.role, want.name)
		}
	}
	// A screen reader selects a row by pressing it.
	sub := findNode(tree.Root, access.RoleRow, "notes")
	h.w.Input(access.Request{ID: sub.ID, Action: access.ActionPress})
	h.until("pressing the row selects it", func() bool { return h.a.nav.sel["notes"] })
}
