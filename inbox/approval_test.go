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

// approve writes the panel's Approve straight to the db: the CLI can never
// answer a prod-action ask.
func (f *fake) approve(id string) {
	f.t.Helper()
	f.sqlite("UPDATE decisions SET status = 'answered', answer_kind = 'pick', answer_option = 'a'," +
		" delivery = 'sent', answered_at = '2026-01-01T00:00:00', rev = rev + 1 WHERE id = '" + id + "'")
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
	f.approve("680.1")
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
	f.approve("680.1")
	if r := f.zen(nil, approvalArgs("use", "680.1")...); r.code != 0 || r.stdout != "used 680.1 by user\n" {
		t.Fatalf("%+v", r)
	}
}

func TestConcurrentApprovalUseOneWins(t *testing.T) {
	f := newFake(t)
	f.zen(nil, append([]string{"ask", "680", "--session", "s1"}, prodArgs...)...)
	f.approve("680.1")
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
	if v := strings.TrimSpace(f.sqlite("PRAGMA user_version")); v != "4" {
		t.Fatal(v)
	}
	if after := f.sqlite("SELECT * FROM decisions ORDER BY id"); after != before {
		t.Fatalf("migration changed rows:\n%s\n%s", before, after)
	}
	f.zen(nil, append([]string{"ask", "680", "--session", "s1"}, prodArgs...)...)
	f.approve("680.1")
	if r := f.zen(nil, approvalArgs("use", "680.1")...); r.code != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestCLIRefusesProdAnswer(t *testing.T) {
	refused := "zen-inbox: 680.1 is a prod-action ask; answer it in the decisions panel\n"
	for _, c := range []struct {
		name string
		env  []string
		args []string
	}{
		{"other agent", []string{"AGENT_MANAGER_SESSION_ID=s2"}, []string{"answer", "680.1", "a"}},
		{"asker", []string{"AGENT_MANAGER_SESSION_ID=s1"}, []string{"answer", "680.1", "a"}},
		{"no session", nil, []string{"answer", "680.1", "a"}},
		{"free text", nil, []string{"answer", "680.1", "--text", "Approve"}},
		{"hold", []string{"AGENT_MANAGER_SESSION_ID=s1"}, []string{"answer", "680.1", "b"}},
		{"resolve", nil, []string{"resolve", "680.1"}},
		{"resolve asker", []string{"AGENT_MANAGER_SESSION_ID=s1"}, []string{"resolve", "680.1"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFake(t)
			f.zen(nil, append([]string{"ask", "680", "--session", "s1"}, prodArgs...)...)
			if r := f.zen(c.env, c.args...); r.code != 1 || r.stderr != refused || r.stdout != "" {
				t.Fatalf("%+v", r)
			}
			if got := f.sqlite("SELECT status, rev FROM decisions"); got != "open|0\n" {
				t.Fatalf("%q", got)
			}
			if r := f.zen(nil, approvalArgs("check", "680.1")...); r.code != 1 || !strings.Contains(r.stderr, "not answered") {
				t.Fatalf("%+v", r)
			}
			if len(f.sends()) != 0 {
				t.Fatal(f.sends())
			}
		})
	}
}

func TestCLIWithdrawProdAskUnchanged(t *testing.T) {
	f := newFake(t)
	f.zen(nil, append([]string{"ask", "680", "--session", "s1"}, prodArgs...)...)
	f.zen(nil, append([]string{"ask", "680", "--session", "s2"}, prodArgs...)...)
	if r := f.zen([]string{"AGENT_MANAGER_SESSION_ID=s1"}, "withdraw", "680.1"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := f.zen([]string{"AGENT_MANAGER_SESSION_ID=chief"}, "withdraw", "680.2", "--force"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if got := f.sqlite("SELECT status FROM decisions ORDER BY id"); got != "withdrawn\nwithdrawn\n" {
		t.Fatalf("%q", got)
	}
}

func TestCLIPlainAskFromAgentStillAnswers(t *testing.T) {
	f := newFake(t)
	f.zen(nil, "ask", "7", "--session", "s1", "-q", "Ship?", "-o", "Approve", "-o", "Hold")
	r := f.zen([]string{"AGENT_MANAGER_SESSION_ID=s2"}, "answer", "7.1", "b")
	if r.code != 0 || f.sqlite("SELECT status, answer_option, delivery FROM decisions") != "answered|b|sent\n" {
		t.Fatalf("%+v", r)
	}
	if sends := f.sends(); len(sends) != 1 || !strings.Contains(sends[0][2], "7.1 Ship? → b: Hold") {
		t.Fatal(sends)
	}
}

func matchArgs(verb string) []string {
	return []string{"approval", verb, "--match", "--action", "push", "--repo", "dotfiles", "--branch", "master", "--sha", sha40}
}

var pushArgs = []string{"--prod", "push", "--repo", "dotfiles", "--branch", "master", "--sha", sha40}

func TestApprovalMatchCLI(t *testing.T) {
	f := newFake(t)
	f.zen(nil, append([]string{"ask", "703", "--session", "s1"}, pushArgs...)...)
	noMatch := "zen-inbox: no approved unused ask for push dotfiles master@0123456789ab\n"
	if r := f.zen(nil, matchArgs("check")...); r.code != 1 || r.stderr != noMatch {
		t.Fatalf("%+v", r)
	}
	f.approve("703.1")
	if r := f.zen(nil, matchArgs("check")...); r.code != 0 || r.stdout != "approved 703.1\n" {
		t.Fatalf("%+v", r)
	}
	if got := f.sqlite("SELECT used_at FROM prod_actions"); got != "\n" {
		t.Fatalf("check consumed: %q", got)
	}
	r := f.zen([]string{"AGENT_MANAGER_SESSION_ID=w1"}, matchArgs("use")...)
	if r.code != 0 || r.stdout != "used 703.1 by w1\n" {
		t.Fatalf("%+v", r)
	}
	if r := f.zen(nil, matchArgs("use")...); r.code != 1 || r.stderr != noMatch {
		t.Fatalf("%+v", r)
	}
}

func TestApprovalMatchUsageErrors(t *testing.T) {
	f := newFake(t)
	fields := []string{"--action", "push", "--repo", "dotfiles", "--branch", "master", "--sha", sha40}
	for _, args := range [][]string{
		append([]string{"approval", "use", "703.1", "--match"}, fields...),
		append([]string{"approval", "check", "703.1", "--match"}, fields...),
		append([]string{"approval", "use"}, fields...),
		append([]string{"approval", "check"}, fields...),
		{"approval", "use", "--match", "--action", "push", "--repo", "dotfiles", "--branch", "master"},
	} {
		if r := f.zen(nil, args...); r.code != 2 {
			t.Fatalf("%v: %+v", args, r)
		}
	}
}

func TestApprovalUsedCLI(t *testing.T) {
	f := newFake(t)
	f.zen(nil, append([]string{"ask", "680", "--session", "s1"}, prodArgs...)...)
	used := []string{"approval", "used", "680.1", "--action", "release", "--repo", "dotfiles", "--branch", "prod"}
	if r := f.zen(nil, used...); r.code != 1 || r.stderr != "zen-inbox: approval 680.1: not answered\n" {
		t.Fatalf("%+v", r)
	}
	f.approve("680.1")
	if r := f.zen(nil, used...); r.code != 1 || r.stderr != "zen-inbox: approval 680.1: not used\n" {
		t.Fatalf("%+v", r)
	}
	f.zen(nil, approvalArgs("use", "680.1")...)
	before := f.sqlite("SELECT * FROM prod_actions")
	if r := f.zen(nil, used...); r.code != 0 || r.stdout != sha40+"\n" {
		t.Fatalf("%+v", r)
	}
	if after := f.sqlite("SELECT * FROM prod_actions"); after != before {
		t.Fatalf("used wrote: %q -> %q", before, after)
	}
	for _, args := range [][]string{
		{"approval", "used", "680.1", "--action", "release", "--repo", "dotfiles"},
		{"approval", "used", "680.1", "--action", "release", "--branch", "prod"},
		{"approval", "used", "--action", "release", "--repo", "dotfiles", "--branch", "prod"},
	} {
		if r := f.zen(nil, args...); r.code != 2 {
			t.Fatalf("%v: %+v", args, r)
		}
	}
	if r := f.zen(nil, "approval", "used", "680.1", "--action", "push", "--repo", "dotfiles", "--branch", "prod"); r.code != 1 {
		t.Fatalf("%+v", r)
	}
}
