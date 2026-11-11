package inbox

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

const runCmd = "uv run scripts/publish.py --apply  --so prod"

func runWant() ProdAction { return ProdAction{Action: RunAction, Repo: "task-flow", Cmd: runCmd} }

// withCmd is a pre-v6 prod_actions CREATE after v6's ALTER.
func withCmd(create string) string {
	i := strings.LastIndex(create, ")")
	return create[:i] + ", cmd TEXT NOT NULL DEFAULT '')" + create[i+1:]
}

func askRun(t *testing.T, ib *Inbox) string {
	t.Helper()
	d, err := ib.AskProd(AskInput{Task: "736", SessionID: "s1", Name: "spec-s1"}, runWant())
	if err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func TestAskRunBindsCommand(t *testing.T) {
	ib, _, _ := open(t)
	d := get(t, ib, askRun(t, ib))
	if d.Question != "run in task-flow: "+runCmd+"?" || d.Prod == nil || *d.Prod != runWant() {
		t.Fatalf("%+v %+v", d, d.Prod)
	}
}

func TestAskRunValidation(t *testing.T) {
	ib, _, _ := open(t)
	run := func(f func(*ProdAction)) ProdAction { w := runWant(); f(&w); return w }
	for _, p := range []ProdAction{
		run(func(w *ProdAction) { w.Cmd = "" }),
		run(func(w *ProdAction) { w.Cmd = "   " }),
		run(func(w *ProdAction) { w.Repo = "" }),
		run(func(w *ProdAction) { w.Branch = "prod" }),
		run(func(w *ProdAction) { w.Sha = testSha }),
		run(func(w *ProdAction) { w.Cmd = "ok\nrm -rf /" }),
		run(func(w *ProdAction) { w.Cmd = "ok\rhidden" }),
		run(func(w *ProdAction) { w.Cmd = "ok \x1b[2Khidden" }),
		run(func(w *ProdAction) { w.Cmd = "ok ‮dorp" }),
		run(func(w *ProdAction) { w.Cmd = "ok \xff" }),
		run(func(w *ProdAction) { w.Cmd = "ok\u2028rm -rf /" }),
		run(func(w *ProdAction) { w.Cmd = "ok\u00a0--apply" }),
		run(func(w *ProdAction) { w.Repo = "core\nother" }),
		{Action: "release", Repo: "dotfiles", Branch: "prod", Sha: testSha, Cmd: "x"},
	} {
		if _, err := ib.AskProd(AskInput{Task: "736", SessionID: "s1"}, p); code(err) != 2 {
			t.Fatalf("%+v: %v", p, err)
		}
	}
	if ds, _ := ib.Select("1"); len(ds) != 0 {
		t.Fatalf("%v", ds)
	}
}

func TestRunApprovalExactCommandOnce(t *testing.T) {
	ib, _, _ := open(t)
	ib.User = true
	id := askRun(t, ib)
	approve(ib, id)
	for _, cmd := range []string{
		runCmd + " ",
		strings.Replace(runCmd, "  ", " ", 1),
		strings.Replace(runCmd, "prod", "dev", 1),
		strings.ToUpper(runCmd),
	} {
		want := runWant()
		want.Cmd = cmd
		if _, err := ib.Approval(id, want, "s9"); code(err) != 1 || !strings.Contains(err.Error(), "command mismatch") {
			t.Fatalf("%q: %v", cmd, err)
		}
		if _, _, err := ib.ApprovalMatch(want, "s9"); code(err) != 1 {
			t.Fatalf("match %q: %v", cmd, err)
		}
	}
	other := runWant()
	other.Repo = "core"
	if _, err := ib.Approval(id, other, "s9"); code(err) != 1 || !strings.Contains(err.Error(), "repo mismatch") {
		t.Fatalf("%v", err)
	}
	if at, _ := usedAt(t, ib, id); at != "" {
		t.Fatalf("mismatch consumed: %s", at)
	}
	if _, err := ib.Approval(id, runWant(), "orch"); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.Approval(id, runWant(), "orch"); code(err) != 1 || !strings.Contains(err.Error(), "already used by orch") {
		t.Fatalf("%v", err)
	}
	if _, _, err := ib.ApprovalMatch(runWant(), "orch"); code(err) != 1 {
		t.Fatalf("%v", err)
	}
}

func TestRunApprovalMatchConsumesOnce(t *testing.T) {
	ib, _, _ := open(t)
	ib.User = true
	id := askRun(t, ib)
	approve(ib, id)
	got, p, err := ib.ApprovalMatch(runWant(), "w1")
	if err != nil || got != id || p.UsedBy != "w1" {
		t.Fatalf("%s %+v %v", got, p, err)
	}
	if _, _, err := ib.ApprovalMatch(runWant(), "w1"); code(err) != 1 {
		t.Fatalf("%v", err)
	}
}

func TestRunApprovalRefusesHoldTextAndAgents(t *testing.T) {
	for name, answer := range map[string]func(*Inbox, string){
		"hold": func(ib *Inbox, id string) { _, _ = ib.Answer(id, p("b"), nil, "", nil) },
		"text": func(ib *Inbox, id string) { _, _ = ib.Answer(id, nil, p("Approve"), "", nil) },
		"talk": func(ib *Inbox, id string) { _, _ = ib.Resolve(id, "") },
	} {
		t.Run(name, func(t *testing.T) {
			ib, _, _ := open(t)
			ib.User = true
			id := askRun(t, ib)
			answer(ib, id)
			if _, err := ib.Approval(id, runWant(), "s9"); code(err) != 1 {
				t.Fatalf("%v", err)
			}
		})
	}
	ib, _, _ := open(t)
	id := askRun(t, ib)
	if _, err := ib.Answer(id, p("a"), nil, "", nil); code(err) != 1 || !strings.Contains(err.Error(), "answer it in the decisions panel") {
		t.Fatalf("%v", err)
	}
	if _, err := ib.Approval(id, runWant(), "s9"); code(err) != 1 || !strings.Contains(err.Error(), "not answered") {
		t.Fatalf("%v", err)
	}
}

func TestRunWantNeverMatchesPushApproval(t *testing.T) {
	ib, _, _ := open(t)
	ib.User = true
	id := askProd(t, ib, "s1")
	approve(ib, id)
	want := runWant()
	if _, err := ib.Approval(id, want, "s9"); code(err) != 1 || !strings.Contains(err.Error(), "action mismatch") {
		t.Fatalf("%v", err)
	}
}

func TestV5DBMigratesToV6(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zen-flow.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= 5; v++ {
		for _, stmt := range strings.Split(Migrations[v], ";") {
			if strings.TrimSpace(stmt) != "" {
				if _, err := raw.Exec(stmt); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, stmt := range []string{
		"PRAGMA user_version = 5",
		"INSERT INTO decisions (id, task, seq, session_id, session_name, question, created_at, updated_at)" +
			" VALUES ('703.1', '703', 1, 's1', 'spec-s1', 'push dotfiles master@0123456789ab?'," +
			" '2026-01-01T00:00:00+00:00', '2026-01-01T00:00:00+00:00')",
		"INSERT INTO decision_options VALUES ('703.1', 'a', 'Approve'), ('703.1', 'b', 'Hold')",
		"INSERT INTO prod_actions (decision_id, action, repo, branch, sha) VALUES ('703.1', 'push', 'dotfiles', 'master', '" + testSha + "')",
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	_ = raw.Close()
	ib, err := Open(path, &fakeAM{})
	if err != nil {
		t.Fatal(err)
	}
	defer ib.Close()
	if v, _ := ib.userVersion(); v != 7 {
		t.Fatalf("user_version %d", v)
	}
	ib.User = true
	approve(ib, "703.1")
	push := ProdAction{Action: "push", Repo: "dotfiles", Branch: "master", Sha: testSha}
	if p, err := ib.Approval("703.1", push, "w1"); err != nil || p.Cmd != "" {
		t.Fatalf("%+v %v", p, err)
	}
}
