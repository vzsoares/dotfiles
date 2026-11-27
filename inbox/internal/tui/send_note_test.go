package tui

import (
	"strings"
	"testing"
)

// noteSkipFixture answers 7.1, skips 7.2 and 7.3, and stops on the review.
func noteSkipFixture(t *testing.T) *drv {
	t.Helper()
	d := newDrv(t, 92, 29)
	for range 3 {
		d.ask("7", "s1", "Which way?", "left", "right")
	}
	d.openWalk()
	d.keys("a", "s", "s")
	if d.m.scr != scReview {
		t.Fatalf("expected review, got %d", d.m.scr)
	}
	return d
}

func (d *drv) sendNote(note string) {
	d.keys("n")
	if d.m.scr != scReviewNote {
		d.t.Fatalf("no note prompt: screen %d", d.m.scr)
	}
	d.keys(note, "ctrl+d")
}

func TestNoteSendWithSkipsDeliversNow(t *testing.T) {
	d := noteSkipFixture(t)
	d.sendNote("go ahead")
	if len(d.am.sends) != 1 {
		t.Fatalf("sends %v", d.am.sends)
	}
	msg := d.am.sends[0][1]
	for _, want := range []string{"7.1 Which way? → a: left", "Note: go ahead", "Left open: 7.2, 7.3"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	if got := d.get("7.1").Delivery; got != "sent" {
		t.Errorf("7.1 delivery %q", got)
	}
	if want := "sent 1 answer + your note to spec-s1; 2 left open"; d.m.flash != want {
		t.Errorf("flash %q, want %q", d.m.flash, want)
	}
}

func TestNoteSendKeepsSkippedOpen(t *testing.T) {
	d := noteSkipFixture(t)
	d.sendNote("go ahead")
	for _, id := range []string{"7.2", "7.3"} {
		if s := d.get(id); s.Status != "open" || s.AnswerKind != "" || s.Delivery != "none" {
			t.Errorf("%s = %+v", id, s)
		}
	}
	d.poll()
	if open := d.m.sessionOpen(); len(open) != 2 {
		t.Fatalf("panel open %v", open)
	}
	// Answering the rest later sends only them.
	d.keys("enter", "enter", "b", "b", "enter")
	if len(d.am.sends) != 2 {
		t.Fatalf("sends %v", d.am.sends)
	}
	if later := d.am.sends[1][1]; strings.Contains(later, "7.1") || !strings.Contains(later, "7.2") || strings.Contains(later, "Left open") {
		t.Errorf("later message:\n%s", later)
	}
}

func TestPlainSendWithSkipsStillHolds(t *testing.T) {
	d := noteSkipFixture(t)
	d.sendNote("")
	if len(d.am.sends) != 0 {
		t.Fatalf("sends %v", d.am.sends)
	}
	if got := d.get("7.1").Delivery; got != "held" {
		t.Errorf("7.1 delivery %q", got)
	}
}

func TestNoteWithAllSkippedGoesToAsker(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "First?", "x", "y")
	d.ask("7", "s1", "Second?", "x", "y")
	d.openWalk()
	d.keys("s", "s")
	d.sendNote("need a day")
	if len(d.am.sends) != 1 || d.am.sends[0][0] != "s1" {
		t.Fatalf("sends %v", d.am.sends)
	}
	msg := d.am.sends[0][1]
	for _, want := range []string{"Note: need a day", "Left open: 7.1, 7.2"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	for _, id := range []string{"7.1", "7.2"} {
		if d.get(id).Status != "open" {
			t.Errorf("%s closed", id)
		}
	}
	if want := "sent your note to spec-s1; 2 left open"; d.m.flash != want {
		t.Errorf("flash %q, want %q", d.m.flash, want)
	}
}

func TestNoteSendRetryDoesNotDuplicate(t *testing.T) {
	d := noteSkipFixture(t)
	d.am.fail = "session refused the message"
	d.sendNote("go ahead")
	if got := d.get("7.1").Delivery; got != "failed" {
		t.Fatalf("7.1 delivery %q", got)
	}
	d.am.fail = ""
	res, err := d.ib.Retry("s1")
	if err != nil || !res.OK {
		t.Fatalf("retry %+v %v", res, err)
	}
	if retried := d.am.sends[1][1]; !strings.Contains(retried, "Note: go ahead") || !strings.Contains(retried, "Left open: 7.2, 7.3") {
		t.Errorf("retried message:\n%s", retried)
	}
	if _, err := d.ib.Retry("s1"); err == nil {
		t.Fatal("a second retry found something to send")
	}
	d.poll()
	d.keys("enter", "enter", "b", "b", "enter")
	n := 0
	for _, s := range d.am.sends {
		n += strings.Count(s[1], "7.1 Which way?")
	}
	if n != 2 || len(d.am.sends) != 3 {
		t.Fatalf("7.1 in %d messages (1 failed + 1 retry expected), sends %d", n, len(d.am.sends))
	}
}

func TestNoteWithAllSkippedFailedSendSaysSo(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "First?", "x", "y")
	d.openWalk()
	d.keys("s")
	d.am.fail = "session refused the message"
	d.sendNote("need a day")
	if want := "note not sent: session refused the message"; d.m.flash != want {
		t.Errorf("flash %q, want %q", d.m.flash, want)
	}
	if d.get("7.1").Status != "open" {
		t.Error("7.1 closed")
	}
}
