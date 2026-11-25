package am_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/am/amtest"
)

func TestTranscriptMatch(t *testing.T) {
	projects := t.TempDir()
	s := am.StateSession{ID: "s1", Cwd: "/w/repo.x", AgentIDs: []string{"a1", "a0"}}
	dir := "-w-repo-x"
	quoted := "say \"hi\" to\nthe \\ team\twith tabs"
	long := "synthetic " + repeat("lorem ", 60)
	amtest.Transcript(t, projects, dir, "a0", t0.Add(-time.Hour), amtest.Delivered("worker", "old retired body"))
	// zen-rotate clears in place: the new jsonl has an id no column names.
	amtest.Transcript(t, projects, dir, "rotated", t0.Add(time.Minute), amtest.Delivered("worker", quoted),
		amtest.Delivered("worker", long+"tail A"))
	amtest.Transcript(t, projects, dir, "stale", t0.Add(-2*time.Hour), amtest.Delivered("worker", "only in a stale file"))
	files := am.Transcripts(projects, s, t0)
	for _, c := range []struct {
		body string
		want bool
	}{
		{quoted, true},
		{"say \"hi\" to the \\ team with tabs", true},
		{long + "tail B, differs past the compared prefix", true},
		{"only in a stale file", false},
		{"never sent", false},
		{"", false},
	} {
		if got := am.InTranscripts(files, c.body, t0); got != c.want {
			t.Errorf("%.30q: %v, want %v (files %v)", c.body, got, c.want, files)
		}
	}
	// The retired id's file counts whatever its age.
	if !am.InTranscripts(am.Transcripts(projects, s, t0), "old retired body", t0) {
		t.Error("retired transcript not read")
	}
}

func TestTranscriptIgnoresToolResults(t *testing.T) {
	projects := t.TempDir()
	path := filepath.Join(projects, "-w", "a1.jsonl")
	amtest.Transcript(t, projects, "-w", "a1", t0, "unrelated")
	appendLine(t, path, `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"echoed body"}]}}`)
	appendLine(t, path, `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[agent-manager] Message from w:\n\nblock body"}]}}`)
	files := am.Transcripts(projects, am.StateSession{AgentIDs: []string{"a1"}}, t0)
	if am.InTranscripts(files, "echoed body", t0) {
		t.Error("a tool result counted as delivery")
	}
	if !am.InTranscripts(files, "block body", t0) {
		t.Error("a text block did not count")
	}
	if am.InTranscripts(files, "unrelated", t0) {
		t.Error("a user turn without the delivery marker counted")
	}
}

func TestTranscriptSkipsEarlierTurns(t *testing.T) {
	projects := t.TempDir()
	path := filepath.Join(projects, "-w", "a1.jsonl")
	amtest.Transcript(t, projects, "-w", "a1", t0, "unrelated")
	appendLine(t, path, `{"type":"user","timestamp":"`+t0.Add(-time.Hour).UTC().Format(time.RFC3339Nano)+
		`","message":{"role":"user","content":"[agent-manager] Message from w:\n\nsame body"}}`)
	files := am.Transcripts(projects, am.StateSession{AgentIDs: []string{"a1"}}, t0)
	if am.InTranscripts(files, "same body", t0) {
		t.Error("an earlier turn with the same body counted as this delivery")
	}
	if !am.InTranscripts(files, "same body", t0.Add(-time.Hour)) {
		t.Error("a turn after sentAt did not count")
	}
}
