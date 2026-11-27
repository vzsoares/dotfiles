package inbox

import (
	"errors"
	"slices"
	"testing"
)

func TestProjectsStoreAndUnresolved(t *testing.T) {
	ib, _, _ := open(t)
	ask(t, ib, "11", "s1", false, "q?")
	ask(t, ib, "12", "s1", false, "q?")
	if _, err := ib.AddNote(NoteInput{Task: "13", Title: "n", SessionID: "c1", Name: NoteSender}); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.AddNote(NoteInput{Title: "loose", SessionID: "c1", Name: NoteSender}); err != nil {
		t.Fatal(err)
	}
	got, err := ib.Unresolved()
	if err != nil || !slices.Equal(got, []string{"11", "12", "13"}) {
		t.Fatalf("unresolved %v %v", got, err)
	}
	if err := ib.SetProject("11", "Alpha"); err != nil {
		t.Fatal(err)
	}
	if err := ib.SetProject("12", ""); err != nil { // asked: no project
		t.Fatal(err)
	}
	got, _ = ib.Unresolved()
	if !slices.Equal(got, []string{"13"}) {
		t.Fatalf("after set %v", got)
	}
	m, err := ib.Projects()
	if err != nil || len(m) != 1 || m["11"] != "Alpha" {
		t.Fatalf("projects %v %v", m, err)
	}
	if err := ib.SetProject("11", "Beta"); err != nil {
		t.Fatal(err)
	}
	if m, _ = ib.Projects(); m["11"] != "Beta" {
		t.Fatalf("not replaced: %v", m)
	}
}

func TestUnresolvedSkipsClosedDecisions(t *testing.T) {
	ib, _, _ := open(t)
	id := ask(t, ib, "21", "s1", false, "q?")
	if _, err := ib.Answer(id, p("a"), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := ib.Unresolved(); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestResolveProjectsSkipsAFailingBoardAndStoresNothing(t *testing.T) {
	ib, _, _ := open(t)
	ask(t, ib, "31", "s1", false, "q?")
	down := func(string) (string, error) { return "", errors.New("HTTP 500") }
	resolved, failed := ib.ResolveProjects(down, []string{"31"})
	if resolved != 0 || failed != 1 {
		t.Fatalf("resolved %d failed %d", resolved, failed)
	}
	if m, _ := ib.Projects(); len(m) != 0 {
		t.Fatalf("stored on failure: %v", m)
	}
	if got, _ := ib.Unresolved(); !slices.Equal(got, []string{"31"}) {
		t.Fatalf("not retryable: %v", got)
	}
	d, err := ib.Ask(AskInput{Task: "31", Question: "still files?", Options: []string{"a", "b"}, SessionID: "s1", Name: "spec-s1"})
	if err != nil || d.ID != "31.2" {
		t.Fatalf("ask after failure: %+v %v", d, err)
	}
}
