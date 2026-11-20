package inbox

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestChatInsertThenStatusUpdates(t *testing.T) {
	ib, _, _ := open(t)
	at(t, "2026-01-01T00:00:00+00:00")
	c, err := ib.AddChat("843", "how is it going?")
	if err != nil || c.ID != 1 || c.Status != "queued" || c.SentAt != "2026-01-01T00:00:00+00:00" {
		t.Fatalf("%+v %v", c, err)
	}
	if err := ib.UpdateChat(c.ID, "m7", "queued", ""); err != nil {
		t.Fatal(err)
	}
	if err := ib.UpdateChat(c.ID, "", "delivered", ""); err != nil {
		t.Fatal(err)
	}
	got, err := ib.Chat(c.ID)
	if err != nil || got.MessageID != "m7" || got.Status != "delivered" || got.Task != "843" || got.Text != "how is it going?" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestChatStoresTheErrorOnFailure(t *testing.T) {
	ib, _, _ := open(t)
	c, err := ib.AddChat("", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if err := ib.UpdateChat(c.ID, "", "failed", "chief is gone"); err != nil {
		t.Fatal(err)
	}
	if got, _ := ib.Chat(c.ID); got.Status != "failed" || got.Error != "chief is gone" || got.MessageID != "" {
		t.Fatalf("%+v", got)
	}
	var ie *Error
	if _, err := ib.Chat(99); !errors.As(err, &ie) || ie.Code != 1 {
		t.Fatalf("%v", err)
	}
}

// v9Tables are the tables a v9 db holds, with the columns a v9 binary reads.
var v9Tables = map[string]string{
	"decisions":        v10Columns,
	"decision_options": "decision_id, letter, text",
	"decision_briefs":  "session_id, task, title, brief, updated_at",
	"prod_actions":     "decision_id, action, repo, branch, sha, used_at, used_by, cmd, pushed_sha, pushed_at, pushed_by",
	"decision_drafts":  "decision_id, kind, option, text, rev, updated_at",
	"notes":            noteColumns,
	"note_replies":     "id, note_id, text, replied_at",
}

func TestV9DumpMigratesToV10(t *testing.T) {
	dump, err := os.ReadFile("../../testdata/xver/v9.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "zen-flow.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(string(dump)); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	rows := map[string]string{}
	for table, cols := range v9Tables {
		before[table] = createSQL(t, raw, table)
		rows[table] = tableRows(t, raw, table, cols)
		if rows[table] == "" {
			t.Fatalf("%s: fixture has no rows", table)
		}
	}
	_ = raw.Close()

	ib, err := Open(path, &fakeAM{})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := ib.userVersion(); v != SchemaVersion {
		t.Fatalf("user_version %d", v)
	}
	for table := range v9Tables {
		want := before[table]
		if table == "decisions" {
			want = withBody(want)
		}
		if got := createSQL(t, ib.conn, table); got != want {
			t.Fatalf("%s changed:\n%s\n%s", table, got, want)
		}
	}
	th, err := ib.Thread("843")
	if err != nil || len(th.Items) != 3 || th.Title != "Phone cockpit setup" {
		t.Fatalf("%+v %v", th, err)
	}
	if n := th.Items[2].Note; n == nil || len(n.Replies) != 1 || n.Replies[0].Text != "thanks" {
		t.Fatalf("%+v", th.Items[2])
	}
	if _, err := ib.AddChat("843", "hi"); err != nil {
		t.Fatal(err)
	}
	_ = ib.Close()

	// A v9 binary sees user_version 10 as current and reads its own columns.
	raw, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for table, cols := range v9Tables {
		if got := tableRows(t, raw, table, cols); got != rows[table] {
			t.Fatalf("%s rows changed:\n%s\n%s", table, got, rows[table])
		}
	}
}

// tableRows is every row of cols in table, one line each, for comparing.
func tableRows(t *testing.T, db *sql.DB, table, cols string) string {
	t.Helper()
	rs, err := db.Query("SELECT " + cols + " FROM " + table + " ORDER BY rowid")
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	names, err := rs.Columns()
	if err != nil {
		t.Fatal(err)
	}
	out := ""
	for rs.Next() {
		vals := make([]sql.NullString, len(names))
		ptrs := make([]any, len(names))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for _, v := range vals {
			out += v.String + "|"
		}
		out += "\n"
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
