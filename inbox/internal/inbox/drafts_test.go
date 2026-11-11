package inbox

import (
	"database/sql"
	"maps"
	"path/filepath"
	"strings"
	"testing"
)

func drafts(t *testing.T, ib *Inbox) map[string]Draft {
	t.Helper()
	out, err := ib.Drafts()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func save(t *testing.T, ib *Inbox, d Draft) {
	t.Helper()
	if err := ib.SaveDraft(d); err != nil {
		t.Fatal(err)
	}
}

func TestDraftSaveLoadAndReplace(t *testing.T) {
	ib, _, path := open(t)
	a, b, c := ask(t, ib, "7", "s1", false, "A?"), ask(t, ib, "7", "s1", false, "B?"), ask(t, ib, "7", "s1", false, "C?")
	save(t, ib, Draft{ID: a, Kind: "pick", Option: "a"})
	save(t, ib, Draft{ID: b, Kind: "text", Text: "multi\nline"})
	save(t, ib, Draft{ID: c, Kind: "skip"})
	save(t, ib, Draft{ID: a, Kind: "pick", Option: "b"})
	want := map[string]Draft{
		a: {ID: a, Kind: "pick", Option: "b"},
		b: {ID: b, Kind: "text", Text: "multi\nline"},
		c: {ID: c, Kind: "skip"},
	}
	_ = ib.Close()
	again, err := Open(path, &fakeAM{})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if got := drafts(t, again); !maps.Equal(got, want) {
		t.Fatalf("%v", got)
	}
	if err := again.SaveDraft(Draft{ID: a, Kind: "bogus"}); code(err) != 2 {
		t.Fatalf("%v", err)
	}
	if err := again.DeleteDraft(c); err != nil {
		t.Fatal(err)
	}
	if _, ok := drafts(t, again)[c]; ok {
		t.Fatal("deleted draft still loaded")
	}
}

func TestDraftsClearedOnSend(t *testing.T) {
	ib, fake, _ := open(t)
	a, b := ask(t, ib, "7", "s1", false, "A?"), ask(t, ib, "7", "s1", false, "B?")
	other := ask(t, ib, "8", "s2", false, "Other?")
	save(t, ib, Draft{ID: a, Kind: "pick", Option: "a"})
	save(t, ib, Draft{ID: b, Kind: "skip"})
	save(t, ib, Draft{ID: other, Kind: "skip"})
	if _, err := ib.Answer(a, p("a"), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	if got := drafts(t, ib); len(got) != 2 || got[a] != (Draft{}) {
		t.Fatalf("answered draft kept: %v", got)
	}
	if err := ib.ClearDrafts("s1"); err != nil {
		t.Fatal(err)
	}
	if got := drafts(t, ib); len(got) != 1 || got[other].Kind != "skip" {
		t.Fatalf("clear reached another session: %v", got)
	}
	if len(fake.sends) != 0 {
		t.Fatalf("%v", fake.sends)
	}
}

func TestDraftDroppedOnCloseAndAmend(t *testing.T) {
	ib, _, _ := open(t)
	w, r, am := ask(t, ib, "7", "s1", false, "W?"), ask(t, ib, "7", "s1", false, "R?"), ask(t, ib, "7", "s1", false, "Am?")
	keep := ask(t, ib, "7", "s1", false, "Keep?")
	for _, id := range []string{w, r, am, keep} {
		save(t, ib, Draft{ID: id, Kind: "pick", Option: "a"})
	}
	if _, err := ib.Withdraw(w, "s1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.Resolve(r, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.Amend(am, "s1", false, AmendInput{Question: p("Amended?")}); err != nil {
		t.Fatal(err)
	}
	if got := drafts(t, ib); len(got) != 1 || got[keep].Option != "a" {
		t.Fatalf("%v", got)
	}
	var n int
	if err := ib.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM decision_drafts").Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows left %d %v", n, err)
	}
	save(t, ib, Draft{ID: w, Kind: "skip"})
	if _, ok := drafts(t, ib)[w]; ok {
		t.Fatal("draft saved on a closed decision")
	}
}

// A draft staged against an older rev never loads, even if a write path
// missed dropping it.
func TestDraftAtStaleRevNotLoaded(t *testing.T) {
	ib, _, _ := open(t)
	id := ask(t, ib, "7", "s1", false, "A?")
	save(t, ib, Draft{ID: id, Kind: "pick", Option: "a", Rev: 0})
	if err := ib.Exec("UPDATE decisions SET rev = 3 WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	if got := drafts(t, ib); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestProdApproveNeverPersisted(t *testing.T) {
	ib, _, _ := open(t)
	id := askProd(t, ib, "s1")
	save(t, ib, Draft{ID: id, Kind: "pick", Option: "b"})
	if got := drafts(t, ib)[id]; got.Option != "b" {
		t.Fatalf("Hold not kept: %v", got)
	}
	save(t, ib, Draft{ID: id, Kind: "pick", Option: "a"})
	if got, ok := drafts(t, ib)[id]; ok {
		t.Fatalf("Approve persisted, or the stale Hold kept: %v", got)
	}
	plain := ask(t, ib, "7", "s1", false, "Ship?", "Approve", "Hold")
	save(t, ib, Draft{ID: plain, Kind: "pick", Option: "a"})
	if _, ok := drafts(t, ib)[plain]; !ok {
		t.Fatal("a plain ask's Approve is an ordinary draft")
	}
}

func TestV3DBMigratesToV4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zen-flow.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= 3; v++ {
		for _, stmt := range strings.Split(Migrations[v], ";") {
			if strings.TrimSpace(stmt) != "" {
				if _, err := raw.Exec(stmt); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, stmt := range []string{
		"PRAGMA user_version = 3",
		"INSERT INTO decisions (id, task, seq, session_id, session_name, question, created_at, updated_at)" +
			" VALUES ('7.1', '7', 1, 's1', 'spec-s1', 'Old?', '2026-01-01T00:00:00+00:00', '2026-01-01T00:00:00+00:00')",
		"INSERT INTO decision_options VALUES ('7.1', 'a', 'x'), ('7.1', 'b', 'y')",
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	v3 := map[string]string{}
	for _, table := range []string{"decisions", "decision_options", "decision_briefs", "prod_actions"} {
		v3[table] = createSQL(t, raw, table)
	}
	_ = raw.Close()
	ib, err := Open(path, &fakeAM{})
	if err != nil {
		t.Fatal(err)
	}
	defer ib.Close()
	if v, _ := ib.userVersion(); v != SchemaVersion {
		t.Fatalf("user_version %d", v)
	}
	for table, want := range v3 {
		if got := createSQL(t, ib.conn, table); got != want {
			t.Fatalf("%s changed:\n%s\n%s", table, got, want)
		}
	}
	if d := get(t, ib, "7.1"); d.Question != "Old?" || len(d.Options) != 2 {
		t.Fatalf("%+v", d)
	}
	save(t, ib, Draft{ID: "7.1", Kind: "pick", Option: "b"})
	if got := drafts(t, ib)["7.1"]; got.Option != "b" {
		t.Fatalf("%v", got)
	}
}
