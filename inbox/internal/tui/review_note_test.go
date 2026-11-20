package tui

import (
	"strings"
	"testing"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

// reviewAtThree walks three asks of one task to the review screen.
func (d *drv) reviewAtThree() {
	d.t.Helper()
	for range 3 {
		d.ask("7", "s1", "Which way?", "left", "right")
	}
	d.poll()
	d.keys("enter", "enter", "a", "b", "a")
	if d.m.scr != scReview {
		d.t.Fatalf("not at review:\n%s", d.view())
	}
}

func TestSendWithNoteStoresAndDeliversOnce(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.reviewAtThree()
	d.keys("n")
	if d.m.scr != scReviewNote {
		t.Fatalf("screen %d\n%s", d.m.scr, d.view())
	}
	d.keys("ship it after lunch", "ctrl+d")
	for _, id := range []string{"7.1", "7.2", "7.3"} {
		if got := d.get(id); got.Note != "ship it after lunch" || got.Status != "answered" {
			t.Errorf("%s = %+v", id, got)
		}
	}
	if len(d.am.sends) != 1 {
		t.Fatalf("sends %v", d.am.sends)
	}
	msg := d.am.sends[0][1]
	if n := strings.Count(msg, "ship it after lunch"); n != 1 {
		t.Errorf("note appears %d times:\n%s", n, msg)
	}
	if !strings.Contains(msg, "Note: ship it after lunch") || !strings.Contains(msg, "7.3 Which way? → a: left") {
		t.Errorf("message:\n%s", msg)
	}
}

func TestSendWithEmptyNoteIsPlain(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.reviewAtThree()
	d.keys("down", "enter", "ctrl+d")
	if d.get("7.1").Note != "" || len(d.am.sends) != 1 || strings.Contains(d.am.sends[0][1], "Note:") {
		t.Fatalf("sends %v note %q", d.am.sends, d.get("7.1").Note)
	}
}

func TestReviewNoteEscReturnsToReview(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.reviewAtThree()
	d.keys("n", "draft", "esc")
	if d.m.scr != scReview || len(d.am.sends) != 0 || d.get("7.1").Status != "open" {
		t.Fatalf("screen %d sends %v\n%s", d.m.scr, d.am.sends, d.view())
	}
}

func TestReviewNoteRender92x29(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.reviewAtThree()
	v := d.view()
	for _, want := range []string{"Send with note", "n note"} {
		if !strings.Contains(v, want) {
			t.Errorf("review lacks %q:\n%s", want, v)
		}
	}
	d.fits()
	golden(t, "review-92x29", v)
	d.keys("n", "one note for the lot")
	d.fits()
	v = d.view()
	if !strings.Contains(v, "note for all 3 answers") || !strings.Contains(v, "one note for the lot") {
		t.Errorf("note screen:\n%s", v)
	}
	golden(t, "review-note-92x29", v)
}

func TestFailedSendKeepsNoteForRetry(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.reviewAtThree()
	d.am.fail = "boom"
	d.keys("n", "keep me", "ctrl+d")
	for _, id := range []string{"7.1", "7.2", "7.3"} {
		if got := d.get(id); got.Note != "keep me" || got.Delivery != "failed" {
			t.Errorf("%s = %+v", id, got)
		}
	}
}

func TestAmendedSendRefillsNote(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.reviewAtThree()
	q := "Which road?"
	if _, err := d.other.Amend("7.3", "s1", false, inbox.AmendInput{Question: &q, Options: []string{"up", "down"}}); err != nil {
		t.Fatal(err)
	}
	d.keys("n", "keep me", "ctrl+d")
	if d.m.scr != scWalk {
		t.Fatalf("screen %d\n%s", d.m.scr, d.view())
	}
	d.keys("a", "n")
	if d.m.scr != scReviewNote || d.m.ti.Value() != "keep me" {
		t.Fatalf("screen %d note %q", d.m.scr, d.m.ti.Value())
	}
}
