package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestCockpitGoldens(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	cases := []struct {
		name  string
		setup func(d *drv)
		want  []string
	}{
		{"cockpit-empty-92x29", func(d *drv) {
			d.ask("7", "s1", "Q?", "a", "b")
			if _, err := d.other.Withdraw("7.1", "s1", false); err != nil {
				t.Fatal(err)
			}
			at := d.m.now()
			d.m.now = func() time.Time { return at.Add(5 * time.Minute) }
		}, []string{"cockpit · all clear", "Nothing waiting for you", "last item closed 5m ago"}},
		{"cockpit-notes-92x29", func(d *drv) {
			d.addNote("707", "Task 707 merged", "")
			d.addNote("", "Session spec-s2 died", "")
		}, []string{"cockpit · ✉ 2 unread", "Notes · 2 unread", "m read", "f reply"}},
		{"cockpit-asks-held-92x29", func(d *drv) {
			d.ask("7", "s1", "First?", "a", "b")
			d.ask("7", "s1", "Second?", "a", "b")
			d.ask("8", "s2", "Other?", "a", "b")
			if _, err := d.other.Answer("7.1", ptr("a"), nil, "", nil); err != nil {
				t.Fatal(err)
			}
		}, []string{"cockpit · 2 open · 1 held", "Asks · 2 open", "Held · ", "spec-s1: 7.1 held until 7.2", "a answer"}},
		{"cockpit-prod-92x29", func(d *drv) {
			d.ask("7", "s1", "First?", "a", "b")
			d.askProd("s2")
		}, []string{"cockpit · 2 open · 1 prod", "› PROD · spec-s2"}},
	}
	for _, c := range cases {
		d := newDrv(t, 92, 29)
		c.setup(d)
		d.poll()
		d.fits()
		v := d.view()
		if n := len(strings.Split(v, "\n")); n != 29 {
			t.Errorf("%s: %d rows, want the footer on row 29", c.name, n)
		}
		for _, w := range c.want {
			if !strings.Contains(v, w) {
				t.Errorf("%s lacks %q:\n%s", c.name, w, v)
			}
		}
		golden(t, c.name, v)
	}
}

func TestCockpitEmptyIsCentred(t *testing.T) {
	d := newDrv(t, 92, 29)
	for i, l := range strings.Split(d.view(), "\n") {
		if strings.TrimSpace(l) != "Nothing waiting for you" {
			continue
		}
		left := len(l) - len(strings.TrimLeft(l, " "))
		if right := 92 - lipgloss.Width(l); left < right-1 || left > right+1 || i < 12 || i > 15 {
			t.Fatalf("row %d, %d left / %d right: %q", i, left, right, l)
		}
		return
	}
	t.Fatalf("no empty message:\n%s", d.view())
}

func TestCockpitFooterDropsWholeKeys(t *testing.T) {
	d := newDrv(t, 120, 40)
	d.ask("7", "s1", "Q?", "a", "b")
	d.addNote("", "one", "")
	d.poll()
	full := d.last()
	for w := 120; w >= 10; w-- {
		d.send(tea.WindowSizeMsg{Width: w, Height: 40})
		got := d.last()
		if lipgloss.Width(got) > w {
			t.Fatalf("width %d: footer %q overflows", w, got)
		}
		for _, part := range strings.Split(got, " · ") {
			if !strings.Contains(full, part) {
				t.Fatalf("width %d: %q is not a whole key of %q", w, part, full)
			}
		}
		if !strings.HasPrefix(strings.TrimPrefix(got, "↑↓ · "), "enter open") {
			t.Fatalf("width %d: focused key dropped: %q", w, got)
		}
	}
}

func TestCockpitAnswerAndReplyFromList(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "Which way?", "left", "right")
	d.addNote("707", "merged", "")
	d.poll()
	d.keys("a")
	if d.m.scr != scWalk || d.m.p.cur != "7.1" {
		t.Fatalf("a did not start the walk:\n%s", d.view())
	}
	d.keys("esc", "esc")
	if d.m.scr != scList {
		t.Fatalf("esc did not return to the list:\n%s", d.view())
	}
	d.keys("down", "f")
	if d.m.scr != scReply || d.m.note.Title != "merged" {
		t.Fatalf("f did not open the reply box:\n%s", d.view())
	}
}

func TestCockpitFailedGroupComesFirst(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "Q?", "a", "b")
	d.ask("8", "s2", "Q?", "a", "b")
	if err := d.other.CloseRaw("8.1", "answered", "pick", "a", "", "failed"); err != nil {
		t.Fatal(err)
	}
	d.poll()
	v := d.view()
	if f, a := strings.Index(v, "Failed delivery"), strings.Index(v, "Asks · "); f < 0 || a < 0 || f > a {
		t.Fatalf("failed group not above asks:\n%s", v)
	}
	if !strings.Contains(d.last(), "enter retry") {
		t.Fatalf("cursor row is not the retry: %q", d.last())
	}
}

func TestCockpitCursorStaysOnItsRowWhenAProdAskArrives(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "Q?", "a", "b")
	d.ask("8", "s2", "Q?", "a", "b")
	d.poll()
	d.askProd("s2")
	d.poll()
	if !strings.Contains(d.view(), "› spec-s1") {
		t.Fatalf("cursor left spec-s1 when spec-s2 moved above it:\n%s", d.view())
	}
}
