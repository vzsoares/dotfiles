package inbox

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
			ib.User = true
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
	ib.User = true
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

func TestProdAskClosesOnlyForUser(t *testing.T) {
	ib, fake, _ := open(t)
	id := askProd(t, ib, "s1")
	refused := id + " is a prod-action ask; answer it in the decisions panel"
	for name, close := range map[string]func() error{
		"pick a": func() error { _, err := ib.Answer(id, p("a"), nil, "", nil); return err },
		"pick b": func() error { _, err := ib.Answer(id, p("b"), nil, "", nil); return err },
		"text":   func() error { _, err := ib.Answer(id, nil, p("Approve"), "", nil); return err },
		"talk":   func() error { _, err := ib.Resolve(id, ""); return err },
		"raw":    func() error { return ib.CloseRaw(id, "answered", "pick", "a", "", "held") },
	} {
		if err := close(); code(err) != 1 || err.Error() != refused {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if d := get(t, ib, id); d.Status != "open" || d.Rev != 0 || d.Prod == nil || d.Prod.Sha != testSha {
		t.Fatalf("%+v", d)
	}
	if len(fake.sends) != 0 {
		t.Fatalf("%v", fake.sends)
	}
	ib.User = true
	if _, err := ib.Answer(id, p("a"), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	if d := get(t, ib, id); d.Status != "answered" || d.AnswerKind != "pick" || d.AnswerOption != "a" {
		t.Fatalf("%+v", d)
	}
	if got, err := ib.Approval(id, prodWant(), "x"); err != nil || got.UsedBy != "x" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestProdAskWithdrawUnchanged(t *testing.T) {
	ib, _, _ := open(t)
	own, forced := askProd(t, ib, "s1"), askProd(t, ib, "s2")
	if _, err := ib.Withdraw(own, "s1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.Withdraw(forced, "chief", false); code(err) != 1 {
		t.Fatalf("%v", err)
	}
	if _, err := ib.Withdraw(forced, "chief", true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{own, forced} {
		if d := get(t, ib, id); d.Status != "withdrawn" {
			t.Fatalf("%+v", d)
		}
	}
}

func TestPlainAskAnswersWithoutUser(t *testing.T) {
	ib, fake, _ := open(t)
	id := ask(t, ib, "7", "s1", false, "Ship?", "Approve", "Hold")
	if _, err := ib.Answer(id, p("a"), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	if d := get(t, ib, id); d.Status != "answered" || d.Prod != nil || len(fake.sends) != 1 {
		t.Fatalf("%+v %v", d, fake.sends)
	}
}

func pushWant() ProdAction {
	return ProdAction{Action: "push", Repo: "dotfiles", Branch: "master", Sha: testSha}
}

func askPush(t *testing.T, ib *Inbox, sid string, p ProdAction) string {
	t.Helper()
	d, err := ib.AskProd(AskInput{Task: "703", SessionID: sid}, p)
	if err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func TestApprovalMatchUsesOldestApprovedUnused(t *testing.T) {
	ib, _, _ := open(t)
	ib.User = true
	want := pushWant()
	used := askPush(t, ib, "s1", want)
	approve(ib, used)
	if _, err := ib.Approval(used, want, "earlier"); err != nil {
		t.Fatal(err)
	}
	hold, text, talk, opened, withdrawn := askPush(t, ib, "s1", want), askPush(t, ib, "s1", want),
		askPush(t, ib, "s1", want), askPush(t, ib, "s1", want), askPush(t, ib, "s1", want)
	_, _ = ib.Answer(hold, p("b"), nil, "", nil)
	_, _ = ib.Answer(text, nil, p("Approve"), "", nil)
	_, _ = ib.Resolve(talk, "")
	_, _ = ib.Withdraw(withdrawn, "s1", false)
	other := want
	other.Sha = strings.Repeat("f", 40)
	others := []string{askPush(t, ib, "s1", other)}
	for _, f := range []func(*ProdAction){
		func(w *ProdAction) { w.Action = "release" },
		func(w *ProdAction) { w.Repo = "core" },
		func(w *ProdAction) { w.Branch = "main" },
	} {
		w := want
		f(&w)
		others = append(others, askPush(t, ib, "s1", w))
	}
	for _, id := range others {
		approve(ib, id)
	}
	first, second := askPush(t, ib, "s1", want), askPush(t, ib, "s1", want)
	approve(ib, second)
	approve(ib, first)

	id, _, err := ib.ApprovalMatch(want, "")
	if err != nil || id != first {
		t.Fatalf("check: %s %v", id, err)
	}
	if at, _ := usedAt(t, ib, first); at != "" {
		t.Fatalf("check consumed %s", first)
	}
	for _, wantID := range []string{first, second} {
		id, got, err := ib.ApprovalMatch(want, "hook")
		if err != nil || id != wantID || got.UsedBy != "hook" {
			t.Fatalf("use: %s %+v %v", id, got, err)
		}
	}
	_, _, err = ib.ApprovalMatch(want, "hook")
	if code(err) != 1 || err.Error() != "no approved unused ask for push dotfiles master@0123456789ab" {
		t.Fatalf("%v", err)
	}
	if _, by := usedAt(t, ib, used); by != "earlier" {
		t.Fatalf("used row rewritten: %s", by)
	}
	for _, id := range append([]string{hold, text, talk, opened, withdrawn}, others...) {
		if at, _ := usedAt(t, ib, id); at != "" {
			t.Fatalf("%s consumed", id)
		}
	}
	if d := get(t, ib, opened); d.Status != "open" {
		t.Fatalf("%+v", d)
	}
}

func TestApprovalMatchValidation(t *testing.T) {
	ib, _, _ := open(t)
	bad := pushWant()
	bad.Sha = testSha[:12]
	if _, _, err := ib.ApprovalMatch(bad, ""); code(err) != 2 {
		t.Fatalf("%v", err)
	}
}

func TestConcurrentApprovalMatchOneWins(t *testing.T) {
	ib, _, path := open(t)
	ib.User = true
	id := askPush(t, ib, "s1", pushWant())
	approve(ib, id)
	const n = 8
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := range n {
		other, err := Open(path, &fakeAM{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = other.Close() })
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := other.ApprovalMatch(pushWant(), fmt.Sprintf("w%d", i))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	won := 0
	for err := range errs {
		switch {
		case err == nil:
			won++
		case code(err) != 1 || !strings.Contains(err.Error(), "no approved unused ask"):
			t.Fatalf("%v", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d calls consumed one approval", won)
	}
}

func TestApprovalUsed(t *testing.T) {
	release := ProdAction{Action: "release", Repo: "dotfiles", Branch: "prod"}
	other := func(f func(*ProdAction)) ProdAction { w := release; f(&w); return w }
	consume := func(ib *Inbox, id string) {
		approve(ib, id)
		if _, err := ib.Approval(id, prodWant(), "rel"); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name   string
		setup  func(ib *Inbox, id string)
		want   ProdAction
		reason string
	}{
		{"unused", approve, release, "not used"},
		{"open", func(*Inbox, string) {}, release, "not answered"},
		{"hold", func(ib *Inbox, id string) { _, _ = ib.Answer(id, p("b"), nil, "", nil) }, release, "answered Hold"},
		{"text", func(ib *Inbox, id string) { _, _ = ib.Answer(id, nil, p("Approve"), "", nil) }, release, "free text"},
		{"talk", func(ib *Inbox, id string) { _, _ = ib.Resolve(id, "") }, release, "resolved in talk"},
		{"action", consume, other(func(w *ProdAction) { w.Action = "push" }), "action mismatch"},
		{"repo", consume, other(func(w *ProdAction) { w.Repo = "core" }), "repo mismatch"},
		{"branch", consume, other(func(w *ProdAction) { w.Branch = "main" }), "branch mismatch"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ib, _, _ := open(t)
			ib.User = true
			id := askProd(t, ib, "s1")
			c.setup(ib, id)
			before, _ := usedAt(t, ib, id)
			if _, err := ib.ApprovalUsed(id, c.want); code(err) != 1 || !strings.Contains(err.Error(), c.reason) {
				t.Fatalf("%v", err)
			}
			if after, _ := usedAt(t, ib, id); after != before {
				t.Fatalf("used changed: %q -> %q", before, after)
			}
		})
	}
	ib, _, _ := open(t)
	ib.User = true
	id := askProd(t, ib, "s1")
	consume(ib, id)
	at, _ := usedAt(t, ib, id)
	if sha, err := ib.ApprovalUsed(id, release); err != nil || sha != testSha {
		t.Fatalf("%s %v", sha, err)
	}
	if again, _ := usedAt(t, ib, id); again != at {
		t.Fatal("used rewrote the row")
	}
	if _, err := ib.ApprovalUsed("703.9", release); code(err) != 1 || !strings.Contains(err.Error(), "unknown id") {
		t.Fatalf("%v", err)
	}
	plain := ask(t, ib, "7", "s1", false, "Ship?", "Approve", "Hold")
	approve(ib, plain)
	if _, err := ib.ApprovalUsed(plain, release); code(err) != 1 || !strings.Contains(err.Error(), "not a prod-action ask") {
		t.Fatalf("%v", err)
	}
	if _, err := ib.ApprovalUsed(id, ProdAction{Action: "release", Repo: "dotfiles"}); code(err) != 2 {
		t.Fatalf("%v", err)
	}
}
