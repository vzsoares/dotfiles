package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

// reviewFixture is a session with a plain ask, a recommended one and a prod ask.
func (d *drv) reviewFixture() (string, string, string) {
	d.t.Helper()
	a := d.ask("7", "s1", "Which way?", "left", "right")
	b, err := d.other.Ask(inbox.AskInput{Task: "7", Question: "Which store?", Options: []string{"sqlite", "files"},
		Rec: "b", Context: "Both work.\nFiles are simpler.", SessionID: "s1", Name: "spec-s1"})
	if err != nil {
		d.t.Fatal(err)
	}
	p := d.askProd("s1")
	d.poll()
	return a, b.ID, p
}

func TestReviewAllShowsTheWholeSet(t *testing.T) {
	d := newDrv(t, 92, 29)
	a, b, p := d.reviewFixture()
	d.keys("enter", "r")
	if d.m.scr != scAll {
		t.Fatalf("screen %d\n%s", d.m.scr, d.view())
	}
	d.fits()
	v := d.view()
	if !strings.HasPrefix(v, "cockpit") || !strings.Contains(v, "Review all · spec-s1 · 3 open") {
		t.Fatalf("top rows cut:\n%s", v)
	}
	for _, want := range []string{a, b, p, "Which way?", "Which store?", "│ Both work.", "a) left", "b) files  ← rec",
		"action ", "release", "repo   dotfiles", "branch prod", "sha", prodSha, "› " + a} {
		if !strings.Contains(v, want) {
			t.Errorf("%q missing:\n%s", want, v)
		}
	}
	golden(t, "review-all-92x29", v)
}

func TestReviewAllRunAskShowsCommand(t *testing.T) {
	d := newDrv(t, 92, 29)
	if _, err := d.other.AskProd(inbox.AskInput{Task: "680", SessionID: "s1", Name: "spec-s1"},
		inbox.ProdAction{Action: inbox.RunAction, Repo: "dotfiles", Cmd: "just deploy --stage prod"}); err != nil {
		t.Fatal(err)
	}
	d.poll()
	d.keys("enter", "r")
	if v := d.view(); !strings.Contains(v, "command") || !strings.Contains(v, "just deploy --stage prod") {
		t.Fatalf("no command:\n%s", v)
	}
}

func TestReviewAllEnterWalksFromHighlighted(t *testing.T) {
	d := newDrv(t, 92, 29)
	_, b, _ := d.reviewFixture()
	d.keys("enter", "r", "down", "enter")
	if d.m.scr != scWalk || d.m.p.cur != b {
		t.Fatalf("screen %d cur %q\n%s", d.m.scr, d.m.p.cur, d.view())
	}
	d.keys("esc")
	d.keys("enter", "r", "esc")
	if d.m.scr != scSession || d.m.cursor != 1 {
		t.Fatalf("esc: screen %d cursor %d", d.m.scr, d.m.cursor)
	}
}

func TestReviewAllScrollsAtEverySize(t *testing.T) {
	for _, sz := range sizes {
		d := newDrv(t, sz[0], sz[1])
		d.reviewFixture()
		d.keys("enter", "r")
		d.fits()
		if !strings.HasPrefix(d.view(), "cockpit") || !strings.Contains(d.view(), "Review all") {
			t.Fatalf("%v: top cut:\n%s", sz, d.view())
		}
		d.keys("down", "down")
		d.fits()
		if !strings.Contains(d.view(), "› "+d.m.sessionOpen()[2].ID) {
			t.Fatalf("%v: highlight left the screen:\n%s", sz, d.view())
		}
	}
}

func TestTalkPickSendsOnlyPickedAndClosesNothing(t *testing.T) {
	d := newDrv(t, 92, 29)
	a, b, p := d.reviewFixture()
	d.keys("enter", "r", "t")
	if d.m.scr != scTalkWho {
		t.Fatalf("screen %d", d.m.scr)
	}
	d.fits()
	golden(t, "talk-pick-92x29", d.view())
	d.keys("down", " ", "down", " ", "enter")
	if len(d.am.sends) != 1 || d.am.sends[0][0] != "s1" || !slices.Equal(d.am.popups, []string{"s1"}) {
		t.Fatalf("sends %v popups %v", d.am.sends, d.am.popups)
	}
	msg := d.am.sends[0][1]
	if strings.Contains(msg, a) || !strings.Contains(msg, b+": Which store?") || !strings.Contains(msg, p) {
		t.Fatalf("message %q", msg)
	}
	if d.m.scr != scTalkPick || len(d.m.marked) != 0 {
		t.Fatalf("settle pick: screen %d marked %v", d.m.scr, d.m.marked)
	}
	d.keys("enter")
	for _, id := range []string{a, b, p} {
		if d.get(id).Status != "open" {
			t.Fatalf("%s closed by the talk", id)
		}
	}
}

func TestTalkPickNoneMarkedMeansAll(t *testing.T) {
	d := newDrv(t, 92, 29)
	a, b, p := d.reviewFixture()
	d.keys("enter", "t", "enter")
	msg := d.am.sends[0][1]
	for _, id := range []string{a, b, p} {
		if !strings.Contains(msg, id+": ") {
			t.Fatalf("%s missing: %q", id, msg)
		}
	}
}

func TestTalkPickEscCancelsWithoutSending(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.reviewFixture()
	d.keys("enter", "r", "down", "t", "esc")
	if d.m.scr != scAll || d.m.cursor != 1 || len(d.am.sends) != 0 || len(d.am.popups) != 0 {
		t.Fatalf("screen %d cursor %d sends %v", d.m.scr, d.m.cursor, d.am.sends)
	}
	d.keys("enter", "t", "esc")
	if d.m.scr != scWalk {
		t.Fatalf("walk talk cancel: screen %d", d.m.scr)
	}
}

func TestTalkStillOpensWhenTheAgentCannotBeTold(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.reviewFixture()
	d.am.fail = "session busy"
	d.keys("enter", "t", "enter", "esc")
	if d.m.scr != scList || !strings.Contains(d.m.flash, "agent not told: session busy") {
		t.Fatalf("screen %d flash %q", d.m.scr, d.m.flash)
	}
}

func TestTalkMessageNamesThePickedQuestions(t *testing.T) {
	got := talkMessage([]inbox.Decision{
		{ID: "7.1", Question: "Which   way?\nSecond line"},
		{ID: "7.2", Question: strings.Repeat("x", 300)},
	})
	lines := strings.Split(got, "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "opening a talk") || !strings.Contains(lines[0], "2 open") ||
		lines[1] != "- 7.1: Which way? Second line" || !strings.HasPrefix(lines[2], "- 7.2: xxx") || len(lines[2]) > 220 {
		t.Fatalf("%q", got)
	}
}
