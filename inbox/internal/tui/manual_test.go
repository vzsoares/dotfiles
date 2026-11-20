package tui

import (
	"strings"
	"testing"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

const manualBody = "Run on the build machine:\n\n```sh\njust rotate-key --env prod\n```\n"

func (d *drv) askManual(body string) string {
	d.t.Helper()
	dec, err := d.other.Ask(inbox.AskInput{Task: "929", Question: "Rotate the signing key?", Body: body, Manual: true,
		SessionID: "s1", Name: "spec-s1", Title: "Key rotation"})
	if err != nil {
		d.t.Fatal(err)
	}
	return dec.ID
}

func userPanel(d *drv) { d.ib.User = true }

func TestManualDoneNeedsTypedY(t *testing.T) {
	for _, sz := range sizes {
		d := newDrvWith(t, sz[0], sz[1], userPanel)
		id := d.askManual(manualBody)
		d.openWalk()
		for _, back := range []string{"n", "esc", "enter", "x"} {
			d.keys("a")
			if d.m.scr != scConfirm {
				t.Fatalf("%v: a did not confirm:\n%s", sz, d.view())
			}
			v := d.text()
			for _, want := range []string{"manual step", "Rotate the signing key?", "just rotate-key --env prod",
				"Did you run it? type y", "y I ran it"} {
				if !strings.Contains(v, want) {
					t.Fatalf("%v: %q missing:\n%s", sz, want, d.view())
				}
			}
			d.fits()
			d.keys(back)
			if d.m.scr != scWalk || d.m.p.cur != id || len(d.m.p.staged) != 0 {
				t.Fatalf("%v: %s staged or left the question:\n%s", sz, back, d.view())
			}
		}
		if dec := d.get(id); dec.Status != "open" {
			t.Fatalf("%v: answered without y: %+v", sz, dec)
		}
		d.keys("enter")
		if d.m.scr != scConfirm {
			t.Fatalf("%v: enter on Done did not confirm", sz)
		}
		d.keys("y")
		if d.m.scr != scReview {
			t.Fatalf("%v: y did not reach review:\n%s", sz, d.view())
		}
		if _, ok := d.m.drafts[id]; ok {
			t.Fatalf("%v: a confirmed Done was kept as a draft", sz)
		}
		d.keys("enter")
		dec := d.get(id)
		if dec.Status != "answered" || dec.AnswerOption != "a" || dec.ConfirmedAt == "" {
			t.Fatalf("%v: %+v", sz, dec)
		}
		if len(d.am.sends) != 1 || !strings.Contains(d.am.sends[0][1], id+" Rotate the signing key? → a: Done (confirmed)") {
			t.Fatalf("%v: %q", sz, d.am.sends)
		}
	}
}

func TestManualLaterNeedsNoConfirm(t *testing.T) {
	d := newDrvWith(t, 92, 29, userPanel)
	id := d.askManual(manualBody)
	d.openWalk()
	d.keys("b")
	if d.m.scr != scReview {
		t.Fatalf("Later went through confirm:\n%s", d.view())
	}
	d.keys("enter")
	if dec := d.get(id); dec.Status != "answered" || dec.AnswerOption != "b" || dec.ConfirmedAt != "" {
		t.Fatalf("%+v", dec)
	}
	if len(d.am.sends) != 1 || !strings.HasSuffix(d.am.sends[0][1], "→ b: Later") {
		t.Fatalf("%q", d.am.sends)
	}
}

func TestManualConfirmScrollsALongStep(t *testing.T) {
	d := newDrvWith(t, 60, 12, userPanel)
	var steps []string
	for i := range 30 {
		steps = append(steps, "- step "+string(rune('A'+i%26))+words(20))
	}
	id := d.askManual(strings.Join(steps, "\n") + "\n\nlast line of the step")
	d.openWalk()
	d.keys("a")
	d.fits()
	if v := d.view(); !strings.Contains(v, "Did you run it? type y") || strings.Contains(v, "last line of the step") {
		t.Fatalf("prompt not pinned, or nothing to scroll:\n%s", v)
	}
	for range 40 {
		d.keys("pgdown")
	}
	d.keys("down", "j")
	if d.m.scr != scConfirm {
		t.Fatalf("scrolling left the confirm:\n%s", d.view())
	}
	if v := d.view(); !strings.Contains(v, "last line of the step") || !strings.Contains(v, "Did you run it? type y") {
		t.Fatalf("end of the step not reached:\n%s", v)
	}
	d.keys("y", "enter")
	if dec := d.get(id); dec.AnswerOption != "a" || dec.ConfirmedAt == "" {
		t.Fatalf("%+v", dec)
	}
}

func TestManualDoneRefusedInAgentPanel(t *testing.T) {
	d := newDrvWith(t, 92, 29, func(d *drv) { d.m.prodBlock = "CLAUDECODE is set" })
	id := d.askManual(manualBody)
	d.openWalk()
	if !strings.Contains(d.text(), "Done only from the cockpit terminal (CLAUDECODE is set)") {
		t.Fatalf("no notice:\n%s", d.view())
	}
	d.keys("a")
	d.keys("enter")
	d.keys("y")
	if d.m.scr != scWalk || len(d.m.p.staged) != 0 || d.get(id).Status != "open" {
		t.Fatalf("Done staged in an agent panel: screen %d\n%s", d.m.scr, d.view())
	}
	d.keys("b", "enter")
	if dec := d.get(id); dec.Status != "answered" || dec.AnswerOption != "b" {
		t.Fatalf("Later refused: %+v", dec)
	}
}

func TestManualAskIsMarked(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrvWith(t, 92, 29, userPanel)
	d.askManual(manualBody)
	d.ask("7", "s2", "Which way?", "left", "right")
	d.poll()
	if v := d.view(); !strings.Contains(v, "MANUAL · spec-s1") || strings.Contains(v, "MANUAL · spec-s2") {
		t.Fatalf("list row:\n%s", v)
	}
	golden(t, "cockpit-manual-92x29", d.view())
	d.addNote("929", "Key rotation queued", "")
	d.poll()
	if v := d.view(); !strings.Contains(v, "#929 · Key rotation · MANUAL · 1 open") {
		t.Fatalf("thread header:\n%s", v)
	}
	d.m.inboxOpen = true
	if v := d.view(); !strings.Contains(v, "MANUAL · spec-s1 · #929") || strings.Contains(v, "MANUAL · spec-s2") {
		t.Fatalf("inbox row:\n%s", v)
	}
}

func TestManualWalkAndConfirmGoldens(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrvWith(t, 92, 29, userPanel)
	d.askManual(manualBody)
	d.openWalk()
	if !strings.Contains(d.view(), "929.1 manual  Rotate the signing key?") {
		t.Fatalf("walk:\n%s", d.view())
	}
	golden(t, "walk-manual-92x29", d.view())
	d.keys("a")
	golden(t, "confirm-manual-92x29", d.view())
}

func TestPhoneManualConfirmFits(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrvWith(t, phoneW, phoneH, userPanel)
	d.askManual(manualBody)
	d.openWalk()
	d.keys("a")
	d.fits()
	golden(t, "phone-confirm-manual-44x24", d.view())
}

func TestManualAmendedDuringConfirmIsNotStaged(t *testing.T) {
	d := newDrvWith(t, 92, 29, userPanel)
	id := d.askManual(manualBody)
	d.openWalk()
	d.keys("a")
	if _, err := d.other.Amend(id, "s1", false, inbox.AmendInput{Body: ptr("Run `just rotate-key --env dev` instead.")}); err != nil {
		t.Fatal(err)
	}
	d.pollKeep()
	if d.m.scr != scConfirm || !strings.Contains(d.text(), "--env dev") {
		t.Fatalf("confirm did not follow the amend:\n%s", d.view())
	}
	d.keys("y")
	if d.m.scr != scWalk || len(d.m.p.staged) != 0 || !strings.Contains(d.text(), "changed: read it again before confirming") {
		t.Fatalf("y staged Done for a step amended under it:\n%s", d.view())
	}
	d.keys("a", "y")
	if d.m.scr != scReview {
		t.Fatalf("a second confirm on the new text did not stage:\n%s", d.view())
	}
}

func TestManualMarkerFollowsTheTask(t *testing.T) {
	d := newDrvWith(t, 92, 29, userPanel)
	d.ask("7", "s1", "Which way?", "left", "right")
	d.askManual("")
	d.addNote("7", "Seven merged", "")
	d.poll()
	for _, l := range strings.Split(d.view(), "\n") {
		if strings.Contains(l, "#7 ·") && strings.Contains(l, "items") && strings.Contains(l, "MANUAL") {
			t.Fatalf("thread #7 has no manual ask: %q", l)
		}
	}
}

func TestManualNoticeKeepsTheDraftInAgentPanel(t *testing.T) {
	d := newDrvWith(t, 92, 29, func(d *drv) { d.m.prodBlock = "CLAUDECODE is set" })
	d.askManual("")
	d.ask("929", "s1", "Second?", "x", "y")
	d.openWalk()
	d.keys("b", "left")
	v := d.text()
	if !strings.Contains(v, "draft: b: Later") || !strings.Contains(v, "Done only from the cockpit terminal") {
		t.Fatalf("notice:\n%s", d.view())
	}
}
