package main

import (
	"strings"
	"testing"
)

func TestManualAskCLI(t *testing.T) {
	f := newFake(t)
	env := []string{"AGENT_MANAGER_SESSION_ID=s1"}
	c := f.cmd(env, zenBin, "ask", "929", "--manual", "-q", "Rotate the API key?", "--body", "-")
	c.Stdin = strings.NewReader("```sh\njust rotate\n```\n")
	if r := run(c); r.code != 0 || r.stdout != "929.1\n" {
		t.Fatalf("%+v", r)
	}
	r := f.zen(nil, "show", "929.1", "--json")
	for _, want := range []string{`"rec": "a"`, `{"letter": "a", "text": "Done"}, {"letter": "b", "text": "Later"}`,
		`"kind": "manual", "confirmed_at": ""}`} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("%s missing: %+v", want, r)
		}
	}
	if r := f.zen([]string{"COLUMNS=80"}, "list"); !strings.Contains(r.stdout, "929.1 manual  Rotate the API key?") {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(env, "ask", "7", "-q", "Plain?", "--json"); r.code != 0 || strings.Contains(r.stdout, `"kind"`) {
		t.Fatalf("%+v", r)
	}
}

func TestManualAskFlagsRefused(t *testing.T) {
	f := newFake(t)
	env := []string{"AGENT_MANAGER_SESSION_ID=s1"}
	for _, args := range [][]string{
		{"--manual"},
		{"--manual", "-q", "Q?", "-o", "x", "-o", "y"},
		{"--manual", "-q", "Q?", "--gate"},
		{"--manual", "-q", "Q?", "--rec", "c"},
		append([]string{"--manual"}, prodArgs...),
	} {
		if r := f.zen(env, append([]string{"ask", "929"}, args...)...); r.code != 2 || r.stdout != "" {
			t.Fatalf("%v: %+v", args, r)
		}
	}
	if r := f.zen(nil, "list", "--all", "--json"); strings.TrimSpace(r.stdout) != "[]" {
		t.Fatalf("a refused ask was stored: %+v", r)
	}
}

func TestAnswerRefusesDoneOnManualAsk(t *testing.T) {
	f := newFake(t)
	f.sessions(session("s1", "idle", true))
	asker := []string{"AGENT_MANAGER_SESSION_ID=s1"}
	chief := []string{"AGENT_MANAGER_SESSION_ID=chief"}
	for range 2 {
		if r := f.zen(asker, "ask", "929", "--manual", "-q", "Rotate the API key?"); r.code != 0 {
			t.Fatalf("%+v", r)
		}
	}
	for _, env := range [][]string{asker, chief, nil} {
		r := f.zen(env, "answer", "929.1", "a")
		if r.code != 1 || r.stderr != "zen-inbox: 929.1 is a manual-step ask; confirm Done in the decisions panel\n" {
			t.Fatalf("%v: %+v", env, r)
		}
	}
	if r := f.zen(nil, "show", "929.1", "--json"); !strings.Contains(r.stdout, `"status": "open"`) {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(chief, "answer", "929.1", "b"); r.code != 0 {
		t.Fatalf("Later refused: %+v", r)
	}
	if r := f.zen(chief, "answer", "929.2", "--text", "ran it on the other machine"); r.code != 0 {
		t.Fatalf("free text refused: %+v", r)
	}
	if sends := f.sends(); len(sends) != 1 || !strings.Contains(strings.Join(sends[0], " "), "→ b: Later") {
		t.Fatalf("%q", sends)
	}
	if r := f.zen(asker, "amend", "929.1", "-o", "x", "-o", "y"); r.code != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestAskWithCommandBodyNeedsWhoRuns(t *testing.T) {
	f := newFake(t)
	env := []string{"AGENT_MANAGER_SESSION_ID=s1"}
	body := "```sh\njust rotate\n```\n"
	ask := func(extra ...string) result {
		c := f.cmd(env, append([]string{zenBin, "ask", "929", "-q", "Run it?"}, append(extra, "--body", "-")...)...)
		c.Stdin = strings.NewReader(body)
		return run(c)
	}
	r := ask("-o", "Run", "-o", "Hold")
	if r.code != 2 || r.stdout != "" || !strings.Contains(r.stderr, "add --manual (the user runs it) or --agent-runs (you run it after the answer)") {
		t.Fatalf("%+v", r)
	}
	if r := ask("--agent-runs"); r.code != 0 || r.stdout != "929.1\n" {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(nil, "show", "929.1", "--json"); !strings.Contains(r.stdout, `"kind": "agent-runs"`) {
		t.Fatalf("%+v", r)
	}
	if r := ask("--agent-runs", "--manual"); r.code != 2 {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(env, "ask", "929", "-q", "No code?", "--body", "plain text"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
}
