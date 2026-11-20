package main

import (
	"strings"
	"testing"
)

func TestAskBodyFromStdinShowsEverywhere(t *testing.T) {
	f := newFake(t)
	env := []string{"AGENT_MANAGER_SESSION_ID=s1"}
	body := "Steps:\n\n```sh\nmake deploy\n```\n"
	c := f.cmd(env, zenBin, "ask", "7", "-q", "Deploy now?", "-o", "yes", "-o", "no", "--body", "-")
	c.Stdin = strings.NewReader(body)
	if r := run(c); r.code != 0 || r.stdout != "7.1\n" {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(nil, "show", "7.1", "--json"); !strings.Contains(r.stdout, `"body": "Steps:\n\n`+"```sh"+`\nmake deploy\n`+"```"+`\n"}`) {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(nil, "show", "7.1"); !strings.Contains(r.stdout, "Deploy now?\n"+body) {
		t.Fatalf("%+v", r)
	}
	if r := f.zen([]string{"COLUMNS=80"}, "list"); !strings.Contains(r.stdout, "7.1  Deploy now?\n  Steps:\n") || !strings.Contains(r.stdout, "  make deploy\n") {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(env, "ask", "7", "-q", "Inline?", "--body", "one `line`", "--json"); !strings.Contains(r.stdout, `"body": "one `+"`line`"+`"`) {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(env, "ask", "7", "-q", "None?", "--json"); r.code != 0 || strings.Contains(r.stdout, `"body"`) {
		t.Fatalf("%+v", r)
	}
}

func TestAmendBodyAndLimitsCLI(t *testing.T) {
	f := newFake(t)
	env := []string{"AGENT_MANAGER_SESSION_ID=s1"}
	if r := f.zen(env, "ask", "7", "-q", "Deploy now?", "--body", "old"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	c := f.cmd(env, zenBin, "amend", "7.1", "--body", "-")
	c.Stdin = strings.NewReader("new body")
	if r := run(c); r.code != 0 || r.stdout != "amended 7.1\n" {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(nil, "show", "7.1", "--json"); !strings.Contains(r.stdout, `"body": "new body"`) || !strings.Contains(r.stdout, `"rev": 1`) {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(env, "amend", "7.1", "--body", ""); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(nil, "show", "7.1", "--json"); strings.Contains(r.stdout, `"body"`) {
		t.Fatalf("%+v", r)
	}
	c = f.cmd(env, zenBin, "ask", "7", "-q", "Big?", "--body", "-")
	c.Stdin = strings.NewReader(strings.Repeat("x", 16*1024+1))
	if r := run(c); r.code != 2 || !strings.Contains(r.stderr, "--body is over 16384 bytes") {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(nil, "list", "--all", "--json"); strings.Contains(r.stdout, "Big?") {
		t.Fatalf("oversized ask stored: %+v", r)
	}
}

func TestProdAskTakesABody(t *testing.T) {
	f := newFake(t)
	r := f.zen(nil, append([]string{"ask", "680", "--session", "s1", "--body", "Why: hotfix", "--json"}, prodArgs...)...)
	if r.code != 0 || !strings.Contains(r.stdout, `"body": "Why: hotfix"`) {
		t.Fatalf("%+v", r)
	}
}
