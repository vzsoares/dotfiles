package main

import (
	"os/exec"
	"strings"
	"testing"
)

const sha40 = "0123456789abcdef0123456789abcdef01234567"

var prodArgs = []string{"--prod", "release", "--repo", "dotfiles", "--branch", "prod", "--sha", sha40}

func approvalArgs(verb, id string) []string {
	return []string{"approval", verb, id, "--action", "release", "--repo", "dotfiles", "--branch", "prod", "--sha", sha40}
}

func TestAskProdUsageErrors(t *testing.T) {
	f := newFake(t)
	base := []string{"ask", "680", "--session", "s1"}
	for _, extra := range [][]string{
		{"--prod", "release", "--branch", "prod", "--sha", sha40},
		{"--prod", "release", "--repo", "dotfiles", "--sha", sha40},
		{"--prod", "release", "--repo", "dotfiles", "--branch", "prod"},
		{"--prod", "release", "--repo", "dotfiles", "--branch", "prod", "--sha", sha40[:12]},
		{"--prod", "release", "--repo", "dotfiles", "--branch", "prod", "--sha", strings.ToUpper(sha40)},
		append([]string{"-q", "Q?"}, prodArgs...),
		append([]string{"-o", "a", "-o", "b"}, prodArgs...),
		append([]string{"--rec", "a"}, prodArgs...),
		append([]string{"--gate"}, prodArgs...),
		{"-q", "Q?", "--sha", sha40},
		{},
	} {
		if r := f.zen(nil, append(append([]string{}, base...), extra...)...); r.code != 2 {
			t.Fatalf("%v: %+v", extra, r)
		}
	}
	if got := f.sqlite("SELECT count(*) FROM decisions"); got != "0\n" {
		t.Fatal(got)
	}
}

func TestAskProdWritesQuestionAndBinding(t *testing.T) {
	f := newFake(t)
	r := f.zen(nil, append([]string{"ask", "680", "--session", "s1", "--json", "--context", "why"}, prodArgs...)...)
	if r.code != 0 || !strings.Contains(r.stdout, `"question": "release dotfiles prod@0123456789ab?"`) ||
		!strings.Contains(r.stdout, `"rec": "a"`) ||
		!strings.Contains(r.stdout, `"options": [{"letter": "a", "text": "Approve"}, {"letter": "b", "text": "Hold"}]`) {
		t.Fatalf("%+v", r)
	}
	if got := f.sqlite("SELECT * FROM prod_actions"); got != "680.1|release|dotfiles|prod|"+sha40+"||\n" {
		t.Fatalf("%q", got)
	}
	if r := f.zen([]string{"AGENT_MANAGER_SESSION_ID=s1"}, "amend", "680.1", "-q", "x?"); r.code != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestApprovalCheckThenUse(t *testing.T) {
	f := newFake(t)
	f.zen(nil, append([]string{"ask", "680", "--session", "s1"}, prodArgs...)...)
	if r := f.zen(nil, approvalArgs("check", "680.1")...); r.code != 1 || r.stderr != "zen-inbox: approval 680.1: not answered\n" {
		t.Fatalf("%+v", r)
	}
	f.zen(nil, "answer", "680.1", "a")
	if r := f.zen(nil, approvalArgs("check", "680.1")...); r.code != 0 || r.stdout != "approved 680.1\n" {
		t.Fatalf("%+v", r)
	}
	if got := f.sqlite("SELECT used_at, used_by FROM prod_actions"); got != "|\n" {
		t.Fatalf("check consumed: %q", got)
	}
	r := f.zen([]string{"AGENT_MANAGER_SESSION_ID=orch"}, approvalArgs("use", "680.1")...)
	if r.code != 0 || r.stdout != "used 680.1 by orch\n" {
		t.Fatalf("%+v", r)
	}
	if got := f.sqlite("SELECT used_at != '', used_by FROM prod_actions"); got != "1|orch\n" {
		t.Fatalf("%q", got)
	}
	if r := f.zen(nil, approvalArgs("use", "680.1")...); r.code != 1 || !strings.Contains(r.stderr, "already used by orch at ") {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(nil, "approval", "use", "680.1", "--action", "release"); r.code != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestApprovalUseDefaultsToUser(t *testing.T) {
	f := newFake(t)
	f.zen(nil, append([]string{"ask", "680", "--session", "s1"}, prodArgs...)...)
	f.zen(nil, "answer", "680.1", "a")
	if r := f.zen(nil, approvalArgs("use", "680.1")...); r.code != 0 || r.stdout != "used 680.1 by user\n" {
		t.Fatalf("%+v", r)
	}
}

func TestConcurrentApprovalUseOneWins(t *testing.T) {
	f := newFake(t)
	f.zen(nil, append([]string{"ask", "680", "--session", "s1"}, prodArgs...)...)
	f.zen(nil, "answer", "680.1", "a")
	var cmds []*exec.Cmd
	for range 8 {
		cmds = append(cmds, f.cmd(nil, append([]string{zenBin}, approvalArgs("use", "680.1")...)...))
	}
	won := 0
	for _, r := range parallel(cmds) {
		switch {
		case r.code == 0:
			won++
		case r.code != 1 || !strings.Contains(r.stderr, "already used"):
			t.Fatalf("%+v", r)
		}
	}
	if won != 1 {
		t.Fatalf("%d runs consumed one approval", won)
	}
}

func TestV2FixtureMigratesToV3(t *testing.T) {
	f := newFake(t)
	f.load("testdata/xver/v2.sql")
	before := f.sqlite("SELECT * FROM decisions ORDER BY id")
	if r := f.zen(nil, "list", "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if v := strings.TrimSpace(f.sqlite("PRAGMA user_version")); v != "3" {
		t.Fatal(v)
	}
	if after := f.sqlite("SELECT * FROM decisions ORDER BY id"); after != before {
		t.Fatalf("migration changed rows:\n%s\n%s", before, after)
	}
	f.zen(nil, append([]string{"ask", "680", "--session", "s1"}, prodArgs...)...)
	f.zen(nil, "answer", "680.1", "a")
	if r := f.zen(nil, approvalArgs("use", "680.1")...); r.code != 0 {
		t.Fatalf("%+v", r)
	}
}
