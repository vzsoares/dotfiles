package tui

import (
	"strings"
	"testing"
)

func TestAgentRunsAskShowsBanner(t *testing.T) {
	d := newDrvWith(t, 92, 29, userPanel)
	d.askBody()
	d.openWalk()
	if v := d.view(); !strings.Contains(v, "The agent runs this after you answer; don't run it yourself.") {
		t.Fatalf("banner missing:\n%s", v)
	}
}

func TestAgentRunsCopyThenYRecordsRanIt(t *testing.T) {
	d := newDrvWith(t, 92, 29, userPanel)
	dec := d.askBody()
	got := d.copies()
	d.openWalk()
	d.keys("y", "1")
	if len(*got) != 1 || !strings.Contains(d.view(), "Did you run it? type y") {
		t.Fatalf("no prompt after the copy %q:\n%s", *got, d.view())
	}
	d.keys("y")
	if c := d.get(dec.ID); !c.UserRan() {
		t.Fatalf("ran it not recorded: %+v", c)
	}
	d.keys("a")
	d.keys("enter")
	if len(d.am.sends) != 1 || !strings.Contains(d.am.sends[0][1], "→ a: Run: an agent runs all three (user ran it; skip your run)") {
		t.Fatalf("%q", d.am.sends)
	}
}

func TestAgentRunsCopyThenOtherKeyRecordsNothing(t *testing.T) {
	d := newDrvWith(t, 92, 29, userPanel)
	dec := d.askBody()
	d.copies()
	d.openWalk()
	d.keys("y", "1", "n")
	if strings.Contains(d.view(), "Did you run it?") || d.get(dec.ID).UserRan() {
		t.Fatalf("recorded or still asking:\n%s", d.view())
	}
}
