package tui

import (
	"strings"
	"testing"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

const twoBlocks = "Run:\n\n```sh\n  sed -i 's/a/b/' f \\\n  --x \\\n```\n\nthen:\n\n```\nsecond\n```"

const firstBlock = "  sed -i 's/a/b/' f \\\n  --x \\"

func (d *drv) copies() *[]string {
	var got []string
	d.m.clip = func(s string) { got = append(got, s) }
	return &got
}

func openNoteWithBlocks(t *testing.T) (*drv, *[]string) {
	d := newDrv(t, 92, 29)
	d.addNote("", "Setup", twoBlocks)
	d.poll()
	d.keys("enter")
	if d.m.scr != scNote {
		t.Fatalf("not on the note:\n%s", d.view())
	}
	return d, d.copies()
}

func TestYThenNumberCopiesThatBlockRaw(t *testing.T) {
	d, got := openNoteWithBlocks(t)
	v := d.view()
	if !strings.Contains(v, "[1] copy") || !strings.Contains(v, "[2] copy") {
		t.Fatalf("labels missing:\n%s", v)
	}
	if strings.ContainsAny(v, "╭╮╰╯") {
		t.Fatalf("border on screen:\n%s", v)
	}
	d.keys("y", "1")
	d.keys("y", "2")
	if want := []string{firstBlock, "second"}; strings.Join(*got, "|") != strings.Join(want, "|") {
		t.Fatalf("copied %q", *got)
	}
	if !strings.Contains(d.view(), "copied 2") {
		t.Fatalf("no toast:\n%s", d.view())
	}
}

func TestYWithOutOfRangeNumberCopiesNothing(t *testing.T) {
	d, got := openNoteWithBlocks(t)
	d.keys("y", "3")
	d.keys("y", "0")
	if len(*got) != 0 || strings.Contains(d.view(), "copied") {
		t.Fatalf("copied %q:\n%s", *got, d.view())
	}
}

func TestYThenOtherKeyCancelsAndActsNormally(t *testing.T) {
	d, got := openNoteWithBlocks(t)
	d.keys("y", "m")
	if len(*got) != 0 || d.noteRow(1).Unread() {
		t.Fatalf("copied %q, unread %v", *got, d.noteRow(1).Unread())
	}
}

func TestYInWalkCopiesFromContext(t *testing.T) {
	d := newDrv(t, 92, 29)
	_, err := d.other.Ask(inbox.AskInput{Task: "7", Question: "Run it?", Context: twoBlocks,
		Options: []string{"yes", "no"}, SessionID: "s1", Name: "spec-s1"})
	if err != nil {
		t.Fatal(err)
	}
	got := d.copies()
	d.openWalk()
	d.keys("y", "2")
	if len(*got) != 1 || (*got)[0] != "second" {
		t.Fatalf("copied %q:\n%s", *got, d.view())
	}
}

func TestYWithoutBlocksSaysSoAndWaitsForNothing(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.addNote("", "Plain", "no code here")
	d.poll()
	d.keys("enter")
	got := d.copies()
	d.keys("y", "1")
	if len(*got) != 0 || d.m.yank {
		t.Fatalf("copied %q, yank %v", *got, d.m.yank)
	}
}
