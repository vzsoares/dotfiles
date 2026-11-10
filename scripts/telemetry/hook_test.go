package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func payload(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	p := map[string]any{
		"session_id":      "sess-1",
		"transcript_path": "/tmp/x.jsonl",
		"cwd":             "/tmp/demo",
		"permission_mode": "auto",
		"hook_event_name": "PostToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "cd /tmp/demo && git status"},
		"tool_response":   map[string]any{"stdout": "clean", "stderr": "", "interrupted": false},
		"tool_use_id":     "toolu_1",
		"duration_ms":     42,
	}
	for k, v := range fields {
		if v == nil {
			delete(p, k)
		} else {
			p[k] = v
		}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func hook(t *testing.T, raw []byte) {
	t.Helper()
	runHook(bytes.NewReader(raw), os.Getenv("HOME"), now)
}

func TestHookEnvUnset(t *testing.T) {
	_, dbp := env(t, "")
	t.Setenv("AGENT_MANAGER_SESSION_ID", "")
	cmd := exec.Command(binPath, "hook")
	cmd.Stdin = bytes.NewReader(payload(t, nil))
	out, err := cmd.CombinedOutput()
	if err != nil || len(out) != 0 {
		t.Fatalf("want exit 0 and no output, got %v %q", err, out)
	}
	if _, err := os.Stat(dbp); !os.IsNotExist(err) {
		t.Fatalf("db created with env unset: %v", err)
	}
}

func TestHookBashRow(t *testing.T) {
	_, dbp := env(t, "")
	hook(t, payload(t, map[string]any{"agent_id": nil}))
	db := openTest(t, dbp)
	var exe, exes, input, source, am string
	var dur, out, in, ok int
	err := db.QueryRow(`SELECT exe, exes, input, duration_ms, out_bytes, in_bytes, ok, source, am_session
		FROM tool_calls`).Scan(&exe, &exes, &input, &dur, &out, &in, &ok, &source, &am)
	if err != nil {
		t.Fatal(err)
	}
	if exe != "git" || exes != "git" || dur != 42 || out == 0 || in == 0 || ok != 1 || source != "hook" || am != "am000001" {
		t.Fatalf("got exe=%q exes=%q dur=%d out=%d in=%d ok=%d source=%q am=%q", exe, exes, dur, out, in, ok, source, am)
	}
	if input != "cd /tmp/demo && git status" {
		t.Fatalf("input %q", input)
	}
}

func TestHookFailureExitCode(t *testing.T) {
	_, dbp := env(t, "")
	hook(t, payload(t, map[string]any{
		"hook_event_name": "PostToolUseFailure",
		"tool_response":   nil,
		"error":           "Exit code 2\ngrep: nope: No such file",
	}))
	db := openTest(t, dbp)
	var ok, code, timedOut int
	var errText string
	if err := db.QueryRow(`SELECT ok, exit_code, error, timed_out FROM tool_calls`).Scan(&ok, &code, &errText, &timedOut); err != nil {
		t.Fatal(err)
	}
	if ok != 0 || code != 2 || !strings.HasPrefix(errText, "Exit code 2") || timedOut != 0 {
		t.Fatalf("ok=%d code=%d err=%q timed_out=%d", ok, code, errText, timedOut)
	}
}

func TestHookTimeoutAndBackground(t *testing.T) {
	_, dbp := env(t, "")
	hook(t, payload(t, map[string]any{
		"hook_event_name": "PostToolUseFailure",
		"tool_use_id":     "toolu_to",
		"error":           "Command timed out after 2m",
	}))
	hook(t, payload(t, map[string]any{
		"tool_use_id": "toolu_bg",
		"tool_input":  map[string]any{"command": "sleep 100", "run_in_background": true},
	}))
	db := openTest(t, dbp)
	if n := count(t, db, "tool_use_id = 'toolu_to' AND timed_out = 1"); n != 1 {
		t.Fatalf("timed_out rows %d", n)
	}
	if n := count(t, db, "tool_use_id = 'toolu_bg' AND background = 1"); n != 1 {
		t.Fatalf("background rows %d", n)
	}
}

func TestHookMCP(t *testing.T) {
	_, dbp := env(t, "")
	hook(t, payload(t, map[string]any{
		"tool_name":  "mcp__claude_ai_Notion__notion-fetch",
		"tool_input": map[string]any{"id": "private-page", "query": "private words"},
	}))
	db := openTest(t, dbp)
	var server, input, exe string
	if err := db.QueryRow(`SELECT mcp_server, input, exe FROM tool_calls`).Scan(&server, &input, &exe); err != nil {
		t.Fatal(err)
	}
	if server != "claude_ai_Notion" || input != "id,query" || exe != "" {
		t.Fatalf("server=%q input=%q exe=%q", server, input, exe)
	}
}

func TestHookSubagent(t *testing.T) {
	_, dbp := env(t, "")
	hook(t, payload(t, map[string]any{"agent_id": "agent-7", "agent_type": "Explore"}))
	db := openTest(t, dbp)
	if n := count(t, db, "agent_id = 'agent-7' AND agent_type = 'Explore'"); n != 1 {
		t.Fatalf("subagent rows %d", n)
	}
}

func TestHookDuplicateToolUseID(t *testing.T) {
	_, dbp := env(t, "")
	hook(t, payload(t, nil))
	hook(t, payload(t, map[string]any{"duration_ms": 99}))
	db := openTest(t, dbp)
	if n := count(t, db, ""); n != 1 {
		t.Fatalf("rows %d", n)
	}
	if n := count(t, db, "duration_ms = 99"); n != 1 {
		t.Fatal("second hook row did not replace the first")
	}
}

func TestHookLockedDB(t *testing.T) {
	_, dbp := env(t, "")
	hook(t, payload(t, map[string]any{"tool_use_id": "toolu_first"}))
	holder := openTest(t, dbp)
	conn, err := holder.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binPath, "hook")
	cmd.Stdin = bytes.NewReader(payload(t, map[string]any{"tool_use_id": "toolu_locked"}))
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	if err != nil || len(out) != 0 {
		t.Fatalf("want exit 0 and no output, got %v %q", err, out)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("hook took %v under lock", elapsed)
	}
	if _, err := conn.ExecContext(context.Background(), "COMMIT"); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if n := count(t, holder, "tool_use_id = 'toolu_locked'"); n != 0 {
		t.Fatal("row written under lock")
	}
}

func TestHookMissingVaultAndMalformed(t *testing.T) {
	home, dbp := env(t, "")
	t.Setenv("ZEN_FLOW_DB", "")
	for _, raw := range [][]byte{payload(t, nil), []byte("{not json"), []byte("")} {
		cmd := exec.Command(binPath, "hook")
		cmd.Stdin = bytes.NewReader(raw)
		if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
			t.Fatalf("missing vault: %v %q", err, out)
		}
	}
	t.Setenv("ZEN_FLOW_DB", dbp)
	for _, raw := range [][]byte{[]byte("{not json"), []byte(`{"tool_name":"Bash"}`)} {
		cmd := exec.Command(binPath, "hook")
		cmd.Stdin = bytes.NewReader(raw)
		if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
			t.Fatalf("malformed: %v %q", err, out)
		}
	}
	if _, err := os.Stat(dbp); !os.IsNotExist(err) {
		t.Fatal("malformed payload created the db")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Fatal("hook created files under HOME")
	}
}
