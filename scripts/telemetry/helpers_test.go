package main

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "zen-telemetry-bin")
	if err != nil {
		panic(err)
	}
	binPath = filepath.Join(dir, "zen-telemetry")
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// env points HOME, ZEN_FLOW_DB and PATH (with a fake agent-manager) at temp
// dirs and returns the db path.
func env(t *testing.T, amJSON string) (home, db string) {
	t.Helper()
	home = t.TempDir()
	db = filepath.Join(t.TempDir(), "zen-flow.db")
	t.Setenv("HOME", home)
	t.Setenv("ZEN_FLOW_DB", db)
	t.Setenv("AGENT_MANAGER_SESSION_ID", "am000001")
	bin := t.TempDir()
	if amJSON == "" {
		amJSON = "[]"
	}
	if err := os.WriteFile(filepath.Join(bin, "sessions.json"), []byte(amJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n[ \"$1\" = sessions ] && exec cat \"" + filepath.Join(bin, "sessions.json") + "\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "agent-manager"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return home, db
}

func openTest(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := openDB(path, 5000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func count(t *testing.T, db *sql.DB, where string, args ...any) int {
	t.Helper()
	var n int
	q := "SELECT COUNT(*) FROM tool_calls"
	if where != "" {
		q += " WHERE " + where
	}
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		if isNoTable(err) {
			return 0
		}
		t.Fatal(err)
	}
	return n
}

// allText concatenates every stored column of every telemetry row.
func allText(t *testing.T, db *sql.DB) string {
	t.Helper()
	var b strings.Builder
	for _, table := range []string{"tool_calls", "tool_sessions"} {
		rows, err := db.Query("SELECT * FROM " + table)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]sql.NullString, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for _, v := range vals {
				b.WriteString(v.String + "\x00")
			}
		}
		rows.Close()
	}
	return b.String()
}
