package am_test

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/am/amtest"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T) string {
	t.Helper()
	// A space in the path, like macOS's Application Support.
	path := filepath.Join(t.TempDir(), "App Support", "state.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	amtest.State(t, path, []amtest.Session{{ID: "s1", Name: "chief", Cwd: "/w/a", Agent: "a1", Retired: "a0"}},
		[]amtest.Row{{ID: 7, To: "s1", From: "s2", FromName: "worker", Body: "synthetic body", Sent: t0, Delivered: t0.Add(time.Second)}})
	return path
}

func TestStateReadsRowsAndSessions(t *testing.T) {
	st, err := am.OpenState(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	r, ok, err := st.Row(7)
	if err != nil || !ok || r.Body != "synthetic body" || !r.SentAt.Equal(t0) || r.DroppedAt != (time.Time{}) {
		t.Fatalf("%+v %v %v", r, ok, err)
	}
	if _, ok, _ := st.Row(8); ok {
		t.Fatal("row 8 found")
	}
	ss, err := st.Sessions()
	if err != nil || ss["s1"].Name != "chief" || strings.Join(ss["s1"].AgentIDs, ",") != "a1,a0" {
		t.Fatalf("%+v %v", ss, err)
	}
}

func TestStateIsReadOnly(t *testing.T) {
	path := fixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := am.OpenState(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Rows(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Sessions(); err != nil {
		t.Fatal(err)
	}
	if err := am.TryWrite(st); err == nil {
		t.Fatal("a write through the reader succeeded")
	}
	_ = st.Close()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("state.db bytes changed")
	}
}

func TestStateUnavailable(t *testing.T) {
	dir := t.TempDir()
	noTable := filepath.Join(dir, "no-table.db")
	noColumn := filepath.Join(dir, "no-column.db")
	for path, ddl := range map[string]string{
		noTable:  "CREATE TABLE sessions (id TEXT)",
		noColumn: strings.Replace(amtest.DDL, ", dropped_at INTEGER NOT NULL DEFAULT 0", "", 1),
	} {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
	}
	for path, want := range map[string]string{
		filepath.Join(dir, "missing.db"): "no agent-manager state.db at",
		noTable:                          "state.db has no session_inbox table",
		noColumn:                         "state.db session_inbox has no dropped_at column",
	} {
		_, err := am.OpenState(path)
		var u *am.Unavailable
		if !errors.As(err, &u) || !strings.Contains(u.Error(), want) {
			t.Fatalf("%s: %v, want %q", filepath.Base(path), err, want)
		}
	}
}
