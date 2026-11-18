package tui

import "testing"

func TestInboxListCursorStaysOnItsDecisionAcrossPolls(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("597", "s1", "Q one?", "a", "b", "c")
	d.ask("597", "s1", "Q two?", "a", "b", "c")
	d.ask("597", "s1", "Q three?", "a", "b", "c")
	d.pollKeep()
	d.rawKeys("enter", "down", "down")
	if d.m.cursor != 2 {
		t.Fatalf("cursor %d:\n%s", d.m.cursor, d.view())
	}
	for range 3 {
		d.pollKeep()
	}
	if d.m.cursor != 2 {
		t.Fatalf("cursor jumped to %d:\n%s", d.m.cursor, d.view())
	}
	d.ask("598", "s2", "Other?", "a", "b")
	d.pollKeep()
	if d.m.cursor != 2 {
		t.Fatalf("cursor jumped to %d after a new row:\n%s", d.m.cursor, d.view())
	}
}
