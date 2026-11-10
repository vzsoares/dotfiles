package inbox

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const testSha = "0123456789abcdef0123456789abcdef01234567"

func prodWant() ProdAction {
	return ProdAction{Action: "release", Repo: "dotfiles", Branch: "prod", Sha: testSha}
}

func askProd(t *testing.T, ib *Inbox, sid string) string {
	t.Helper()
	d, err := ib.AskProd(AskInput{Task: "680", SessionID: sid, Name: "spec-" + sid}, prodWant())
	if err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func usedAt(t *testing.T, ib *Inbox, id string) (string, string) {
	t.Helper()
	ib.mu.Lock()
	defer ib.mu.Unlock()
	p, ok, err := ib.prodAction(id)
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	return p.UsedAt, p.UsedBy
}

func TestAskProdWritesStandardQuestion(t *testing.T) {
	ib, _, _ := open(t)
	d := get(t, ib, askProd(t, ib, "s1"))
	if d.Question != "release dotfiles prod@0123456789ab?" || d.Rec != "a" || d.Gate {
		t.Fatalf("%+v", d)
	}
	if !slices.Equal(d.Options, []Option{{"a", "Approve"}, {"b", "Hold"}}) {
		t.Fatalf("%v", d.Options)
	}
}

func TestAskProdValidation(t *testing.T) {
	ib, _, _ := open(t)
	for _, p := range []ProdAction{
		{Action: "release", Branch: "prod", Sha: testSha},
		{Action: "release", Repo: "dotfiles", Sha: testSha},
		{Action: "release", Repo: "dotfiles", Branch: "prod"},
		{Action: "release", Repo: "dotfiles", Branch: "prod", Sha: testSha[:12]},
		{Action: "release", Repo: "dotfiles", Branch: "prod", Sha: strings.ToUpper(testSha)},
		{Action: "release", Repo: "dotfiles", Branch: "prod", Sha: testSha[:39] + "g"},
		{Action: "", Repo: "dotfiles", Branch: "prod", Sha: testSha},
	} {
		if _, err := ib.AskProd(AskInput{Task: "680", SessionID: "s1"}, p); code(err) != 2 {
			t.Fatalf("%+v: %v", p, err)
		}
	}
	if ds, _ := ib.Select("1"); len(ds) != 0 {
		t.Fatalf("%v", ds)
	}
}

func TestAmendOfProdAskRefused(t *testing.T) {
	ib, _, _ := open(t)
	id := askProd(t, ib, "s1")
	for _, in := range []AmendInput{{Question: p("release other prod@x?")}, {Context: p("c")}, {Options: []string{"Approve", "Go"}}} {
		if _, err := ib.Amend(id, "s1", false, in); code(err) != 1 || !strings.Contains(err.Error(), "withdraw and re-ask") {
			t.Fatalf("%v", err)
		}
	}
	if d := get(t, ib, id); d.Amended || d.Rev != 0 {
		t.Fatalf("%+v", d)
	}
}

func TestApprovalReasons(t *testing.T) {
	want := prodWant()
	other := func(f func(*ProdAction)) ProdAction { w := want; f(&w); return w }
	cases := []struct {
		name   string
		setup  func(ib *Inbox, id string)
		want   ProdAction
		reason string
	}{
		{"open", func(*Inbox, string) {}, want, "not answered"},
		{"hold", func(ib *Inbox, id string) { _, _ = ib.Answer(id, p("b"), nil, "", nil) }, want, "answered Hold"},
		{"talk", func(ib *Inbox, id string) { _, _ = ib.Resolve(id, "") }, want, "resolved in talk"},
		{"text", func(ib *Inbox, id string) { _, _ = ib.Answer(id, nil, p("Approve"), "", nil) }, want, "free text"},
		{"withdrawn", func(ib *Inbox, id string) { _, _ = ib.Withdraw(id, "s1", false) }, want, "withdrawn"},
		{"action", approve, other(func(w *ProdAction) { w.Action = "deploy" }), "action mismatch"},
		{"repo", approve, other(func(w *ProdAction) { w.Repo = "core" }), "repo mismatch: approved dotfiles, releasing core"},
		{"branch", approve, other(func(w *ProdAction) { w.Branch = "main" }), "branch mismatch"},
		{"sha", approve, other(func(w *ProdAction) { w.Sha = strings.Repeat("f", 40) }),
			"sha mismatch: approved " + testSha + ", releasing " + strings.Repeat("f", 40)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ib, _, _ := open(t)
			id := askProd(t, ib, "s1")
			c.setup(ib, id)
			_, err := ib.Approval(id, c.want, "s9")
			if code(err) != 1 || !strings.Contains(err.Error(), c.reason) {
				t.Fatalf("%v", err)
			}
			if at, _ := usedAt(t, ib, id); at != "" {
				t.Fatalf("rejected approval consumed: %s", at)
			}
		})
	}
}

func approve(ib *Inbox, id string) { _, _ = ib.Answer(id, p("a"), nil, "", nil) }

func TestApprovalUnknownAndPlainAsk(t *testing.T) {
	ib, _, _ := open(t)
	if _, err := ib.Approval("680.9", prodWant(), "s9"); code(err) != 1 || !strings.Contains(err.Error(), "unknown id") {
		t.Fatalf("%v", err)
	}
	id := ask(t, ib, "680", "s1", false, "Ship?", "Approve", "Hold")
	approve(ib, id)
	if _, err := ib.Approval(id, prodWant(), "s9"); code(err) != 1 || !strings.Contains(err.Error(), "not a prod-action ask") {
		t.Fatalf("%v", err)
	}
}

func TestApprovalCheckNeverConsumesUseOnce(t *testing.T) {
	ib, _, _ := open(t)
	id := askProd(t, ib, "s1")
	approve(ib, id)
	for range 2 {
		if _, err := ib.Approval(id, prodWant(), ""); err != nil {
			t.Fatal(err)
		}
	}
	if at, by := usedAt(t, ib, id); at != "" || by != "" {
		t.Fatalf("check consumed: %s %s", at, by)
	}
	p, err := ib.Approval(id, prodWant(), "chief")
	if err != nil || p.UsedBy != "chief" || p.UsedAt == "" {
		t.Fatalf("%+v %v", p, err)
	}
	if at, by := usedAt(t, ib, id); at != p.UsedAt || by != "chief" {
		t.Fatalf("%s %s", at, by)
	}
	for _, by := range []string{"chief", ""} {
		_, err := ib.Approval(id, prodWant(), by)
		if code(err) != 1 || !strings.Contains(err.Error(), "already used by chief at "+p.UsedAt) {
			t.Fatalf("%v", err)
		}
	}
}

func createSQL(t *testing.T, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, table string) string {
	t.Helper()
	rows, err := db.QueryContext(ctx, "SELECT sql FROM sqlite_master WHERE tbl_name = ? ORDER BY name", table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s sql.NullString
		_ = rows.Scan(&s)
		out = append(out, s.String)
	}
	return strings.Join(out, "\n")
}

// A v2 reader skips migration on a v3 db, so it only needs the v2 tables to
// keep their exact shape.
func TestV3KeepsV2TablesUnchanged(t *testing.T) {
	dir := t.TempDir()
	v2, err := sql.Open("sqlite", filepath.Join(dir, "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer v2.Close()
	for _, v := range []int{1, 2} {
		for _, stmt := range strings.Split(Migrations[v], ";") {
			if strings.TrimSpace(stmt) != "" {
				if _, err := v2.Exec(stmt); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	ib, _, _ := open(t)
	for _, table := range []string{"decisions", "decision_options", "decision_briefs"} {
		if got, want := createSQL(t, ib.conn, table), createSQL(t, v2, table); got != want {
			t.Fatalf("%s changed:\n%s\n%s", table, got, want)
		}
	}
}
