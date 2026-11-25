package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const explorerFixture = `[{"id":"am-b","name":"ask-1","group":"demo","environment":"role-explorer","directory":"/nonexistent/demo","agent_session_id":"` + sessB + `"}]`

func usageRows(t *testing.T, dbp string) int {
	t.Helper()
	var n int
	if err := openTest(t, dbp).QueryRow(`SELECT COUNT(*) FROM tool_usage`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestImportUsage(t *testing.T) {
	home, dbp := env(t, explorerFixture)
	copyProjects(t, home)
	var out strings.Builder
	if err := runImport(home, 31, false, &out, now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "4 usage rows added") {
		t.Fatalf("import output: %s", out.String())
	}
	db := openTest(t, dbp)
	type got struct {
		agentType                       string
		msgs                            int
		in, outTok, cacheRead, cacheWrt int64
	}
	read := func(session, agent, model string) got {
		var g got
		err := db.QueryRow(`SELECT agent_type, messages, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens
			FROM tool_usage WHERE session_id = ? AND agent_id = ? AND model = ?`, session, agent, model).
			Scan(&g.agentType, &g.msgs, &g.in, &g.outTok, &g.cacheRead, &g.cacheWrt)
		if err != nil {
			t.Fatalf("%s/%s/%s: %v", session, agent, model, err)
		}
		return g
	}
	// Two transcript lines share msg_a1: the later usage counts once.
	if g := read(sessA, "", "claude-test-1"); g != (got{"", 1, 10, 7, 100, 20}) {
		t.Fatalf("main usage %+v", g)
	}
	if g := read(sessA, "s1", "claude-test-haiku"); g != (got{"Explore", 1, 30, 4, 300, 10}) {
		t.Fatalf("subagent usage %+v", g)
	}
	if n := usageRows(t, dbp); n != 4 {
		t.Fatalf("rows %d", n)
	}

	out.Reset()
	if err := runImport(home, 31, false, &out, now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "0 usage rows added") || usageRows(t, dbp) != 4 {
		t.Fatalf("rerun: %s rows=%d", out.String(), usageRows(t, dbp))
	}
}

func TestImportUsageGrowsWithTheTranscript(t *testing.T) {
	home, dbp := env(t, explorerFixture)
	copyProjects(t, home)
	if err := runImport(home, 31, false, io.Discard, now); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectsDir(home), "-tmp-demo", sessB+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","sessionId":"` + sessB + `","timestamp":"2026-11-09T11:00:03.000Z","message":{"id":"msg_b2","model":"claude-test-3","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":5,"output_tokens":2}}}` + "\n"
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := runImport(home, 31, false, io.Discard, now); err != nil {
		t.Fatal(err)
	}
	var msgs, in int
	if err := openTest(t, dbp).QueryRow(`SELECT messages, input_tokens FROM tool_usage WHERE session_id = ?`, sessB).Scan(&msgs, &in); err != nil {
		t.Fatal(err)
	}
	if msgs != 2 || in != 6 || usageRows(t, dbp) != 4 {
		t.Fatalf("messages=%d input=%d rows=%d", msgs, in, usageRows(t, dbp))
	}
}

func TestImportDryRunWritesNoUsage(t *testing.T) {
	home, dbp := env(t, explorerFixture)
	copyProjects(t, home)
	if err := runImport(home, 31, true, io.Discard, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dbp); !os.IsNotExist(err) {
		t.Fatal("dry run created the db")
	}
}

func TestUsageTallySkipsSyntheticAndCountsIdless(t *testing.T) {
	var u usageTally
	u.add([]byte(`{"type":"assistant","timestamp":"2026-11-09T10:00:00Z","message":{"model":"<synthetic>","usage":{"input_tokens":9}}}`))
	u.add([]byte(`{"type":"user","message":{"usage":{"input_tokens":9}}}`))
	u.add([]byte(`{"type":"assistant","timestamp":"2026-11-09T23:59:59Z","message":{"model":"m","usage":{"input_tokens":1}}}`))
	u.add([]byte(`{"type":"assistant","timestamp":"2026-11-09T23:59:59Z","message":{"model":"m","usage":{"input_tokens":2}}}`))
	got := u.rows()
	if len(got) != 1 {
		t.Fatalf("rows %+v", got)
	}
	r := got[usageKey{"m", "2026-11-09"}]
	if r.messages != 2 || r.Input != 3 {
		t.Fatalf("row %+v", r)
	}
}

func TestRoleOfExplorerEnvironment(t *testing.T) {
	if got := roleOf("ask-1", "/nonexistent/x", "role-explorer"); got != "explorer" {
		t.Fatalf("role %q", got)
	}
	if got := roleOf("spec-9", "/nonexistent/x", "role-spec-writer"); got != "spec-writer" {
		t.Fatalf("role %q", got)
	}
}

func byModelJSON(t *testing.T, home string, at int) ModelReport {
	t.Helper()
	var out strings.Builder
	days := 7
	when := now.AddDate(0, 0, at)
	if err := runByModel(home, days, true, &out, when); err != nil {
		t.Fatal(err)
	}
	var rep ModelReport
	if err := json.Unmarshal([]byte(out.String()), &rep); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	return rep
}

func TestReportByModel(t *testing.T) {
	home, _ := env(t, explorerFixture)
	copyProjects(t, home)
	if err := runImport(home, 31, false, io.Discard, now); err != nil {
		t.Fatal(err)
	}
	rep := byModelJSON(t, home, 0)
	if rep.From != "2026-11-04" || rep.To != "2026-11-10" || len(rep.Previous) != 0 {
		t.Fatalf("window %+v", rep)
	}
	find := func(rows []ModelStat, role, agent, model string) ModelStat {
		for _, m := range rows {
			if m.Role == role && m.Agent == agent && m.Model == model {
				return m
			}
		}
		t.Fatalf("no %s/%s/%s in %+v", role, agent, model, rep.Current)
		return ModelStat{}
	}
	h := find(rep.Current, "agent", "Explore", "claude-test-haiku")
	if h.Runs != 1 || h.Input != 30 || h.Output != 4 || h.CacheRead != 300 || h.CacheWrite != 10 {
		t.Fatalf("haiku row %+v", h)
	}
	if e := find(rep.Current, "explorer", "main", "claude-test-3"); e.Runs != 1 || e.Input != 1 {
		t.Fatalf("explorer row %+v", e)
	}
	if m := find(rep.Current, "agent", "main", "claude-test-1"); m.Output != 7 {
		t.Fatalf("main row %+v", m)
	}
	// One week later the same rows fall into the previous window.
	later := byModelJSON(t, home, 7)
	if len(later.Current) != 0 || len(later.Previous) != len(rep.Current) {
		t.Fatalf("later window: %d current, %d previous", len(later.Current), len(later.Previous))
	}
}

func TestReportByModelMarkdownAndFlag(t *testing.T) {
	home, _ := env(t, explorerFixture)
	copyProjects(t, home)
	if err := runImport(home, 31, false, io.Discard, now); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	if code := run([]string{"report", "--by-model", "--days", "7"}, strings.NewReader(""), &out, &errb, now); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, want := range []string{"# Tokens by model", "| agent | Explore | `claude-test-haiku` | 1 | 30 | 4 | 300 | 10 |", "| explorer | main |", "Previous: 2026-10-28 to 2026-11-03"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out.String())
		}
	}
}
