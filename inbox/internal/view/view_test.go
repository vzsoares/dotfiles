package view

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/md"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
)

var at = time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC)

func dec(id, task, sid, created string, gate bool) inbox.Decision {
	return inbox.Decision{ID: id, Task: task, SessionID: sid, SessionName: "spec-" + sid, Question: "Which way?",
		CreatedAt: created, Gate: gate, Status: "open",
		Options: []inbox.Option{{Letter: "a", Text: "l"}, {Letter: "b", Text: "r"}}}
}

func TestRowsOnePerSessionOldestFirst(t *testing.T) {
	open := []inbox.Decision{
		dec("570.1", "570", "s2", "2026-01-01T00:00:02+00:00", false),
		dec("573.1", "573", "s1", "2026-01-01T00:00:01+00:00", false),
		dec("574.g", "574", "s1", "2026-01-01T00:00:04+00:00", true),
		dec("570.2", "570", "s2", "2026-01-01T00:00:03+00:00", false),
	}
	rows := Rows(open, nil)
	if len(rows) != 2 || rows[0].SessionID != "s1" || strings.Join(rows[0].Tasks, ",") != "573,574" ||
		rows[0].Count != 2 || !rows[0].Gate || rows[1].SessionID != "s2" || rows[1].Gate {
		t.Fatalf("%+v", rows)
	}
	if got := RowLabel(rows[0], at); got != "spec-s1 · #573, #574 · 2 open · 59s · [gone] · gate" {
		t.Fatal(got)
	}
}

func TestContextBlockBeforeQuestions(t *testing.T) {
	d := dec("570.1", "570", "s1", "2026-01-01T00:00:00+00:00", false)
	d.Rec, d.Title, d.Brief = "a", "Zen board", "What the board does.\nWhat these settle."
	sessions := am.Sessions{{"id": "s1", "status": "idle", "running": true, "group": "me", "directory": "/x/dotfiles"}}
	lines := SessionLines([]inbox.Decision{d}, sessions, 80, at, false)
	want := []string{"#570 Zen board", "  What the board does.", "  What these settle.",
		"asked by spec-s1 (me, dotfiles) · idle · 1m", "", "570.1  Which way?", "a) l  ← rec", "b) r"}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%q", lines)
	}
	compact := SessionLines([]inbox.Decision{d}, sessions, 80, at, true)
	if compact[4] != "570.1  Which way?" {
		t.Fatalf("%q", compact)
	}
	bare := dec("570.1", "570", "s1", "2026-01-01T00:00:00+00:00", false)
	if l := SessionLines([]inbox.Decision{bare}, sessions[:0], 80, at, false); l[0] != "#570" || l[1] != "asked by spec-s1 · gone · 1m" {
		t.Fatalf("%q", l)
	}
}

func TestPanelOptionsPrefixAndRecMark(t *testing.T) {
	d := dec("7.1", "7", "s1", "2026-01-01T00:00:00+00:00", false)
	d.Rec = "a"
	d.Options = []inbox.Option{{Letter: "a", Text: strings.Repeat("x", 37)}, {Letter: "b", Text: "r"}}
	var widths []int
	same := func(k md.Key, text string) []string {
		widths = append(widths, k.Width)
		return []string{text}
	}
	got := PanelOptionLines(d, 40, same)
	want := []string{"a) " + strings.Repeat("x", 37), "   ← rec", "b) r"}
	if !slices.Equal(got, want) || !slices.Equal(widths, []int{37, 37}) {
		t.Fatalf("%q at widths %v", got, widths)
	}
	d.Options[0].Text = "short"
	if got := PanelOptionLines(d, 40, same); got[0] != "a) short  ← rec" {
		t.Fatalf("%q", got)
	}
}

func TestWrapNeverExceedsWidthOrSplitsWords(t *testing.T) {
	text := strings.TrimSpace(strings.Repeat("lorem ipsum dolor sit amet consectetur ", 20))
	for _, width := range []int{40, 80, 200} {
		lines := Wrap(text, width, "a) ")
		var parts []string
		for _, l := range lines {
			if pyfmt.Len(l) > width {
				t.Fatalf("%d: %q", width, l)
			}
			parts = append(parts, strings.TrimSpace(l))
		}
		if strings.Replace(strings.Join(parts, " "), "a) ", "", 1) != text {
			t.Fatalf("%d: words changed", width)
		}
	}
}

func TestStatusLinesShowHeldAndFailed(t *testing.T) {
	h := dec("7.1", "7", "s1", "x", false)
	o := dec("7.2", "7", "s1", "x", false)
	f := dec("8.1", "8", "s2", "x", false)
	f.DeliveryError = "send refused"
	got := StatusLines([]inbox.Decision{h}, []inbox.Decision{o},
		[]inbox.Batch{{SessionID: "s2", Decisions: []inbox.Decision{f}}})
	want := []string{"held · spec-s1: 7.1 held until 7.2", "failed · spec-s2: 8.1 (send refused); zen-inbox retry s2"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%q", got)
	}
}

func TestAge(t *testing.T) {
	for stamp, want := range map[string]string{
		"2026-01-01T00:00:30+00:00": "30s", "2025-12-31T23:00:00+00:00": "1h",
		"2025-12-29T00:00:00+00:00": "3d", "2026-01-01T00:05:00+00:00": "0s", "garbage": "?",
	} {
		if got := Age(stamp, at); got != want {
			t.Errorf("%s: %s", stamp, got)
		}
	}
}
