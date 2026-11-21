package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

// jkScrolls checks J moves the body 3 lines and K back, K clamps at 0, J at
// the end, and the pick cursor stays put.
func jkScrolls(t *testing.T, d *drv, scr screen) {
	t.Helper()
	d.fits()
	if d.m.scr != scr {
		t.Fatalf("on screen %v, want %v:\n%s", d.m.scr, scr, d.view())
	}
	cursor := d.m.cursor
	d.keys("K")
	if d.m.scroll != 0 {
		t.Fatalf("K at the top scrolled to %d", d.m.scroll)
	}
	d.keys("J")
	if d.m.scroll != 3 {
		t.Fatalf("J scrolled to %d, want 3", d.m.scroll)
	}
	d.keys("K")
	if d.m.scroll != 0 {
		t.Fatalf("K scrolled to %d, want 0", d.m.scroll)
	}
	for range 100 {
		d.keys("J")
	}
	end := d.m.scroll
	if end <= 3 {
		t.Fatalf("J never reached the end: %d", end)
	}
	d.keys("J")
	if d.m.scroll != end {
		t.Fatalf("J past the end moved %d to %d", end, d.m.scroll)
	}
	if d.m.cursor != cursor || d.m.scr != scr {
		t.Fatalf("cursor %d -> %d, screen %v", cursor, d.m.cursor, d.m.scr)
	}
}

func longLines(prefix string, n int) string {
	l := make([]string, n)
	for i := range l {
		l[i] = prefix + " " + strconv.Itoa(i+1)
	}
	return strings.Join(l, "\n")
}

func TestJKScrollSession(t *testing.T) {
	d := newDrv(t, 92, 30)
	if _, err := d.other.Ask(inbox.AskInput{Task: "669", Question: "Scroll?", Options: []string{"yes", "no"},
		Context: longLines("context line", 40), SessionID: "s1", Name: "spec-s1"}); err != nil {
		t.Fatal(err)
	}
	d.poll()
	d.keys("enter")
	jkScrolls(t, d, scSession)
}

func TestJKScrollWalk(t *testing.T) {
	d := newDrv(t, 40, 15)
	d.ask("7", "s1", "Which way?", words(150), words(150), words(150))
	d.openWalk()
	jkScrolls(t, d, scWalk)
}

func TestJKScrollFull(t *testing.T) {
	d := newDrv(t, 40, 15)
	d.ask("7", "s1", "Which way?", words(150), words(150), words(150))
	d.openWalk()
	d.keys("v")
	jkScrolls(t, d, scFull)
}

func TestJKScrollNote(t *testing.T) {
	d := newDrv(t, 60, 20)
	d.addNote("707", "Long report", words(1500))
	d.poll()
	d.keys("enter")
	jkScrolls(t, d, scNote)
}

func TestJKScrollThread(t *testing.T) {
	d := newDrv(t, 60, 14)
	d.fixture843()
	d.openThread843()
	d.keys("g")
	jkScrolls(t, d, scThread)
}

func TestJKScrollConfirm(t *testing.T) {
	d := newDrvWith(t, 60, 12, userPanel)
	d.askManual(longLines("- step", 60))
	d.openWalk()
	d.keys("a")
	jkScrolls(t, d, scConfirm)
}

func TestJKTypeIntoInputScreens(t *testing.T) {
	d := newDrv(t, 60, 20)
	d.ask("7", "s1", "Which way?", "yes", "no")
	d.openWalk()
	d.keys("f", "J", "K")
	if d.m.scr != scFree || d.m.scroll != 0 || !strings.Contains(d.text(), "JK") {
		t.Fatalf("J/K did not type into free text:\n%s", d.view())
	}
}
