package tui

import (
	"strings"
	"testing"
)

// inputScreens opens every free-text screen of the panel and says how to
// leave it: the key that submits, and the screen esc goes back to.
func TestEveryInputEnterIsNewlineCtrlDSubmitsEscLeaves(t *testing.T) {
	cases := []struct {
		name string
		open func(d *drv)
		scr  screen
		ta   func(m *Model) string
		back screen
	}{
		{"free", func(d *drv) {
			d.ask("7", "s1", "Q?", "a", "b")
			d.poll()
			d.keys("enter", "enter", "f")
		}, scFree, func(m *Model) string { return m.ta.Value() }, scWalk},
		{"reply", func(d *drv) {
			d.addNote("707", "merged", "body")
			d.poll()
			d.keys("enter", "f")
		}, scReply, func(m *Model) string { return m.ta.Value() }, scNote},
		{"chief", func(d *drv) { d.keys("c") }, scChief, func(m *Model) string { return m.chiefTA.Value() }, scList},
		{"review note", func(d *drv) { d.reviewAtThree(); d.keys("n") }, scReviewNote, func(m *Model) string { return m.ti.Value() }, scReview},
		{"refile", func(d *drv) {
			d.addNote("", "loose", "")
			d.poll()
			d.cursorTo("loose")
			d.keys("T", "ctrl+u")
		}, scRefile, func(m *Model) string { return m.ti.Value() }, scList},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := newDrv(t, 92, 29)
			c.open(d)
			if d.m.scr != c.scr {
				t.Fatalf("screen %d, want %d\n%s", d.m.scr, c.scr, d.view())
			}
			d.keys("one", "enter", "two")
			if got := c.ta(d.m); got != "one\ntwo" {
				t.Fatalf("enter did not add a line: %q", got)
			}
			if d.m.scr != c.scr || d.m.busy != "" {
				t.Fatalf("enter left the screen (%d, busy %q)", d.m.scr, d.m.busy)
			}
			if !strings.Contains(d.view(), "ctrl+d") || !strings.Contains(d.view(), "enter newline") {
				t.Errorf("help line lacks ctrl+d / enter newline:\n%s", d.view())
			}
			d.keys("esc")
			if d.m.scr == c.scr {
				t.Fatalf("esc stayed on the input\n%s", d.view())
			}
		})
	}
}

func TestCtrlDSubmitsEachNoteInput(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.reviewAtThree()
	d.keys("n", "line one", "enter", "line two", "ctrl+d")
	if len(d.am.sends) != 1 || d.get("7.1").Note != "line one\nline two" {
		t.Fatalf("sends %v note %q", d.am.sends, d.get("7.1").Note)
	}
}

func TestInputWrapsAt92Columns(t *testing.T) {
	d := newDrv(t, 120, 29)
	d.reviewAtThree()
	d.keys("n", strings.Repeat("word ", 40))
	d.fits()
	for _, l := range strings.Split(d.view(), "\n") {
		if !strings.HasPrefix(l, "┃") {
			continue
		}
		if w := len([]rune(strings.TrimRight(l, " "))); w > inputCols {
			t.Fatalf("line of %d columns:\n%s", w, d.view())
		}
	}
	if n := strings.Count(d.view(), "word"); n != 40 {
		t.Fatalf("wrapped text lost words (%d of 40):\n%s", n, d.view())
	}
	if !strings.Contains(d.view(), "┃ word word") || strings.Count(d.view(), "┃") < 3 {
		t.Fatalf("not wrapped over several rows:\n%s", d.view())
	}
}
