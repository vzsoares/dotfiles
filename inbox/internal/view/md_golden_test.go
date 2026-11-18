package view

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/md"
)

var updateMD = flag.Bool("update-md", false, "rewrite testdata/md goldens")

const sampleNoteBody = "**The cache move is done; one call is yours.**\n\n" +
	"## What changed\n\n" +
	"- `zen-inbox watch` reads from *SQLite* now\n" +
	"- the old **JSON** file is gone\n\n" +
	"```sh\nzen-inbox list --json | jq '.[0].id'\n```\n\n" +
	"| Store | Reads | Writes |\n|---|---|---|\n| SQLite | 2ms | 5ms |\n| JSON | 40ms | 90ms |\n\n" +
	"> Writes still go through one lock.\n\n" +
	"Spec: [the note](https://example.com/856)."

func sampleDecision() inbox.Decision {
	return inbox.Decision{
		ID: "856.1", Rev: 1, Task: "856",
		Question: "Keep **glamour** as the only renderer for `md.Lines`?",
		Context: "**Lead:** both options render every field.\n\n" +
			"## Trade-off\n\n" +
			"| Option | Cost |\n|---|---|\n| glamour | none |\n| own | ~300 lines |\n\n" +
			"```go\nlines := r.Lines(text, width)\n```\n\n" +
			"> crush does the same; see [crush](https://github.com/charmbracelet/crush).\n\n" +
			"1. *keep* the cache\n2. drop the shim",
		Options: []inbox.Option{
			{Letter: "a", Text: "Keep glamour: no new code"},
			{Letter: "b", Text: "Own renderer: `~300` lines to maintain"},
		},
		Rec: "a",
	}
}

func colourPainter() Painter {
	lr := lipgloss.NewRenderer(io.Discard)
	lr.SetColorProfile(termenv.ANSI)
	return md.New(lr).Cached
}

func mdGolden(t *testing.T, name string, lines []string, width int) {
	t.Helper()
	for _, l := range lines {
		if w := lipgloss.Width(l); w > width {
			t.Errorf("%s: %d-wide line %q", name, w, l)
		}
	}
	got := strings.Join(lines, "\n") + "\n"
	path := filepath.Join("..", "..", "testdata", "md", name+".ansi")
	if *updateMD {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestSampleNoteAndDecisionGoldens(t *testing.T) {
	note := inbox.Note{ID: 1, Title: "Cache move", Body: sampleNoteBody, Reply: "thanks, **ship it**",
		Replies: []inbox.NoteReply{{Text: "thanks, **ship it**", RepliedAt: "2026-01-01T00:00:00+00:00"}}}
	for _, w := range []int{92, 60} {
		p := colourPainter()
		mdGolden(t, "note-"+strconv.Itoa(w), PanelNoteLines(note, w, at, p), w)
		mdGolden(t, "decision-"+strconv.Itoa(w), PanelDecisionLines(sampleDecision(), w, p), w)
	}
}

func TestReviewAnswerJoinsTheQuestionOnlyWhenItFits(t *testing.T) {
	d := sampleDecision()
	plain := func(k md.Key, text string) []string {
		return md.New(lipgloss.NewRenderer(io.Discard)).Lines(text, k.Width)
	}
	pick := &Staged{Kind: "pick", Option: "a"}
	if got := PanelReviewLines(d, pick, 92, plain); len(got) != 1 ||
		!strings.HasSuffix(got[0], "? → a: Keep glamour: no new code") {
		t.Fatalf("short answer not joined: %q", got)
	}
	got := PanelReviewLines(d, pick, 60, plain)
	if len(got) < 2 || !strings.HasPrefix(got[len(got)-1], "  → a: Keep glamour") {
		t.Fatalf("long line not split under the question: %q", got)
	}
	d.Question = "Which?\n\n```\nx\n```"
	got = PanelReviewLines(d, pick, 92, plain)
	if last := got[len(got)-1]; !strings.HasPrefix(last, "  → a:") {
		t.Fatalf("answer joined a multi-line question: %q", got)
	}
}
