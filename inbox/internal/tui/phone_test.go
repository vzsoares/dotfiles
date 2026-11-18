package tui

import (
	"strings"
	"testing"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

const phoneW, phoneH = 44, 24

func TestPhoneGoldens(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	cases := []struct {
		name  string
		setup func(d *drv)
		scr   screen
	}{
		{"phone-list-44x24", func(d *drv) {
			d.ask("7", "s1", "Which storage should the cache use?", "redis", "files")
			d.ask("7", "s1", "Second?", "a", "b")
			d.addNote("707", "Task 707 merged", "Tag v1.2.3.")
		}, scList},
		{"phone-walk-44x24", func(d *drv) {
			d.ask("7", "s1", "Which storage should the cache use?", "redis", "files")
			d.ask("7", "s1", "Second?", "a", "b")
			d.openWalk()
		}, scWalk},
		{"phone-review-44x24", func(d *drv) {
			d.ask("7", "s1", "Which storage should the cache use?", "redis", "files")
			d.ask("7", "s1", "Second?", "a", "b")
			d.openWalk()
			d.keys("a", "b")
		}, scReview},
		{"phone-decisions-44x24", func(d *drv) {
			d.ask("7", "s1", "Which storage should the cache use?", "redis", "files")
			d.ask("7", "s1", "Second?", "a", "b")
			d.openWalk()
			d.keys("a", "esc")
		}, scJump},
		{"phone-confirm-44x24", func(d *drv) {
			d.ib.User = true
			d.askProd("s1")
			d.openWalk()
			d.keys("a")
		}, scConfirm},
	}
	for _, c := range cases {
		d := newDrv(t, phoneW, phoneH)
		c.setup(d)
		d.poll()
		if d.m.scr != c.scr {
			t.Fatalf("%s: screen %d, want %d:\n%s", c.name, d.m.scr, c.scr, d.view())
		}
		d.fits()
		golden(t, c.name, d.view())
	}
}

func TestPhoneConfirmWrapsFullSha(t *testing.T) {
	d := newDrvWith(t, phoneW, phoneH, func(d *drv) { d.ib.User = true })
	d.askProd("s1")
	d.openWalk()
	d.keys("a")
	d.fits()
	if !strings.Contains(strings.NewReplacer(" ", "", "\n", "").Replace(d.view()), prodSha) {
		t.Fatalf("sha cut:\n%s", d.view())
	}
}

func TestRingPushesCountsOnly(t *testing.T) {
	d := newDrv(t, phoneW, phoneH)
	d.poll()
	d.ask("7", "s1", "Use sk-live-SECRET123?", "sk-live-OPT", "b")
	d.ask("7", "s1", "Second?", "a", "b")
	if _, err := d.other.AskProd(inbox.AskInput{Task: "9", SessionID: "s2", Name: "spec-s2"}, prodWant()); err != nil {
		t.Fatal(err)
	}
	d.addNote("707", "token ghp_NOTETEXT leaked", "")
	d.poll()
	want := []string{"#7 · 2 open", "#7 · 2 open", "#9 · 1 open · PROD", "✉ 1 unread"}
	if strings.Join(d.am.pushes, "|") != strings.Join(want, "|") {
		t.Fatalf("pushes %q, want %q", d.am.pushes, want)
	}
	d.poll()
	if len(d.am.pushes) != len(want) {
		t.Fatalf("pushed again: %q", d.am.pushes)
	}
}
