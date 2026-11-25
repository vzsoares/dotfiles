// Package amtest builds agent-manager fixtures for tests: a state.db with
// agent-manager's own sessions and session_inbox DDL, and Claude transcripts.
// Every body in them is synthetic.
package amtest

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// DDL is agent-manager's sessions and session_inbox schema.
const DDL = `
CREATE TABLE sessions (
	id             TEXT PRIMARY KEY,
	name           TEXT NOT NULL,
	tool           TEXT NOT NULL,
	cwd            TEXT NOT NULL,
	group_name     TEXT NOT NULL,
	status         TEXT NOT NULL,
	archived       INTEGER NOT NULL DEFAULT 0,
	created_at     INTEGER NOT NULL,
	last_status_at INTEGER NOT NULL,
	agent_session_id TEXT NOT NULL DEFAULT '',
	pending_inputs TEXT NOT NULL DEFAULT '[]',
	pending_claimed INTEGER NOT NULL DEFAULT 0,
	launch_prompt  TEXT NOT NULL DEFAULT ''
, sort_order INTEGER NOT NULL DEFAULT 0, acked INTEGER NOT NULL DEFAULT 0, snapshot TEXT NOT NULL DEFAULT '', worktree_repo TEXT NOT NULL DEFAULT '', worktree_branch TEXT NOT NULL DEFAULT '', agent_launched_at INTEGER NOT NULL DEFAULT 0, retired_agent_session_id TEXT NOT NULL DEFAULT '', parent_id TEXT NOT NULL DEFAULT '', tmux_socket TEXT NOT NULL DEFAULT '', last_prompt TEXT NOT NULL DEFAULT '', relaunch_snapshot TEXT NOT NULL DEFAULT '', model_provider TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '', effort TEXT NOT NULL DEFAULT '', profile TEXT NOT NULL DEFAULT '', after_turn TEXT NOT NULL DEFAULT '', after_turn_at INTEGER NOT NULL DEFAULT 0, opened_without_session INTEGER NOT NULL DEFAULT 0, environment TEXT NOT NULL DEFAULT '', released_agent_session_id TEXT NOT NULL DEFAULT '', released_launch INTEGER NOT NULL DEFAULT 0, spawned_by TEXT NOT NULL DEFAULT '');
CREATE TABLE session_inbox (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id   TEXT    NOT NULL,
			sender_id    TEXT    NOT NULL,
			sender_name  TEXT    NOT NULL,
			body         TEXT    NOT NULL,
			fingerprint  TEXT    NOT NULL,
			sent_at      INTEGER NOT NULL,
			claimed_at   INTEGER NOT NULL DEFAULT 0,
			delivered_at INTEGER NOT NULL DEFAULT 0,
			read_at      INTEGER NOT NULL DEFAULT 0
		, dropped_at INTEGER NOT NULL DEFAULT 0);
CREATE INDEX session_inbox_queue ON session_inbox (session_id, delivered_at, id);
`

// Session is a sessions row.
type Session struct {
	ID, Name, Tool, Cwd, Status string
	Archived                    bool
	Agent, Retired, Released    string
}

// Row is a session_inbox row; a zero time stores 0.
type Row struct {
	ID                       int64
	To, From, FromName       string
	Body                     string
	Sent, Delivered, Dropped time.Time
}

func ns(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

// State writes a state.db at path holding sessions and rows.
func State(t testing.TB, path string, sessions []Session, rows []Row) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(DDL); err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if s.Tool == "" {
			s.Tool = "claude"
		}
		if s.Status == "" {
			s.Status = "idle"
		}
		archived := 0
		if s.Archived {
			archived = 1
		}
		if _, err := db.Exec("INSERT INTO sessions (id, name, tool, cwd, group_name, status, archived, created_at, last_status_at,"+
			" agent_session_id, retired_agent_session_id, released_agent_session_id) VALUES (?, ?, ?, ?, 'g', ?, ?, 0, 0, ?, ?, ?)",
			s.ID, s.Name, s.Tool, s.Cwd, s.Status, archived, s.Agent, s.Retired, s.Released); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range rows {
		if _, err := db.Exec("INSERT INTO session_inbox (id, session_id, sender_id, sender_name, body, fingerprint, sent_at,"+
			" delivered_at, dropped_at) VALUES (?, ?, ?, ?, ?, 'f', ?, ?, ?)",
			r.ID, r.To, r.From, r.FromName, r.Body, ns(r.Sent), ns(r.Delivered), ns(r.Dropped)); err != nil {
			t.Fatal(err)
		}
	}
}

// Delivered is the user turn agent-manager pastes for a delivered message.
func Delivered(from, body string) string {
	return "<pasted_content>\n[agent-manager] Message from " + from + ":\n\n" + body + "\n</pasted_content>"
}

// Transcript appends one user turn per text to projects/<dir>/<agentID>.jsonl,
// and stamps the file with at.
func Transcript(t testing.TB, projects, dir, agentID string, at time.Time, texts ...string) string {
	t.Helper()
	path := filepath.Join(projects, dir, agentID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}` + "\n")
	for _, text := range texts {
		line, err := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteString("\n")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(b.String()); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	return path
}
