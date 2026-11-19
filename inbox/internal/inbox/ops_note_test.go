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
	if strings.Count(msg, "same") != 1 || !strings.Contains(msg, "\nNote: same") || !strings.Contains(msg, "Note: own") {
		t.Fatalf("message:\n%s", msg)
	}
}
