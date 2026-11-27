package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// projectFixture is an ask and a note on one task of a fake project, plus an
// ask on a task the board knows no project for.
func (d *drv) projectFixture() {
	d.t.Helper()
	d.ask("707", "s1", "Which way?", "left", "right")
	d.ask("808", "s2", "Untagged one?", "yes", "no")
	d.addNote("707", "Task 707 merged", "Tag v1.2.3.")
	if err := d.other.SetProject("707", "Fakeproj"); err != nil {
		d.t.Fatal(err)
	}
	if err := d.other.SetProject("808", ""); err != nil {
		d.t.Fatal(err)
	}
	d.poll()
}

func TestProjectChipLeadsListRowsAndHeaders(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 29)
	d.projectFixture()
	v := d.view()
	if !strings.Contains(v, "› Fakeproj  ▸ #707") && !strings.Contains(v, " Fakeproj  ▸ #707") {
		t.Errorf("thread row lacks the chip:\n%s", v)
	}
	if strings.Contains(lineWith(v, "#808"), "Fakeproj") {
		t.Errorf("a task without a project got a chip:\n%s", v)
	}
	golden(t, "project-threads-92x29", v)
	d.m.inboxOpen = true
	d.poll()
	v = d.view()
	for _, want := range []string{"Which way?", "Task 707 merged"} {
		if !strings.Contains(lineWith(v, want), " Fakeproj ") {
			t.Errorf("row %q lacks the chip:\n%s", want, v)
		}
	}
	if strings.Contains(lineWith(v, "Untagged one?"), "Fakeproj") {
		t.Errorf("untagged row got a chip:\n%s", v)
	}
	golden(t, "project-inbox-92x29", v)
	d.cursorTo("Which way?")
	d.keys("enter", "enter")
	d.fits()
	if d.m.scr != scWalk || !strings.Contains(strings.Split(d.view(), "\n")[1], " Fakeproj ") {
		t.Fatalf("walk header lacks the chip:\n%s", d.view())
	}
	golden(t, "project-walk-92x29", d.view())
	d.keys("esc", "esc")
	d.m.inboxOpen = true
	d.poll()
	d.cursorTo("Task 707 merged")
	d.keys("enter")
	if d.m.scr != scNote || !strings.HasPrefix(strings.Split(d.view(), "\n")[1], " Fakeproj ") {
		t.Fatalf("note header lacks the chip:\n%s", d.view())
	}
	golden(t, "project-note-92x29", d.view())
}

func lineWith(v, text string) string {
	for _, l := range strings.Split(v, "\n") {
		if strings.Contains(l, text) {
			return l
		}
	}
	return ""
}

func esc(s string) string { return strings.ReplaceAll(s, "\x1b", "␛") }

// A chip keeps its hue across runs and has dark text on a dark terminal's
// light fill and white text on a light terminal's deep fill.
func TestProjectChipLightAndDarkGolden(t *testing.T) {
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev); lipgloss.SetHasDarkBackground(true) })
	lipgloss.SetColorProfile(termenv.ANSI256)
	var out []string
	for _, dark := range []bool{true, false} {
		lipgloss.SetHasDarkBackground(dark)
		mode := map[bool]string{true: "dark", false: "light"}[dark]
		for _, name := range []string{"Fakeproj", "Otherproj", "Third", "fakeproj"} {
			chip := projectChip(name)
			if chip != projectChip(name) {
				t.Fatalf("%s chip changes between calls", name)
			}
			out = append(out, mode+" "+name+": "+esc(chip))
		}
	}
	golden(t, "project-chip-dark-light", strings.Join(out, "\n"))
}

func TestProjectChipRowsInColour(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev); lipgloss.SetHasDarkBackground(true) })
	lipgloss.SetColorProfile(termenv.ANSI256)
	for _, dark := range []bool{true, false} {
		lipgloss.SetHasDarkBackground(dark)
		d := newDrv(t, 92, 29)
		d.m.inboxOpen = true
		d.projectFixture()
		var rows []string
		for _, l := range strings.Split(d.view(), "\n") {
			if strings.Contains(l, "Which way?") {
				rows = append(rows, esc(l))
			}
		}
		if len(rows) != 1 || !strings.Contains(rows[0], "Fakeproj") {
			t.Fatalf("rows %q", rows)
		}
		name := "project-row-light"
		if dark {
			name = "project-row-dark"
		}
		golden(t, name, rows[0])
	}
}

func TestProjectsResolveOffThreadAndTheBoardFailingTagsNothing(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 29)
	d.m.inboxOpen = true
	d.ask("909", "s1", "Still listed?", "yes", "no")
	calls := 0
	boardUp := false
	d.m.lookup = func(task string) (string, error) {
		calls++
		if !boardUp {
			return "", errors.New("zen-board: HTTP 500")
		}
		return "Fakeproj", nil
	}
	clock := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	d.m.now = func() time.Time { return clock }
	d.poll()
	if calls != 1 {
		t.Fatalf("board asked %d times, want 1", calls)
	}
	v := d.view()
	if !strings.Contains(v, "Still listed?") || strings.Contains(v, "Fakeproj") || d.m.flash != "" {
		t.Fatalf("a failing board changed the panel (flash %q):\n%s", d.m.flash, v)
	}
	d.poll()
	if calls != 1 {
		t.Fatalf("board asked again inside the retry window: %d", calls)
	}
	boardUp, clock = true, clock.Add(ProjectRetry)
	d.poll()
	if calls != 2 || !strings.Contains(d.view(), " Fakeproj ") {
		t.Fatalf("retry after the window did not tag (calls %d):\n%s", calls, d.view())
	}
	d.poll()
	if calls != 2 {
		t.Fatalf("an answered task was asked again: %d", calls)
	}
}
