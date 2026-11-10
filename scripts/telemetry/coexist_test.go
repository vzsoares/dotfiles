package main

import (
	"strings"
	"testing"
)

// zen-inbox owns user_version and creates its tables with a plain CREATE
// TABLE; telemetry must leave both alone.
func TestCoexistWithInboxSchema(t *testing.T) {
	_, dbp := env(t, "")
	db := openTest(t, dbp)
	for _, stmt := range []string{
		"CREATE TABLE decisions (id TEXT PRIMARY KEY, session_id TEXT, status TEXT)",
		"CREATE TABLE decision_options (decision_id TEXT, letter TEXT)",
		"CREATE TABLE decision_briefs (session_id TEXT, task TEXT)",
		"INSERT INTO decisions VALUES ('1.1', 's', 'open')",
		"PRAGMA user_version = 1",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	before := tables(t, dbp)
	hook(t, payload(t, nil))
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 1 {
		t.Fatalf("user_version %d %v", v, err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM decisions").Scan(&n); err != nil || n != 1 {
		t.Fatalf("inbox rows %d %v", n, err)
	}
	for name := range tables(t, dbp) {
		if before[name] {
			continue
		}
		if !strings.HasPrefix(name, "tool_") && !strings.HasPrefix(name, "telemetry_") {
			t.Errorf("telemetry added %q", name)
		}
	}
	if count(t, db, "") != 1 {
		t.Fatal("hook row missing")
	}
}

func tables(t *testing.T, dbp string) map[string]bool {
	t.Helper()
	db := openTest(t, dbp)
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type IN ('table', 'index') AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out[name] = true
	}
	return out
}
