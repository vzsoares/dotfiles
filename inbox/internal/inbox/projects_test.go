package inbox

import (
	"errors"
	"os"
	"path/filepath"
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

func fakeBoard(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "zen-board")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBoardLookupTreatsNotFoundAsNoProjectAndOtherFailuresAsRetryable(t *testing.T) {
	if tag, err := BoardLookup(fakeBoard(t, "exit 2"))("spec-s2"); tag != "" || err != nil {
		t.Fatalf("not found: %q %v", tag, err)
	}
	if _, err := BoardLookup(fakeBoard(t, "exit 1"))("1"); err == nil {
		t.Fatal("an API failure must stay retryable")
	}
	if tag, err := BoardLookup(fakeBoard(t, `echo '{"tag": "Fakeproj"}'`))("1"); tag != "Fakeproj" || err != nil {
		t.Fatalf("%q %v", tag, err)
	}
}

func TestStoredAnswersAreReaskedAfterAWholeDay(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", "2026-11-10T12:00:00Z")
	ib, _, _ := open(t)
	ask(t, ib, "41", "s1", false, "q?")
	if err := ib.SetProject("41", "Alpha"); err != nil {
		t.Fatal(err)
	}
	if got, _ := ib.Unresolved(); len(got) != 0 {
		t.Fatalf("fresh answer asked again: %v", got)
	}
	if fresh, _ := ib.ProjectFresh("41"); !fresh {
		t.Fatal("fresh answer not fresh")
	}
	t.Setenv("ZEN_INBOX_NOW", "2026-11-11T11:59:00Z")
	if got, _ := ib.Unresolved(); len(got) != 0 {
		t.Fatalf("asked before 24 h: %v", got)
	}
	t.Setenv("ZEN_INBOX_NOW", "2026-11-11T12:01:00Z")
	got, _ := ib.Unresolved()
	if !slices.Equal(got, []string{"41"}) {
		t.Fatalf("stale answer not re-asked: %v", got)
	}
	if m, _ := ib.Projects(); m["41"] != "Alpha" {
		t.Fatalf("stale tag hidden while it is re-checked: %v", m)
	}
	if fresh, _ := ib.ProjectFresh("41"); fresh {
		t.Fatal("stale answer reported fresh")
	}
	// The board moved the task to another project.
	ib.ResolveProjects(func(string) (string, error) { return "Beta", nil }, got)
	if m, _ := ib.Projects(); m["41"] != "Beta" {
		t.Fatalf("new project not picked up: %v", m)
	}
	// A "none" answer is re-asked the same way.
	if err := ib.SetProject("42", ""); err != nil {
		t.Fatal(err)
	}
	ask(t, ib, "42", "s1", false, "q?")
	t.Setenv("ZEN_INBOX_NOW", "2026-11-13T00:00:00Z")
	if got, _ = ib.Unresolved(); !slices.Contains(got, "42") {
		t.Fatalf("a none answer was never re-asked: %v", got)
	}
}
