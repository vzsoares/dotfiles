package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files")

type fx struct {
	session, agent, tool, cmd string
	ago                       time.Duration
	durMS                     int64
	ok, bg, timedOut          bool
	out                       int
}

// fixture writes calls straight into a fresh db, plus session metadata.
func fixture(t *testing.T, calls []fx) *sql.DB {
	t.Helper()
	_, dbp := env(t, "")
	db := openTest(t, dbp)
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	for i, f := range calls {
		c := call{SessionID: f.session, ToolUseID: "toolu_" + string(rune('A'+i/26)) + string(rune('a'+i%26)),
			AgentID: f.agent, TS: now.Add(-f.ago), Tool: f.tool, OutBytes: f.out, Source: "hook",
			DurationMS: sql.NullInt64{Int64: f.durMS, Valid: true}}
		in, _ := json.Marshal(map[string]any{"command": f.cmd, "run_in_background": f.bg})
		errText := ""
		if !f.ok {
			errText = "Exit code 1"
			if f.timedOut {
				errText = "Command timed out"
			}
		}
		c.describe(in, !f.ok, errText)
		if _, err := db.Exec(hookInsertSQL, c.args()...); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []sessionMeta{
		{SessionID: "s1", Name: "worker-a", Role: "worker", Repo: "demo"},
		{SessionID: "s2", Name: "chief", Role: "chief", Repo: "code"},
	} {
		if err := upsertSession(db, s, now); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

var reportCalls = []fx{
	// s1: bun fails twice then passes; the 2nd and 3rd are retries.
	{session: "s1", tool: "Bash", cmd: "bun test", ago: 2*time.Hour + 12*time.Minute, durMS: 40_000, out: 100},
	{session: "s1", tool: "Bash", cmd: "bun test", ago: 2*time.Hour + 10*time.Minute, durMS: 45_000, out: 300},
	{session: "s1", tool: "Bash", cmd: "bun test", ago: 2*time.Hour + 8*time.Minute, durMS: 20_000, ok: true, out: 200},
	// a failure more than 5 min before the next call is not a retry trigger.
	{session: "s1", tool: "Bash", cmd: "git push", ago: 2 * time.Hour, durMS: 1_000},
	{session: "s1", tool: "Bash", cmd: "git status", ago: time.Hour, durMS: 500, ok: true},
	{session: "s1", tool: "Bash", cmd: "curl https://example.com", ago: 50 * time.Minute, durMS: 60_000, timedOut: true},
	{session: "s1", tool: "Grep", ago: 40 * time.Minute, durMS: 300, ok: true, out: 5000},
	{session: "s2", tool: "mcp__claude_ai_Notion__notion-fetch", ago: 30 * time.Minute, durMS: 3_000, ok: true, out: 9000},
	{session: "s2", tool: "Bash", cmd: "zen-board list", ago: 29 * time.Minute, durMS: 800, ok: true, out: 400},
	{session: "s2", tool: "Agent", ago: 20 * time.Minute, durMS: 600_000, ok: true},
	{session: "s2", tool: "AskUserQuestion", ago: 15 * time.Minute, durMS: 300_000, ok: true},
	{session: "s2", tool: "Bash", cmd: "zen-await w1", ago: 10 * time.Minute, durMS: 900_000, ok: true, bg: true},
	{session: "s2", tool: "Bash", cmd: "agent-manager wait w1", ago: 9 * time.Minute, durMS: 31_000, ok: true},
	{session: "s2", tool: "Bash", cmd: "make", ago: 8 * time.Minute, durMS: 2_000, ok: true},
	// outside the 7-day window
	{session: "s1", tool: "Bash", cmd: "bun test", ago: 8 * 24 * time.Hour, durMS: 999_000, ok: true},
}

func report(t *testing.T, o reportOpts) (Report, int) {
	t.Helper()
	o.JSON = true
	var out strings.Builder
	code, err := runReport(os.Getenv("HOME"), o, &out, now)
	if err != nil {
		t.Fatal(err)
	}
	var r Report
	if code == 0 {
		if err := json.Unmarshal([]byte(out.String()), &r); err != nil {
			t.Fatal(err)
		}
	} else if out.Len() != 0 {
		t.Fatalf("exit %d printed %q", code, out.String())
	}
	return r, code
}

func defaults() reportOpts { return reportOpts{Days: 7, Slow: 30 * time.Second} }

func toolStat(t *testing.T, r Report, key string) ToolStat {
	t.Helper()
	for _, s := range r.Tools {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("no tool %q", key)
	return ToolStat{}
}

func TestReportSlowAndTotals(t *testing.T) {
	fixture(t, reportCalls)
	r, _ := report(t, defaults())
	// over 30s: bun 40s+45s, curl 60s, Agent 600s, AskUserQuestion 300s, zen-await 900s, agent-manager wait 31s
	if r.Slow.Count != 7 || r.Slow.TotalMS != 40_000+45_000+60_000+600_000+300_000+900_000+31_000 {
		t.Fatalf("slow %+v", r.Slow)
	}
	for _, g := range r.Slow.Groups {
		if g.Example.DurationMS <= 30_000 {
			t.Fatalf("slow group %s example %d", g.Key, g.Example.DurationMS)
		}
	}
	bun := toolStat(t, r, "bash:bun")
	if bun.Calls != 3 || bun.TotalMS != 105_000 || bun.MedianMS != 40_000 || bun.P90MS != 45_000 ||
		bun.FailPct != 66.7 || bun.RetryPct != 66.7 || bun.MedianOutBytes != 200 || bun.TotalOutBytes != 600 {
		t.Fatalf("bun %+v", bun)
	}
	git := toolStat(t, r, "bash:git")
	if git.RetryPct != 0 {
		t.Fatalf("git retry %+v", git)
	}
	if curl := toolStat(t, r, "bash:curl"); curl.Timeouts != 1 {
		t.Fatalf("curl %+v", curl)
	}
	if r.Tools[0].Key != "bash:zen-await" {
		t.Fatalf("tools not sorted by total: %s", r.Tools[0].Key)
	}
}

func TestReportJobsAndSinks(t *testing.T) {
	fixture(t, reportCalls)
	r, _ := report(t, defaults())
	jobs := map[string][]string{}
	for _, j := range r.Jobs {
		for _, s := range j.Tools {
			jobs[j.Job] = append(jobs[j.Job], s.Key)
		}
	}
	want := map[string][]string{
		"board":         {"mcp__claude_ai_Notion__notion-fetch", "bash:zen-board"},
		"search":        {"Grep"},
		"web":           {"bash:curl"},
		"waiting":       {"bash:zen-await", "bash:agent-manager wait"},
		"agent control": {"bash:agent-manager wait"},
	}
	if !reflect.DeepEqual(jobs, want) {
		t.Fatalf("jobs %v", jobs)
	}
	var keys []string
	for _, s := range r.Sinks {
		keys = append(keys, s.Key)
	}
	wantSinks := []string{"bash:bun", "bash:curl", "bash:agent-manager wait", "mcp__claude_ai_Notion__notion-fetch", "bash:make", "bash:git"}
	if !reflect.DeepEqual(keys, wantSinks) {
		t.Fatalf("sinks %v", keys)
	}
	if len(r.Background) != 1 || r.Background[0].Key != "bash:zen-await" {
		t.Fatalf("background %v", r.Background)
	}
	if !strings.Contains(r.Sinks[3].Markdown, "Same job (board)") || !strings.Contains(r.Sinks[3].Markdown, "bash:zen-board") {
		t.Fatalf("sink markdown lacks its job comparison:\n%s", r.Sinks[3].Markdown)
	}
	var unmapped []string
	for _, u := range r.Unmapped {
		unmapped = append(unmapped, u.Key)
	}
	if !reflect.DeepEqual(unmapped, []string{"bash:bun", "bash:make", "bash:git"}) {
		t.Fatalf("unmapped %v", unmapped)
	}
}

func TestReportDueAndFiled(t *testing.T) {
	fixture(t, reportCalls)
	week := isoWeek(now)
	o := defaults()
	o.Due = true
	if _, code := report(t, o); code != 0 {
		t.Fatalf("due before mark-done: %d", code)
	}
	if code := run([]string{"filed", week, "bash:bun", "601"}, nil, os.Stdout, os.Stderr, now); code != 0 {
		t.Fatal(code)
	}
	if code := run([]string{"mark-done", week}, nil, os.Stdout, os.Stderr, now); code != 0 {
		t.Fatal(code)
	}
	if _, code := report(t, o); code != 10 {
		t.Fatalf("due after mark-done: %d", code)
	}
	r, _ := report(t, defaults())
	if !reflect.DeepEqual(r.Sinks[0].Filed, []Filed{{Week: week, TaskID: "601"}}) {
		t.Fatalf("filed %v", r.Sinks[0].Filed)
	}
	if code := run([]string{"mark-done", "W46"}, nil, io.Discard, io.Discard, now); code != 2 {
		t.Fatalf("bad week accepted: %d", code)
	}
}

func TestReportRetention(t *testing.T) {
	db := fixture(t, append(reportCalls, fx{session: "s1", tool: "Read", ago: 400 * 24 * time.Hour, durMS: 1, ok: true}))
	report(t, defaults())
	if n := count(t, db, "tool = 'Read'"); n != 0 {
		t.Fatal("rows older than 365 days not pruned")
	}
}

func TestReportJSONShape(t *testing.T) {
	fixture(t, reportCalls)
	var out strings.Builder
	if _, err := runReport(os.Getenv("HOME"), reportOpts{Days: 7, Slow: 30 * time.Second, JSON: true}, &out, now); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "report.golden.json")
	if *update {
		if err := os.WriteFile(golden, []byte(out.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != string(want) {
		t.Fatalf("JSON shape changed; rerun with -update if intended:\n%s", out.String())
	}
}

func TestReportMarkdown(t *testing.T) {
	fixture(t, reportCalls)
	var out strings.Builder
	if _, err := runReport(os.Getenv("HOME"), defaults(), &out, now); err != nil {
		t.Fatal(err)
	}
	md := out.String()
	order := []string{"## Calls over 30.0s", "## Time per tool", "## Same-job comparisons", "Background runs", "## Time-sink candidates", "## Unmapped tools"}
	last := -1
	for _, h := range order {
		i := strings.Index(md, h)
		if i <= last {
			t.Fatalf("section %q missing or out of order:\n%s", h, md)
		}
		last = i
	}
	if !strings.Contains(md, "worker-a") || !strings.Contains(md, "parallel calls overlap") {
		t.Fatalf("slow example lacks session name or overlap note:\n%s", md)
	}
}
