package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
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
	if d.m.scr != scJump {
		t.Fatalf("esc in the walk should open the list, got %d", d.m.scr)
	}
	d.keys("esc")
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
	for _, want := range []string{"● 7.1  Which way? → a: left", "– 7.3  Which way? → skipped", "› ○ 7.4  Which way?"} {
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
	d.keys("l", "enter")
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
	if !strings.Contains(d.view(), "× 7.4  Which way? · answered: a") {
		t.Fatalf("closed 7.4 not dimmed in the list:\n%s", d.view())
	}
	d.keys("esc")
	if d.m.scr != scList {
		t.Fatalf("esc on the list: screen %d", d.m.scr)
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

func TestFreeTextOnAmendedQuestionStaysOpen(t *testing.T) {
	for _, key := range []string{"esc", "ctrl+d"} {
		t.Run(key, func(t *testing.T) {
			d := newDrv(t, 92, 29)
			d.asks(2)
			d.openWalk()
			d.keys("f", "my answer")
			q := "Which road?"
			if _, err := d.other.Amend("7.1", "s1", false, inbox.AmendInput{Question: &q}); err != nil {
				t.Fatal(err)
			}
			d.poll()
			d.keys(key)
			if d.m.scr != scFree || d.m.ta.Value() != "my answer" {
				t.Fatalf("box closed or lost its text: screen %d %q", d.m.scr, d.m.ta.Value())
			}
			if !strings.Contains(d.view(), "question changed; review before saving") {
				t.Fatalf("no warning:\n%s", d.view())
			}
			if _, ok := d.m.p.staged["7.1"]; ok {
				t.Fatalf("staged against the new rev: %v", d.m.p.staged)
			}
			d.keys(key)
			if s := d.staged("7.1"); s.Text != "my answer" || s.Rev != d.get("7.1").Rev {
				t.Fatalf("second %s staged %+v", key, s)
			}
		})
	}
}

func TestFreeTextNeverSavesOntoAnotherQuestion(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.asks(2)
	d.openWalk()
	d.keys("f", "for the first")
	d.m.p.cur = "7.2"
	d.keys("ctrl+d")
	if d.m.scr != scFree || d.m.ta.Value() != "for the first" {
		t.Fatalf("box closed or lost its text: screen %d %q", d.m.scr, d.m.ta.Value())
	}
	if !strings.Contains(d.view(), "question changed; review before saving") {
		t.Fatalf("no warning:\n%s", d.view())
	}
	if len(d.m.p.staged) != 0 {
		t.Fatalf("staged onto the wrong question: %v", d.m.p.staged)
	}
}

func (d *drv) listed(wants ...string) {
	d.t.Helper()
	if d.m.scr != scJump {
		d.t.Fatalf("want the decision list, got screen %d:\n%s", d.m.scr, d.view())
	}
	v := d.view()
	for _, w := range wants {
		if !strings.Contains(v, w) {
			d.t.Fatalf("%q missing:\n%s", w, v)
		}
	}
}

func TestDecisionListBackEnterBackKeepsCursor(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.asks(4)
	d.openWalk()
	d.keys("a", "s", "f", "txt", "ctrl+d")
	d.at("7.4")
	d.keys("esc")
	d.listed("spec-s1 · decisions · 3/4 drafted", "● 7.1  Which way? → a: left", "– 7.2  Which way? → skipped",
		"● 7.3  Which way? → free text: txt", "› ○ 7.4  Which way?", "enter open · esc back")
	d.fits()
	d.keys("up", "up", "enter")
	d.at("7.2")
	if v := d.view(); !strings.Contains(v, "draft: skipped") || !strings.Contains(v, "› Skip") {
		t.Fatalf("7.2 not opened on its draft:\n%s", v)
	}
	d.keys("esc")
	d.listed("› – 7.2")
	d.keys("up", "enter")
	d.at("7.1")
	if !strings.Contains(d.view(), "› a") || !strings.Contains(d.view(), "draft: a: left") {
		t.Fatalf("7.1 not opened on its draft:\n%s", d.view())
	}
	d.keys("esc", "esc")
	if d.m.scr != scList || !strings.Contains(d.view(), "4 open · 3/4 drafted") {
		t.Fatalf("main list:\n%s", d.view())
	}
}

func TestDecisionListMarksChangedAndClosedAway(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.asks(4)
	d.openWalk()
	d.keys("a", "b", "s", "esc", "esc")
	q := "Which road?"
	if _, err := d.other.Amend("7.1", "s1", false, inbox.AmendInput{Question: &q}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.other.Withdraw("7.2", "s1", false); err != nil {
		t.Fatal(err)
	}
	d.poll()
	if !strings.Contains(d.view(), "3 open · 1/3 drafted") {
		t.Fatalf("main list:\n%s", d.view())
	}
	d.keys("enter", "enter")
	d.at("7.1")
	if !strings.Contains(d.view(), "draft: a: left · changed: review") || !strings.Contains(d.view(), "› a") {
		t.Fatalf("changed draft not shown:\n%s", d.view())
	}
	d.keys("esc")
	d.poll()
	d.listed("● 7.1  Which road? → a: left · changed: review", "– 7.3  Which way? → skipped", "○ 7.4",
		"× 7.2  Which way? · withdrawn", "1/3 drafted")
	d.fits()
}

func TestChangedDraftBlocksSendUntilReconfirmed(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.asks(2)
	d.openWalk()
	d.keys("a", "b")
	if d.m.scr != scReview {
		t.Fatalf("want review:\n%s", d.view())
	}
	q := "Which road?"
	if _, err := d.other.Amend("7.1", "s1", false, inbox.AmendInput{Question: &q}); err != nil {
		t.Fatal(err)
	}
	d.poll()
	d.at("7.1")
	d.keys("right", "right")
	d.at("7.2")
	if len(d.am.sends) != 0 || d.get("7.1").Status != "open" {
		t.Fatal("sent with a changed draft")
	}
	d.restart()
	d.openWalk()
	d.at("7.1")
	d.keys("enter")
	if d.m.scr != scReview {
		t.Fatalf("re-confirm did not reach review:\n%s", d.view())
	}
	d.keys("enter")
	if got := d.get("7.1"); got.AnswerOption != "a" || got.Question != q {
		t.Fatalf("7.1 = %+v", got)
	}
	if got, err := d.ib.Drafts(); err != nil || len(got) != 0 {
		t.Fatalf("drafts after send: %v %v", got, err)
	}
}

func TestDecisionListRebuiltFromStoreAfterRestart(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.asks(3)
	d.openWalk()
	d.keys("b", "s")
	d.restart()
	if !strings.Contains(d.view(), "3 open · 2/3 drafted") {
		t.Fatalf("main list after restart:\n%s", d.view())
	}
	d.openWalk()
	d.at("7.3")
	d.keys("esc")
	d.listed("● 7.1  Which way? → b: right", "– 7.2  Which way? → skipped", "› ○ 7.3")
}

func TestReviewAllShowsDrafts(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.asks(2)
	d.openWalk()
	d.keys("a", "esc", "esc", "enter", "r")
	if d.m.scr != scAll || !strings.Contains(d.view(), "● draft: a: left") {
		t.Fatalf("Review all:\n%s", d.view())
	}
}
