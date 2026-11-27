package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
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
	if !strings.Contains(v, "› Fakeproj ● decision ▸ #707") {
		t.Errorf("thread row lacks its lead:\n%s", v)
	}
	if strings.Contains(lineWith(v, "#808"), "Fakeproj") {
		t.Errorf("a task without a project got a chip:\n%s", v)
	}
	golden(t, "project-threads-92x29", v)
	d.m.inboxOpen = true
	d.poll()
	v = d.view()
	for _, want := range []string{"Which way?", "Task 707 merged"} {
		if !strings.Contains(lineWith(v, want), "Fakeproj ● ") {
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
	if d.m.scr != scWalk || !strings.HasPrefix(strings.Split(d.view(), "\n")[1], "Fakeproj ● decision") {
		t.Fatalf("walk header lacks the chip:\n%s", d.view())
	}
	golden(t, "project-walk-92x29", d.view())
	d.keys("esc", "esc")
	d.m.inboxOpen = true
	d.poll()
	d.cursorTo("Task 707 merged")
	d.keys("enter")
	if d.m.scr != scNote || !strings.HasPrefix(strings.Split(d.view(), "\n")[1], "Fakeproj ● note") {
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

// typesFixture is one row of each type, all on one fake project (none when empty).
func (d *drv) typesFixture(project string) {
	d.t.Helper()
	ask := func(task, sid, q string, in inbox.AskInput) {
		in.Task, in.SessionID, in.Name, in.Question = task, sid, "spec-"+sid, q
		var err error
		if in.Prod != nil {
			_, err = d.other.AskProd(in, *in.Prod)
		} else {
			_, err = d.other.Ask(in)
		}
		if err != nil {
			d.t.Fatal(err)
		}
		if err := d.other.SetProject(task, project); err != nil {
			d.t.Fatal(err)
		}
	}
	ask("11", "s1", "A decision?", inbox.AskInput{Options: []string{"yes", "no"}})
	ask("12", "s2", "A gate?", inbox.AskInput{Gate: true, Options: []string{"yes", "no"}})
	ask("13", "s3", "A human action?", inbox.AskInput{Manual: true})
	prod := prodWant()
	ask("14", "s4", "", inbox.AskInput{Prod: &prod})
	d.addNote("15", "A note", "")
	if err := d.other.SetProject("15", project); err != nil {
		d.t.Fatal(err)
	}
	d.poll()
}

func TestEveryTypeLeadsItsRowInLightAndDark(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev); lipgloss.SetHasDarkBackground(true) })
	lipgloss.SetColorProfile(termenv.ANSI256)
	for _, c := range []struct{ project, golden string }{{"Fakeproj", "rows-types"}, {"", "rows-types-noproject"}} {
		for _, dark := range []bool{true, false} {
			lipgloss.SetHasDarkBackground(dark)
			d := newDrvWith(t, 92, 29, userPanel)
			d.m.inboxOpen = true
			d.typesFixture(c.project)
			var rows []string
			for _, l := range strings.Split(d.view(), "\n") {
				if strings.Contains(l, "spec-s") || strings.Contains(l, "A note") {
					rows = append(rows, esc(l))
				}
			}
			if len(rows) != 5 {
				t.Fatalf("%d typed rows:\n%s", len(rows), d.view())
			}
			for i, typ := range []string{typeDecision, typeGate, typeHuman, typeApproval, typeNote} {
				if !strings.Contains(rows[i], typ) {
					t.Errorf("row %d lacks %q: %s", i, typ, rows[i])
				}
				if c.project == "" && strings.Contains(rows[i], "●") && typ != typeNote {
					t.Errorf("a stray ● on a row with no project: %s", rows[i])
				}
				if c.project != "" && !strings.Contains(rows[i], "Fakeproj") {
					t.Errorf("row %d lacks the project: %s", i, rows[i])
				}
			}
			name := c.golden + "-light"
			if dark {
				name = c.golden + "-dark"
			}
			golden(t, name, strings.Join(rows, "\n"))
		}
	}
}

// A long project name takes room from the title, never from the age.
func TestLongProjectNameKeepsTheAgeAt80Columns(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 80, 24)
	long := strings.Repeat("p", 16)
	title := "A fairly long note title that needs more room than the row has left"
	d.ask("21", "s1", "Q?", "a", "b")
	d.ask("22", "s2", "Q?", "a", "b")
	d.addNote("21", title, "")
	d.addNote("23", title, "")
	for _, task := range []string{"21", "22", "23"} {
		if err := d.other.SetProject(task, long); err != nil {
			t.Fatal(err)
		}
	}
	d.poll()
	v := d.view()
	d.fits()
	if !strings.Contains(lineWith(v, "#21"), " · 0s") || !strings.Contains(lineWith(v, "#21"), long+" ● decision") {
		t.Errorf("thread row lost its age:\n%s", v)
	}
	d.keys("z")
	d.fits()
	for _, l := range strings.Split(d.view(), "\n") {
		if strings.Contains(l, "note") && strings.Contains(l, "chief") && !strings.Contains(l, " · 0s") {
			t.Errorf("note row lost its age: %q", l)
		}
	}
	golden(t, "project-long-80x24", d.view())
	d.m.inboxOpen = true
	d.poll()
	golden(t, "project-long-inbox-80x24", d.view())
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
	if calls != 2 || !strings.Contains(d.view(), "Fakeproj ● decision") {
		t.Fatalf("retry after the window did not tag (calls %d):\n%s", calls, d.view())
	}
	d.poll()
	if calls != 2 {
		t.Fatalf("an answered task was asked again: %d", calls)
	}
}
