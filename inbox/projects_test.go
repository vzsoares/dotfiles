package main

import "testing"

func TestProjectsBoardDownLeavesAskAndNoteFiled(t *testing.T) {
	f := newFake(t)
	f.chief()
	f.write("board-down", "")
	if r := f.zen([]string{"AGENT_MANAGER_SESSION_ID=s1"}, "ask", "707", "-q", "Pick one?", "-o", "x", "-o", "y"); r.code != 0 || r.stdout != "707.1\n" {
		t.Fatalf("ask: %+v", r)
	}
	if r := f.zen(asChief, "note", "707", "--title", "Done"); r.code != 0 || r.stdout != "n1\n" {
		t.Fatalf("note: %+v", r)
	}
	if r := f.zen(nil, "projects"); r.code != 0 || r.stdout != "resolved 0, failed 1\n" || r.stderr != "" {
		t.Fatalf("projects while down: %+v", r)
	}
	f.remove("board-down")
	if r := f.zen(nil, "projects"); r.code != 0 || r.stdout != "resolved 1, failed 0\n" {
		t.Fatalf("projects after: %+v", r)
	}
	if r := f.zen(nil, "projects"); r.code != 0 || r.stdout != "resolved 0, failed 0\n" {
		t.Fatalf("projects again: %+v", r)
	}
}

func TestProjectsNamedTaskRefreshesItsTag(t *testing.T) {
	f := newFake(t)
	if r := f.zen(nil, "projects", "808"); r.code != 0 || r.stdout != "resolved 1, failed 0\n" {
		t.Fatalf("%+v", r)
	}
}
