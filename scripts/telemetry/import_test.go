package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	sessA = "aaaaaaaa-0000-4000-8000-000000000001"
	sessB = "bbbbbbbb-0000-4000-8000-000000000002"
	sessC = "cccccccc-0000-4000-8000-000000000003"
)

// copyProjects installs testdata/projects as ~/.claude/projects.
func copyProjects(t *testing.T, home string) {
	t.Helper()
	src := filepath.Join("testdata", "projects")
	dst := projectsDir(home)
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

const amFixture = `[{"id":"am-b","name":"spec-9","group":"demo","directory":"/nonexistent/demo","agent_session_id":"` + sessB + `"}]`

func TestImport(t *testing.T) {
	home, dbp := env(t, amFixture)
	copyProjects(t, home)
	var out strings.Builder
	if err := runImport(home, 31, false, &out, now); err != nil {
		t.Fatal(err)
	}
	db := openTest(t, dbp)
	// A: Bash, Grep, failed Bash, Agent (unanswered Read dropped) + subagent Glob; B: one MCP call; C skipped.
	if n := count(t, db, ""); n != 6 {
		t.Fatalf("rows %d: %s", n, out.String())
	}
	if n := count(t, db, "session_id = ?", sessC); n != 0 {
		t.Fatal("non-agent transcript imported")
	}
	if n := count(t, db, "source = 'import'"); n != 6 {
		t.Fatal("source not import")
	}
	var dur, ok, code int
	var exe, errText string
	if err := db.QueryRow(`SELECT duration_ms, exe FROM tool_calls WHERE tool_use_id = 'toolu_a1'`).Scan(&dur, &exe); err != nil {
		t.Fatal(err)
	}
	if dur != 2500 || exe != "git" {
		t.Fatalf("parallel tool_use a1: dur=%d exe=%q", dur, exe)
	}
	if err := db.QueryRow(`SELECT duration_ms FROM tool_calls WHERE tool_use_id = 'toolu_a2'`).Scan(&dur); err != nil || dur != 1000 {
		t.Fatalf("parallel tool_use a2: dur=%d %v", dur, err)
	}
	if err := db.QueryRow(`SELECT ok, exit_code, error, exe FROM tool_calls WHERE tool_use_id = 'toolu_a3'`).Scan(&ok, &code, &errText, &exe); err != nil {
		t.Fatal(err)
	}
	if ok != 0 || code != 1 || exe != "bun" || strings.Contains(errText, "fixturevalue123") {
		t.Fatalf("error row: ok=%d code=%d exe=%q err=%q", ok, code, exe, errText)
	}
	var agentID, agentType string
	if err := db.QueryRow(`SELECT agent_id, agent_type FROM tool_calls WHERE tool_use_id = 'toolu_s1'`).Scan(&agentID, &agentType); err != nil {
		t.Fatal(err)
	}
	if agentID != "s1" || agentType != "Explore" {
		t.Fatalf("subagent: %q %q", agentID, agentType)
	}
	var server, input, am string
	if err := db.QueryRow(`SELECT mcp_server, input, am_session FROM tool_calls WHERE tool_use_id = 'toolu_b1'`).Scan(&server, &input, &am); err != nil {
		t.Fatal(err)
	}
	if server != "demo_srv" || input != "id,query" || am != "am-b" {
		t.Fatalf("mcp row: %q %q %q", server, input, am)
	}
	if text := allText(t, db); strings.Contains(text, "private") {
		t.Fatal("MCP input values or result text stored")
	}
	var name, role, model, repo string
	if err := db.QueryRow(`SELECT name, role, model, repo FROM tool_sessions WHERE session_id = ?`, sessB).Scan(&name, &role, &model, &repo); err != nil {
		t.Fatal(err)
	}
	if name != "spec-9" || role != "spec-writer" || model != "claude-test-3" || repo != "demo" {
		t.Fatalf("session B meta: %q %q %q %q", name, role, model, repo)
	}
	if err := db.QueryRow(`SELECT role, model FROM tool_sessions WHERE session_id = ?`, sessA).Scan(&role, &model); err != nil {
		t.Fatal(err)
	}
	if role != "agent" || model != "claude-test-2" {
		t.Fatalf("session A meta: %q %q", role, model)
	}

	out.Reset()
	if err := runImport(home, 31, false, &out, now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), ", 0 added") {
		t.Fatalf("second run added rows: %s", out.String())
	}
	if n := count(t, db, ""); n != 6 {
		t.Fatalf("rows after rerun %d", n)
	}
}

func TestImportDryRunWritesNothing(t *testing.T) {
	home, dbp := env(t, amFixture)
	copyProjects(t, home)
	var out strings.Builder
	if err := runImport(home, 31, true, &out, now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "6 calls parsed (dry run") {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(dbp); !os.IsNotExist(err) {
		t.Fatal("dry run created the db")
	}
}

func TestHookBeatsImport(t *testing.T) {
	home, dbp := env(t, amFixture)
	copyProjects(t, home)
	// Hook first: the import must not overwrite it.
	hook(t, payload(t, map[string]any{"session_id": sessA, "tool_use_id": "toolu_a1", "duration_ms": 7}))
	if err := runImport(home, 31, false, io.Discard, now); err != nil {
		t.Fatal(err)
	}
	db := openTest(t, dbp)
	if n := count(t, db, "tool_use_id = 'toolu_a1' AND source = 'hook' AND duration_ms = 7"); n != 1 {
		t.Fatal("import overwrote the hook row")
	}
	// Import first: a later hook row replaces it.
	hook(t, payload(t, map[string]any{"session_id": sessA, "tool_use_id": "toolu_a2", "tool_name": "Grep",
		"tool_input": map[string]any{"pattern": "TODO"}, "duration_ms": 5}))
	if n := count(t, db, "tool_use_id = 'toolu_a2' AND source = 'hook' AND duration_ms = 5"); n != 1 {
		t.Fatal("hook row did not replace the import row")
	}
	if n := count(t, db, ""); n != 6 {
		t.Fatalf("rows %d", n)
	}
}

func TestImportSkipsOldTranscripts(t *testing.T) {
	home, dbp := env(t, amFixture)
	copyProjects(t, home)
	var out strings.Builder
	if err := runImport(home, 31, false, &out, now.AddDate(0, 3, 0)); err != nil {
		t.Fatal(err)
	}
	if n := count(t, openTest(t, dbp), ""); n != 0 {
		t.Fatalf("imported %d rows from transcripts older than the window", n)
	}
}
