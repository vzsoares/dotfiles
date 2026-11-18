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

func TestInboxListUpDownWrap(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("597", "s1", "Q one?", "a", "b")
	d.ask("597", "s1", "Q two?", "a", "b")
	d.ask("598", "s2", "Q three?", "a", "b")
	d.pollKeep()
	d.rawKeys("enter")
	for _, tc := range []struct {
		key  string
		want int
	}{{"up", 2}, {"down", 0}, {"k", 2}, {"j", 0}, {"down", 1}} {
		d.rawKeys(tc.key)
		if d.m.cursor != tc.want {
			t.Fatalf("%s: cursor %d, want %d", tc.key, d.m.cursor, tc.want)
		}
	}
}

func TestInboxListWrapOneAndEmpty(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("597", "s1", "Q one?", "a", "b")
	d.pollKeep()
	d.rawKeys("enter", "up", "down", "k", "j")
	if d.m.cursor != 0 {
		t.Fatalf("one item: cursor %d", d.m.cursor)
	}
	d.m.snap.open = nil
	d.m.snap.notes = nil
	d.m.inboxOpen = true
	d.rawKeys("up", "down", "k", "j")
	if d.m.cursor != 0 {
		t.Fatalf("empty: cursor %d", d.m.cursor)
	}
}
