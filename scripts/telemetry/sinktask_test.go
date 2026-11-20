package main

import (
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

const day = 24 * time.Hour

func runEntryCmd(t *testing.T, args ...string) string {
	t.Helper()
	var out strings.Builder
	if code := run(append([]string{"entry"}, args...), nil, &out, io.Discard, now); code != 0 {
		t.Fatalf("entry %v: exit %d", args, code)
	}
	return strings.TrimSpace(out.String())
}

func TestHalvedBoundary(t *testing.T) {
	for _, c := range []struct {
		prev, cur int64
		want      bool
	}{{100, 51, false}, {100, 50, true}, {100, 0, true}, {0, 0, false}, {100, 120, false}} {
		if got := halved(c.prev, c.cur); got != c.want {
			t.Fatalf("halved(%d, %d) = %v", c.prev, c.cur, got)
		}
	}
}

func TestChecklistCloseRule(t *testing.T) {
	fixture(t, []fx{
		// bash:bun drops 49%: stays open.
		{session: "s1", tool: "Bash", cmd: "bun test", ago: 10 * day, durMS: 100_000, ok: true},
		{session: "s1", tool: "Bash", cmd: "bun test", ago: 2 * day, durMS: 51_000, ok: true},
		// bash:make drops 50%: closes.
		{session: "s1", tool: "Bash", cmd: "make", ago: 10 * day, durMS: 100_000, ok: true},
		{session: "s1", tool: "Bash", cmd: "make", ago: 2 * day, durMS: 50_000, ok: true},
		// bash:git has no open entry, so it is never closed.
		{session: "s1", tool: "Bash", cmd: "git status", ago: 10 * day, durMS: 100_000, ok: true},
	})
	runEntryCmd(t, "add", "bash:bun")
	runEntryCmd(t, "add", "bash:make")
	r, _ := report(t, defaults())
	if len(r.Checklist.Close) != 1 {
		t.Fatalf("close %+v", r.Checklist.Close)
	}
	c := r.Checklist.Close[0]
	if c.Key != "bash:make" || c.Match != "bash:make ·" || c.PrevMS != 100_000 || c.CurMS != 50_000 || c.DropPct != 50 {
		t.Fatalf("close %+v", c)
	}
	if got := runEntryCmd(t, "close", "bash:make"); got != "closed" {
		t.Fatal(got)
	}
	if code := run([]string{"entry", "close", "bash:make"}, nil, io.Discard, io.Discard, now); code != 1 {
		t.Fatalf("closing a closed entry: %d", code)
	}
	r, _ = report(t, defaults())
	if len(r.Checklist.Close) != 0 {
		t.Fatalf("closed entry listed again: %+v", r.Checklist.Close)
	}
}

func TestChecklistAppendsNewSinkOnce(t *testing.T) {
	fixture(t, reportCalls)
	r, _ := report(t, defaults())
	var keys []string
	for _, a := range r.Checklist.Append {
		keys = append(keys, a.Key)
	}
	if !reflect.DeepEqual(keys, []string{"bash:bun", "bash:curl", "bash:agent-manager wait",
		"mcp__claude_ai_Notion__notion-fetch", "Grep", "bash:zen-board"}) {
		t.Fatalf("append %v", keys)
	}
	if !strings.HasPrefix(r.Checklist.Append[0].Item, "bash:bun · ") {
		t.Fatalf("item %q", r.Checklist.Append[0].Item)
	}
	if got := r.Checklist.Append[4].Item; got != "Grep · 4.9K out over 7 days (2026-W46)" {
		t.Fatalf("byte sink item %q", got)
	}
	if got := runEntryCmd(t, "add", "bash:bun"); got != "added" {
		t.Fatal(got)
	}
	if got := runEntryCmd(t, "add", "bash:bun"); got != "already open" {
		t.Fatal(got)
	}
	r, _ = report(t, defaults())
	if r.Checklist.Append[0].Key != "bash:curl" || len(r.Checklist.Append) != 5 {
		t.Fatalf("open sink appended again: %+v", r.Checklist.Append)
	}
	runEntryCmd(t, "close", "bash:bun")
	if got := runEntryCmd(t, "add", "bash:bun"); got != "added" {
		t.Fatalf("closed entry not reopened: %s", got)
	}
}

func TestSinkTaskFoldsFiledSinks(t *testing.T) {
	fixture(t, reportCalls)
	week := isoWeek(now)
	for _, f := range [][2]string{{"bash:python3", "760"}, {"bash:for", "758"}} {
		if code := run([]string{"filed", week, f[0], f[1]}, nil, io.Discard, os.Stderr, now); code != 0 {
			t.Fatal(code)
		}
	}
	var out strings.Builder
	if code := run([]string{"sink-task", "#900", "--fold", "#760"}, nil, &out, os.Stderr, now); code != 0 {
		t.Fatal(code)
	}
	if out.String() != "bash:python3\n" {
		t.Fatalf("folded %q", out.String())
	}
	if got := runEntryCmd(t, "add", "bash:python3"); got != "already open" {
		t.Fatal(got)
	}
	out.Reset()
	run([]string{"sink-task"}, nil, &out, os.Stderr, now)
	if out.String() != "900\n" {
		t.Fatalf("sink-task %q", out.String())
	}
	r, _ := report(t, defaults())
	if r.Checklist.Task != "900" {
		t.Fatalf("task %q", r.Checklist.Task)
	}
	runEntryCmd(t, "close", "bash:python3")
	out.Reset()
	run([]string{"sink-task", "900", "--fold", "760"}, nil, &out, os.Stderr, now)
	if out.String() != "" {
		t.Fatalf("fold reopened a ticked entry: %q", out.String())
	}
	if code := run([]string{"sink-task", "--fold", "760"}, nil, io.Discard, io.Discard, now); code != 2 {
		t.Fatalf("fold without task: %d", code)
	}
}

func TestChecklistCloseChainedPrimary(t *testing.T) {
	fixture(t, []fx{
		{session: "s1", tool: "Bash", cmd: "python3 -c 'print(1)' && zen-lock core-heavy -- just check", ago: 10 * day, durMS: 100_000, ok: true},
		{session: "s1", tool: "Bash", cmd: "python3 -c 'print(1)' && zen-lock core-heavy -- just check", ago: 2 * day, durMS: 50_000, ok: true},
	})
	runEntryCmd(t, "add", "bash:zen-lock")
	r, _ := report(t, defaults())
	if len(r.Checklist.Close) != 1 {
		t.Fatalf("close %+v", r.Checklist.Close)
	}
	if c := r.Checklist.Close[0]; c.Key != "bash:zen-lock" || c.PrevMS != 100_000 || c.CurMS != 50_000 {
		t.Fatalf("close %+v", c)
	}
}

func TestChecklistCloseNeedsBothMetrics(t *testing.T) {
	fixture(t, []fx{
		// Time halves, output unchanged: stays open.
		{session: "s1", tool: "Bash", cmd: "bun test", ago: 10 * day, durMS: 100_000, ok: true, out: 1000},
		{session: "s1", tool: "Bash", cmd: "bun test", ago: 2 * day, durMS: 50_000, ok: true, out: 1000},
		// Output halves with no recorded time: closes.
		{session: "s1", tool: "Grep", ago: 10 * day, ok: true, out: 1000},
		{session: "s1", tool: "Grep", ago: 2 * day, ok: true, out: 400},
	})
	runEntryCmd(t, "add", "bash:bun")
	runEntryCmd(t, "add", "Grep")
	r, _ := report(t, defaults())
	if len(r.Checklist.Close) != 1 {
		t.Fatalf("close %+v", r.Checklist.Close)
	}
	c := r.Checklist.Close[0]
	if c.Key != "Grep" || c.PrevOut != 1000 || c.CurOut != 400 || c.DropPct != 60 {
		t.Fatalf("close %+v", c)
	}
}
