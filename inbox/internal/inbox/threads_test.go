package inbox

import (
	"errors"
	"strings"
	"testing"
)

func kinds(th Thread) string {
	var out []string
	for _, it := range th.Items {
		switch {
		case it.Decision != nil:
			out = append(out, it.Decision.ID+":"+it.Decision.Status)
		case it.Note != nil:
			out = append(out, it.Note.Label())
		default:
			out = append(out, it.Chat.Label())
		}
	}
	return strings.Join(out, " ")
}

func TestThreadOrdersMixedKindsWithTies(t *testing.T) {
	ib, _, _ := open(t)
	at(t, "2026-01-01T00:00:00+00:00")
	addNote(t, ib, "843", "first note")
	ask(t, ib, "843", "s1", false, "Which?", "a", "b")
	ask(t, ib, "843", "s1", false, "Gone?", "a", "b")
	if _, err := ib.Withdraw("843.2", "s1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.AddChat("843", "same second"); err != nil {
		t.Fatal(err)
	}
	addNote(t, ib, "", "loose")
	ask(t, ib, "9", "s2", false, "Other?", "a", "b")
	at(t, "2026-01-01T00:01:00+00:00")
	addNote(t, ib, "843", "second note")
	if _, err := ib.Reply(3, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.Reply(3, "two"); err != nil {
		t.Fatal(err)
	}
	th, err := ib.Thread("843")
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(th); got != "843.1:open 843.2:withdrawn n1 c1 n3" {
		t.Fatalf("order %q", got)
	}
	last := th.Items[len(th.Items)-1].Note
	if len(last.Replies) != 2 || last.Replies[0].Text != "one" || last.Replies[1].Text != "two" {
		t.Fatalf("%+v", last.Replies)
	}
	if empty, err := ib.Thread("1"); err != nil || len(empty.Items) != 0 {
		t.Fatalf("%+v %v", empty, err)
	}
}

func TestThreadKeepsReadNotesAndTakesTheNewestTitle(t *testing.T) {
	ib, _, _ := open(t)
	at(t, "2026-01-01T00:00:00+00:00")
	if _, err := ib.Ask(AskInput{Task: "843", Question: "Q?", SessionID: "s1", Name: "spec-s1", Title: "Old", Brief: "old"}); err != nil {
		t.Fatal(err)
	}
	at(t, "2026-01-02T00:00:00+00:00")
	if _, err := ib.Ask(AskInput{Task: "843", Question: "Q?", SessionID: "s2", Name: "spec-s2", Title: "New"}); err != nil {
		t.Fatal(err)
	}
	addNote(t, ib, "843", "read")
	if err := ib.MarkNotes([]int64{1}, true); err != nil {
		t.Fatal(err)
	}
	at(t, "2026-01-05T00:00:00+00:00")
	th, err := ib.Thread("843")
	if err != nil || th.Title != "New" || th.Brief != "old" || !strings.Contains(kinds(th), "n1") {
		t.Fatalf("%+v %v", th, err)
	}
	titles, err := ib.TaskTitles()
	if err != nil || titles["843"] != "New" || len(titles) != 1 {
		t.Fatalf("%v %v", titles, err)
	}
}

func TestRefileMovesAndClears(t *testing.T) {
	ib, _, _ := open(t)
	addNote(t, ib, "", "loose")
	n, err := ib.Refile(1, "860")
	if err != nil || n.Task != "860" {
		t.Fatalf("%+v %v", n, err)
	}
	if got, _ := ib.Note(1); got.Task != "860" {
		t.Fatalf("%+v", got)
	}
	for _, clear := range []string{"-", ""} {
		if _, err := ib.Refile(1, "860"); err != nil {
			t.Fatal(err)
		}
		if n, err := ib.Refile(1, clear); err != nil || n.Task != "" {
			t.Fatalf("%q: %+v %v", clear, n, err)
		}
	}
	var ie *Error
	if _, err := ib.Refile(9, "1"); !errors.As(err, &ie) || ie.Code != 2 || ie.Msg != "no note n9" {
		t.Fatalf("%v", err)
	}
	for _, bad := range []string{"abc", "8-1", "#860"} {
		if _, err := ib.Refile(1, bad); !errors.As(err, &ie) || ie.Code != 2 {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}
