package inbox

import (
	"strings"
	"testing"
)

func TestBuildMessagePrintsSharedNoteOnce(t *testing.T) {
	mk := func(id, note string) Decision {
		return Decision{ID: id, Question: "Q", AnswerKind: "text", AnswerText: "x", Note: note}
	}
	msg := BuildMessage([]Decision{mk("1.1", "same"), mk("1.2", "same"), mk("1.3", "own")}, 10000)
	if strings.Count(msg, "same") != 1 || !strings.Contains(msg, "\nNote on 1.1, 1.2: same") || !strings.Contains(msg, "Note: own") {
		t.Fatalf("message:\n%s", msg)
	}
}

func TestBuildMessageCountsNoteInBudget(t *testing.T) {
	long := strings.Repeat("n", 9000)
	var ds []Decision
	for _, id := range []string{"1.1", "1.2", "1.3"} {
		ds = append(ds, Decision{ID: id, Question: "Q", AnswerKind: "text", AnswerText: "a", Note: long})
	}
	msg := BuildMessage(ds, SendLimit)
	if len(msg) > SendLimit-SendMargin {
		t.Fatalf("message %d bytes over budget", len(msg))
	}
	if strings.Contains(msg, "truncated") {
		t.Fatalf("answers cut, not the note:\n%.300s", msg)
	}
	for _, id := range []string{"1.1", "1.2", "1.3"} {
		if !strings.Contains(msg, id+" … → free text: a") {
			t.Errorf("answer %s lost", id)
		}
	}
	if !strings.Contains(msg, "Note: n") || !strings.Contains(msg, "…") {
		t.Errorf("note not clipped:\n%.300s", msg)
	}
}

func TestBuildMessageSharedNoteNamesItsAnswers(t *testing.T) {
	mk := func(id, note string) Decision {
		return Decision{ID: id, Question: "Q", AnswerKind: "text", AnswerText: "x", Note: note}
	}
	msg := BuildMessage([]Decision{mk("1.1", ""), mk("1.2", "ship"), mk("1.3", "ship")}, 10000)
	if !strings.Contains(msg, "Note on 1.2, 1.3: ship") || strings.Contains(msg, "\nNote: ship") {
		t.Fatalf("message:\n%s", msg)
	}
}
