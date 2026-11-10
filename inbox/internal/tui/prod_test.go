package tui

import (
	"strings"
	"testing"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

const prodSha = "0123456789abcdef0123456789abcdef01234567"

func prodWant() inbox.ProdAction {
	return inbox.ProdAction{Action: "release", Repo: "dotfiles", Branch: "prod", Sha: prodSha}
}

func (d *drv) askProd(sid string) string {
	d.t.Helper()
	dec, err := d.other.AskProd(inbox.AskInput{Task: "680", SessionID: sid, Name: "spec-" + sid}, prodWant())
	if err != nil {
		d.t.Fatal(err)
	}
	return dec.ID
}

func TestProdApproveNeedsConfirm(t *testing.T) {
	for _, sz := range sizes {
		d := newDrvWith(t, sz[0], sz[1], func(d *drv) { d.ib.User = true })
		id := d.askProd("s1")
		d.openWalk()
		if strings.Contains(d.text(), "prod approvals only") {
			t.Fatalf("%v: unlocked panel shows the notice:\n%s", sz, d.view())
		}
		for _, back := range []string{"n", "esc", "enter", "x"} {
			d.keys("a")
			if d.m.scr != scConfirm {
				t.Fatalf("%v: a did not confirm:\n%s", sz, d.view())
			}
			v := d.view()
			for _, want := range []string{prodSha, "Approve? y/N", "release", "dotfiles", "prod"} {
				if !strings.Contains(v, want) {
					t.Fatalf("%v: %q missing:\n%s", sz, want, v)
				}
			}
			d.fits()
			d.keys(back)
			if d.m.scr != scWalk || d.m.p.cur != id || len(d.m.p.staged) != 0 {
				t.Fatalf("%v: %s staged or left the question:\n%s", sz, back, d.view())
			}
		}
		d.keys("enter")
		if d.m.scr != scConfirm {
			t.Fatalf("%v: enter on Approve did not confirm", sz)
		}
		d.keys("y")
		if d.m.scr != scReview {
			t.Fatalf("%v: y did not reach review:\n%s", sz, d.view())
		}
		d.keys("enter")
		dec := d.get(id)
		if dec.Status != "answered" || dec.AnswerKind != "pick" || dec.AnswerOption != "a" {
			t.Fatalf("%v: %+v", sz, dec)
		}
		if _, err := d.ib.Approval(id, prodWant(), "user"); err != nil {
			t.Fatalf("%v: %v", sz, err)
		}
	}
}

func TestProdHoldNeedsNoConfirm(t *testing.T) {
	d := newDrvWith(t, 92, 29, func(d *drv) { d.ib.User = true })
	id := d.askProd("s1")
	d.openWalk()
	d.keys("b")
	if d.m.scr != scReview {
		t.Fatalf("hold went through confirm:\n%s", d.view())
	}
	d.keys("enter")
	if dec := d.get(id); dec.Status != "answered" || dec.AnswerOption != "b" {
		t.Fatalf("%+v", dec)
	}
}

func TestProdLockedInAgentSession(t *testing.T) {
	for _, sz := range sizes {
		d := newDrvWith(t, sz[0], sz[1], func(d *drv) { d.m.prodBlock = "AGENT_MANAGER_SESSION_ID is set" })
		id := d.askProd("s1")
		plain := d.ask("900", "s2", "Which way?", "left", "right")
		d.openWalk()
		if d.m.p.cur != id {
			t.Fatalf("%v: walking %s", sz, d.m.p.cur)
		}
		if !strings.Contains(d.text(), "prod approvals only from the decisions terminal (AGENT_MANAGER_SESSION_ID is set)") {
			t.Fatalf("%v: no notice:\n%s", sz, d.view())
		}
		if v := d.text(); strings.Contains(v, "a-d pick") || strings.Contains(v, "Free text") {
			t.Fatalf("%v: locked ask offers an answer:\n%s", sz, d.view())
		}
		d.fits()
		d.keys("a", "b", "f", "y")
		if d.m.scr != scWalk || d.m.p.cur != id || len(d.m.p.staged) != 0 {
			t.Fatalf("%v: locked ask answered: screen %d\n%s", sz, d.m.scr, d.view())
		}
		d.keys("t")
		if d.m.scr == scTalkPick {
			t.Fatalf("%v: talk offers to resolve the locked ask:\n%s", sz, d.view())
		}
		if dec := d.get(id); dec.Status != "open" {
			t.Fatalf("%v: %+v", sz, dec)
		}
		d.keys("esc")
		d.poll()
		d.keys("down", "enter", "enter", "b", "enter")
		if dec := d.get(plain); dec.Status != "answered" || dec.AnswerOption != "b" || len(d.am.sends) != 1 {
			t.Fatalf("%v: %+v sends %v\n%s", sz, dec, d.am.sends, d.view())
		}
	}
}
