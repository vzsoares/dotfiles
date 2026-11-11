package main

import (
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// chief adds a session named chief, id c1, to the fake agent-manager.
func (f *fake) chief() {
	c := session("c1", "idle", true)
	c["name"] = "chief"
	f.sessions(session("s1", "idle", true), c)
}

var asChief = []string{"AGENT_MANAGER_SESSION_ID=c1"}

func TestNoteCLI(t *testing.T) {
	f := newFake(t)
	f.chief()
	if r := f.zen(asChief, "note", "707", "--title", "Task done", "--body", "**ok**"); r.code != 0 || r.stdout != "n1\n" {
		t.Fatalf("%+v", r)
	}
	for _, c := range []struct {
		env  []string
		args []string
		code int
	}{
		{nil, []string{"note", "--title", "x"}, 2},
		{[]string{"AGENT_MANAGER_SESSION_ID=s1"}, []string{"note", "--title", "x"}, 1},
		{asChief, []string{"note", "--title", ""}, 2},
		{asChief, []string{"note", "--title", "a\nb"}, 2},
		{asChief, []string{"note", "--title", strings.Repeat("t", 121)}, 2},
		{asChief, []string{"note", "--title", "x", "--body", strings.Repeat("b", 16*1024+1)}, 2},
	} {
		if r := f.zen(c.env, c.args...); r.code != c.code {
			t.Errorf("%v: %+v", c.args, r)
		}
	}
	if r := f.zen([]string{"AGENT_MANAGER_SESSION_ID=s1"}, "note", "--title", "x"); !strings.Contains(r.stderr,
		"notes come from chief; send chief a message instead") {
		t.Fatalf("%+v", r)
	}
	c := f.cmd(asChief, zenBin, "note", "--title", "From stdin", "--body", "-")
	c.Stdin = strings.NewReader("line one\nline two\n")
	if r := run(c); r.code != 0 || r.stdout != "n2\n" {
		t.Fatalf("%+v", r)
	}
	if body := f.sqlite("SELECT body FROM notes WHERE id = 2"); body != "line one\nline two\n\n" {
		t.Fatalf("%q", body)
	}
}

func notesJSON(t *testing.T, f *fake, args ...string) []map[string]any {
	t.Helper()
	r := f.zen(nil, append([]string{"notes", "--json"}, args...)...)
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	var out []map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNotesListAndRead(t *testing.T) {
	f := newFake(t)
	f.chief()
	day1 := []string{"ZEN_INBOX_NOW=2026-01-01T00:00:00+00:00", "AGENT_MANAGER_SESSION_ID=c1"}
	f.zen(day1, "note", "707", "--title", "one")
	f.zen(day1, "note", "--title", "two")
	r := f.zen(nil, "notes", "--json")
	keys := []string{`"id"`, `"task"`, `"session_id"`, `"session_name"`, `"title"`, `"body"`, `"created_at"`,
		`"read_at"`, `"reply"`, `"replied_at"`, `"delivery"`}
	at := -1
	for _, k := range keys {
		i := strings.Index(r.stdout, k)
		if i <= at {
			t.Fatalf("key %s out of order in %s", k, r.stdout)
		}
		at = i
	}
	if r := f.zen(nil, "notes"); r.stdout != "n2\tunread\tchief\t\ttwo\nn1\tunread\tchief\t#707\tone\n" {
		t.Fatalf("%q", r.stdout)
	}
	if r := f.zen(day1, "read", "n1", "2"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(nil, "read", "n9"); r.code != 2 {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(nil, "read", "x1"); r.code != 2 {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(nil, "read"); r.code != 2 {
		t.Fatalf("%+v", r)
	}
	if got := notesJSON(t, f); len(got) != 0 {
		t.Fatalf("read a day ago, still shown: %v", got)
	}
	if got := notesJSON(t, f, "--all"); len(got) != 2 || got[0]["id"] != "n2" || got[1]["task"] != "707" {
		t.Fatalf("%v", got)
	}
	f.zen(nil, "read", "n1", "--unread")
	if got := notesJSON(t, f); len(got) != 1 || got[0]["read_at"] != "" {
		t.Fatalf("%v", got)
	}
	f.zen(nil, "read", "--all")
	f.zen(asChief, "note", "--title", "three")
	if r := f.zen(nil, "read", "--all", "--unread"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if got := notesJSON(t, f); len(got) != 2 {
		t.Fatalf("%v", got)
	}
}

func TestRetryResendsFailedReply(t *testing.T) {
	f := newFake(t)
	f.chief()
	f.zen(asChief, "note", "707", "--title", "Done")
	f.sqlite("UPDATE notes SET reply = 'thanks', delivery = 'failed', delivery_error = 'gone' WHERE id = 1")
	r := f.zen(nil, "retry", "chief")
	if r.code != 0 || r.stdout != "reply n1 sent to chief\n" {
		t.Fatalf("%+v", r)
	}
	want := []string{"send", "c1", "[zen-inbox] Reply to your note n1 (#707 Done):\nthanks", "--from", "zen-inbox", "--json"}
	if sends := f.sends(); len(sends) != 1 || !slices.Equal(sends[0], want) {
		t.Fatalf("%q", sends)
	}
	if got := strings.TrimSpace(f.sqlite("SELECT delivery, read_at != '' FROM notes")); got != "sent|1" {
		t.Fatal(got)
	}
	if r := f.zen(nil, "retry", "chief"); r.code != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestConcurrentNotesGetDistinctIDs(t *testing.T) {
	f := newFake(t)
	f.chief()
	f.zen(nil, "notes")
	var cmds []*exec.Cmd
	for range 6 {
		cmds = append(cmds, f.cmd(asChief, zenBin, "note", "--title", "n"))
	}
	var ids []string
	for _, r := range parallel(cmds) {
		if r.code != 0 {
			t.Fatalf("%+v", r)
		}
		ids = append(ids, strings.TrimSpace(r.stdout))
	}
	slices.Sort(ids)
	if strings.Join(ids, " ") != "n1 n2 n3 n4 n5 n6" {
		t.Fatal(ids)
	}
}

func TestV4FixtureMigratesToV5(t *testing.T) {
	f := newFake(t)
	f.load("testdata/xver/v4.sql")
	before := f.sqlite("SELECT * FROM decisions ORDER BY id; SELECT * FROM decision_drafts; SELECT * FROM prod_actions")
	var cmds []*exec.Cmd
	for range 6 {
		cmds = append(cmds, f.cmd(nil, zenBin, "notes", "--json"))
	}
	for _, r := range parallel(cmds) {
		if r.code != 0 || r.stdout != "[]\n" {
			t.Fatalf("%+v", r)
		}
	}
	if v := strings.TrimSpace(f.sqlite("PRAGMA user_version")); v != "5" {
		t.Fatal(v)
	}
	if after := f.sqlite("SELECT * FROM decisions ORDER BY id; SELECT * FROM decision_drafts; SELECT * FROM prod_actions"); after != before {
		t.Fatalf("migration changed rows:\n%s\n%s", before, after)
	}
}
