package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestGJumpsListCursors(t *testing.T) {
	three := func(d *drv) {
		for range 3 {
			d.ask("7", "s1", "Which way?", "left", "right")
		}
		d.poll()
	}
	cases := []struct {
		name string
		open func(d *drv)
		scr  screen
	}{
		{"list", func(d *drv) { d.notesFixture() }, scList},
		{"session", func(d *drv) { three(d); d.keys("enter") }, scSession},
		{"walk", func(d *drv) { three(d); d.openWalk() }, scWalk},
		{"review", func(d *drv) { d.reviewAtThree() }, scReview},
		{"jump", func(d *drv) { three(d); d.openWalk(); d.keys("esc") }, scJump},
		{"all", func(d *drv) { three(d); d.keys("enter", "r") }, scAll},
		{"talk-who", func(d *drv) { three(d); d.keys("enter", "t") }, scTalkWho},
		{"talk-pick", func(d *drv) { three(d); d.keys("enter", "t", "enter") }, scTalkPick},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := newDrv(t, 92, 29)
			c.open(d)
			if d.m.scr != c.scr {
				t.Fatalf("screen %d, want %d\n%s", d.m.scr, c.scr, d.view())
			}
			last := d.m.menuLen() - 1
			if last < 1 {
				t.Fatalf("need two rows, have %d", last+1)
			}
			d.keys("G")
			if d.m.scr != c.scr || d.m.cursor != last {
				t.Fatalf("G: screen %d cursor %d, want %d", d.m.scr, d.m.cursor, last)
			}
			d.keys("g")
			if d.m.scr != c.scr || d.m.cursor != 0 {
				t.Fatalf("g: screen %d cursor %d, want 0", d.m.scr, d.m.cursor)
			}
			d.keys("G", "g", "g")
			if d.m.cursor != 0 {
				t.Fatalf("gg: cursor %d", d.m.cursor)
			}
		})
	}
}

func TestGJumpsBodyScroll(t *testing.T) {
	cases := []struct {
		name string
		open func(d *drv)
		scr  screen
		w, h int
	}{
		{"full", func(d *drv) {
			d.ask("7", "s1", "Which way?", words(150), words(150), words(150))
			d.openWalk()
			d.keys("v")
		}, scFull, 40, 15},
		{"note", func(d *drv) {
			d.addNote("707", "Long report", words(1500))
			d.poll()
			d.keys("enter")
		}, scNote, 60, 20},
		{"thread", func(d *drv) { d.fixture843(); d.openThread843() }, scThread, 60, 14},
		{"confirm", func(d *drv) {
			d.askManual(longLines("- step", 60))
			d.openWalk()
			d.keys("a")
		}, scConfirm, 60, 12},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := newDrvWith(t, c.w, c.h, userPanel)
			c.open(d)
			d.fits()
			if d.m.scr != c.scr {
				t.Fatalf("screen %d, want %d\n%s", d.m.scr, c.scr, d.view())
			}
			for range 100 {
				d.keys("J")
			}
			end := d.m.scroll
			if end <= 3 {
				t.Fatalf("J never reached the end: %d", end)
			}
			d.keys("g")
			if d.m.scr != c.scr || d.m.scroll != 0 {
				t.Fatalf("g: screen %d scroll %d", d.m.scr, d.m.scroll)
			}
			d.keys("G")
			if d.m.scr != c.scr || d.m.scroll < end {
				t.Fatalf("G: screen %d scroll %d, want >= %d", d.m.scr, d.m.scroll, end)
			}
			d.keys("J")
			if d.m.scroll < end {
				t.Fatalf("scroll fell back to %d after G", d.m.scroll)
			}
		})
	}
}

func TestGTypesIntoInputScreens(t *testing.T) {
	cases := []struct {
		name string
		open func(d *drv)
		scr  screen
		val  func(m *Model) string
	}{
		{"free", func(d *drv) { d.ask("7", "s1", "Q?", "a", "b"); d.poll(); d.keys("enter", "enter", "f") }, scFree, func(m *Model) string { return m.ta.Value() }},
		{"reply", func(d *drv) { d.addNote("707", "merged", "body"); d.poll(); d.keys("enter", "f") }, scReply, func(m *Model) string { return m.ta.Value() }},
		{"chief", func(d *drv) { d.keys("c") }, scChief, func(m *Model) string { return m.chiefTA.Value() }},
		{"review note", func(d *drv) { d.reviewAtThree(); d.keys("n") }, scReviewNote, func(m *Model) string { return m.ti.Value() }},
		{"refile", func(d *drv) {
			d.addNote("", "loose", "")
			d.poll()
			d.cursorTo("loose")
			d.keys("T", "ctrl+u")
		}, scRefile, func(m *Model) string { return m.ti.Value() }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := newDrv(t, 92, 29)
			c.open(d)
			if d.m.scr != c.scr {
				t.Fatalf("screen %d, want %d\n%s", d.m.scr, c.scr, d.view())
			}
			d.keys("g", "G")
			if got := c.val(d.m); got != "gG" || d.m.scr != c.scr {
				t.Fatalf("typed %q on screen %d", got, d.m.scr)
			}
		})
	}
}

func TestScrollHintListsTopBottom(t *testing.T) {
	for _, scr := range []screen{scNote, scFull, scWalk, scThread, scAll} {
		got := strings.Join(footerLines(scr, 92, true), "\n")
		if !strings.Contains(got, "g/G top/bottom") {
			t.Errorf("screen %d: hint missing:\n%s", scr, got)
		}
		for _, l := range footerLines(scr, 92, true) {
			if w := lipgloss.Width(l); w > 92 {
				t.Errorf("screen %d: footer line %d wide", scr, w)
			}
		}
	}
	if got := strings.Join(footerLines(scFree, 92, true), "\n"); strings.Contains(got, "g/G") {
		t.Errorf("input screen lists g/G:\n%s", got)
	}
}
