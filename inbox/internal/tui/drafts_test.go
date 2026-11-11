package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

func (d *drv) asks(n int) {
	d.t.Helper()
	for range n {
		d.ask("7", "s1", "Which way?", "left", "right")
	}
}

// restart builds a fresh panel on the same db, as a crash and relaunch would.
func (d *drv) restart() {
	d.t.Helper()
	user := d.ib.User
	w, h := d.m.w, d.m.h
	d.m = New(d.ib, d.am)
	d.ib.User = user
	d.m.after = func(time.Duration, tea.Msg) tea.Cmd { return nil }
	d.send(tea.WindowSizeMsg{Width: w, Height: h})
	d.run(d.m.Init())
}

func (d *drv) staged(id string) view.Staged {
	d.t.Helper()
	s, ok := d.m.p.staged[id]
	if !ok {
		d.t.Fatalf("%s has no draft; staged %v", id, d.m.p.staged)
	}
	return s
}

func (d *drv) at(id string) {
	d.t.Helper()
	if d.m.scr != scWalk || d.m.p.cur != id {
		d.t.Fatalf("want %s on screen, got screen %d cur %q:\n%s", id, d.m.scr, d.m.p.cur, d.view())
	}
}

func TestEscThenAnswerResumesAtFirstUndrafted(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.asks(3)
	d.openWalk()
	d.keys("b", "s", "esc")
	if d.m.scr != scList || !strings.Contains(d.m.flash, "drafts kept") {
		t.Fatalf("flash %q", d.m.flash)
	}
	d.keys("enter", "enter")
	d.at("7.3")
	if d.staged("7.1").Option != "b" || d.staged("7.2").Kind != "skip" {
		t.Fatalf("%v", d.m.p.staged)
	}
	d.keys("a")
	if d.m.scr != scReview || !strings.Contains(d.view(), "7.2  Which way? → skipped") {
		t.Fatalf("review:\n%s", d.view())
	}
	d.keys("esc", "enter", "enter")
	if d.m.scr != scReview {
		t.Fatalf("all drafted should reopen the review, got %d", d.m.scr)
	}
	for _, id := range []string{"7.1", "7.2", "7.3"} {
		if d.get(id).Status != "open" {
			t.Fatalf("%s recorded before Send", id)
		}
	}
}

func TestLeftGoesBackWithDraftSelected(t *testing.T) {
	for _, back := range []string{"left", "shift+tab"} {
		d := newDrv(t, 92, 29)
		d.asks(5)
		d.openWalk()
		d.keys("a", "b", "a", "b")
		d.at("7.5")
		d.keys(back)
		d.at("7.4")
		if d.m.cursor != 1 || !strings.Contains(d.view(), "› b") {
			t.Fatalf("%s: cursor %d:\n%s", back, d.m.cursor, d.view())
		}
		d.keys(back, "right")
		d.at("7.4")
		if d.staged("7.3").Option != "a" || d.staged("7.4").Option != "b" {
			t.Fatalf("right changed drafts: %v", d.m.p.staged)
		}
		d.keys("right")
		d.at("7.5")
		d.keys("left", "a")
		d.at("7.5")
		if d.staged("7.4").Option != "a" {
			t.Fatalf("re-pick not kept: %v", d.m.p.staged)
		}
	}
}

func TestJumpListEditsThenReturnsToFirstUndrafted(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.asks(4)
	d.openWalk()
	d.keys("a", "a", "s", "l")
	if d.m.scr != scJump || d.m.cursor != 3 {
		t.Fatalf("screen %d cursor %d", d.m.scr, d.m.cursor)
	}
	v := d.view()
	for _, want := range []string{"7.1  Which way? → a: left", "7.3  Which way? → skipped", "7.4  Which way? → —"} {
		if !strings.Contains(v, want) {
			t.Fatalf("%q missing:\n%s", want, v)
		}
	}
	d.fits()
	d.keys("up", "up", "enter")
	d.at("7.2")
	d.keys("b")
	d.at("7.4")
	if d.staged("7.2").Option != "b" {
		t.Fatalf("%v", d.m.p.staged)
	}
	d.keys("l", "esc")
	d.at("7.4")
	d.keys("l")
	d.poll()
	if d.m.scr != scJump {
		t.Fatalf("a poll closed the jump list opened from the walk:\n%s", d.view())
	}
	if _, err := d.other.Answer("7.4", ptr("a"), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	d.poll()
	d.keys("esc")
	if d.m.scr != scReview {
		t.Fatalf("esc after the current question closed: screen %d cur %q", d.m.scr, d.m.p.cur)
	}
}

func TestRestartResumesWithDrafts(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.asks(3)
	d.openWalk()
	d.keys("b", "f", "line one", "ctrl+d")
	d.restart()
	d.openWalk()
	d.at("7.3")
	if d.staged("7.1").Option != "b" || d.staged("7.2").Text != "line one" {
		t.Fatalf("%v", d.m.p.staged)
	}
	d.keys("a", "enter")
	if !strings.HasPrefix(d.m.flash, "sent 7.1, 7.2, 7.3") {
		t.Fatalf("flash %q", d.m.flash)
	}
	if got, err := d.ib.Drafts(); err != nil || len(got) != 0 {
		t.Fatalf("drafts after send: %v %v", got, err)
	}
}

func TestFreeTextSurvivesLeaving(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.asks(2)
	d.openWalk()
	d.keys("f", "keep this", "ctrl+d", "esc")
	d.restart()
	d.openWalk()
	d.at("7.2")
	d.keys("left")
	d.at("7.1")
	if items := questionMenu(d.get("7.1"), false); items[d.m.cursor].value != "free" {
		t.Fatalf("cursor on %q", items[d.m.cursor].value)
	}
	d.keys("f")
	if d.m.scr != scFree || d.m.ta.Value() != "keep this" {
		t.Fatalf("free text reopened with %q", d.m.ta.Value())
	}
}

func TestSendClearsSkipDrafts(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.asks(2)
	d.openWalk()
	d.keys("a", "s", "enter")
	if got, _ := d.ib.Drafts(); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	d.poll()
	d.keys("enter", "enter")
	d.at("7.2")
}

func TestProdApproveAskedAgainAfterRestart(t *testing.T) {
	d := newDrvWith(t, 92, 29, func(d *drv) { d.ib.User = true })
	id := d.askProd("s1")
	d.openWalk()
	d.keys("a", "y")
	if d.m.scr != scReview {
		t.Fatalf("y did not reach review:\n%s", d.view())
	}
	d.keys("esc")
	d.restart()
	d.openWalk()
	d.at(id)
	if len(d.m.p.staged) != 0 {
		t.Fatalf("%v", d.m.p.staged)
	}
	d.keys("a")
	if d.m.scr != scConfirm {
		t.Fatal("Approve after restart skipped its confirm")
	}
}

func TestRightOnLastStaysUnlessAllDrafted(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.asks(3)
	d.openWalk()
	d.keys("a", "right", "right")
	d.at("7.3")
	d.keys("a")
	d.at("7.2")
	d.keys("right", "right")
	d.at("7.3")
	d.keys("left", "s")
	if d.m.scr != scReview {
		t.Fatalf("expected review, got %d", d.m.scr)
	}
	d.keys("left")
	d.at("7.3")
	d.keys("right")
	if d.m.scr != scReview {
		t.Fatalf("→ on the last drafted question should open the review, got %d", d.m.scr)
	}
}

func TestStagedProdApproveIsNotCalledADraft(t *testing.T) {
	d := newDrvWith(t, 92, 29, func(d *drv) { d.ib.User = true })
	d.askProd("s1")
	d.openWalk()
	d.keys("a", "y", "esc")
	if strings.Contains(d.m.flash, "drafts kept") {
		t.Fatalf("flash %q", d.m.flash)
	}
}
