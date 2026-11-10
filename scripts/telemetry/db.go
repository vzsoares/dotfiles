package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const schemaVersion = "1"

// Shared with zen-inbox, which owns PRAGMA user_version: never read or set it
// here, and keep every table prefixed tool_ or telemetry_.
const schema = `
CREATE TABLE IF NOT EXISTS telemetry_meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS tool_calls (
	id              INTEGER PRIMARY KEY,
	session_id      TEXT NOT NULL,
	tool_use_id     TEXT NOT NULL,
	am_session      TEXT NOT NULL DEFAULT '',
	agent_id        TEXT NOT NULL DEFAULT '',
	agent_type      TEXT NOT NULL DEFAULT '',
	prompt_id       TEXT NOT NULL DEFAULT '',
	host            TEXT NOT NULL DEFAULT '',
	cwd             TEXT NOT NULL DEFAULT '',
	effort          TEXT NOT NULL DEFAULT '',
	permission_mode TEXT NOT NULL DEFAULT '',
	ts              TEXT NOT NULL,
	duration_ms     INTEGER,
	tool            TEXT NOT NULL,
	mcp_server      TEXT NOT NULL DEFAULT '',
	exe             TEXT NOT NULL DEFAULT '',
	exes            TEXT NOT NULL DEFAULT '',
	input           TEXT NOT NULL DEFAULT '',
	ok              INTEGER NOT NULL,
	exit_code       INTEGER,
	error           TEXT NOT NULL DEFAULT '',
	interrupted     INTEGER NOT NULL DEFAULT 0,
	timed_out       INTEGER NOT NULL DEFAULT 0,
	background      INTEGER NOT NULL DEFAULT 0,
	out_bytes       INTEGER NOT NULL DEFAULT 0,
	in_bytes        INTEGER NOT NULL DEFAULT 0,
	source          TEXT NOT NULL,
	UNIQUE (session_id, tool_use_id)
);
CREATE INDEX IF NOT EXISTS tool_calls_ts ON tool_calls (ts);
CREATE INDEX IF NOT EXISTS tool_calls_session_ts ON tool_calls (session_id, ts);
CREATE TABLE IF NOT EXISTS tool_sessions (
	session_id TEXT PRIMARY KEY,
	am_session TEXT NOT NULL DEFAULT '',
	name       TEXT NOT NULL DEFAULT '',
	grp        TEXT NOT NULL DEFAULT '',
	role       TEXT NOT NULL DEFAULT '',
	model      TEXT NOT NULL DEFAULT '',
	repo       TEXT NOT NULL DEFAULT '',
	updated    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS telemetry_weeks (
	week    TEXT PRIMARY KEY,
	done_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS telemetry_filed (
	week     TEXT NOT NULL,
	sink_key TEXT NOT NULL,
	task_id  TEXT NOT NULL,
	filed_at TEXT NOT NULL,
	PRIMARY KEY (week, sink_key)
);
INSERT OR IGNORE INTO telemetry_meta (key, value) VALUES ('schema_version', '` + schemaVersion + `');
`

var errNoVault = errors.New("vault unavailable")

// dbPath mirrors zen-inbox: $ZEN_FLOW_DB, else <vault>/config/zen-flow.db.
func dbPath(home string) (string, error) {
	if p := os.Getenv("ZEN_FLOW_DB"); p != "" {
		return expandHome(p, home), nil
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "second-brain.json"))
	if err != nil {
		return "", fmt.Errorf("%w: %v", errNoVault, err)
	}
	var brain struct {
		Vault string `json:"vault"`
	}
	if err := json.Unmarshal(raw, &brain); err != nil || brain.Vault == "" {
		return "", fmt.Errorf("%w: no vault in second-brain.json", errNoVault)
	}
	config := filepath.Join(expandHome(brain.Vault, home), "config")
	if st, err := os.Stat(config); err != nil || !st.IsDir() {
		return "", fmt.Errorf("%w: %s missing", errNoVault, config)
	}
	return filepath.Join(config, "zen-flow.db"), nil
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

func openDB(path string, busyMS int) (*sql.DB, error) {
	if st, err := os.Stat(filepath.Dir(path)); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("db dir missing: %s", filepath.Dir(path))
	}
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyMS))
	q.Add("_pragma", "journal_mode(WAL)")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func initSchema(db *sql.DB) error {
	_, err := db.Exec(schema)
	return err
}

func isNoTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}
