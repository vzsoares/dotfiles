package am

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const rule = "──────────────────────────────"

func box(line string) string {
	return "some output\n\n" + rule + "\n" + line + "\n" + rule + "\n  ? for shortcuts\n"
}

func TestStuckPasteMatcher(t *testing.T) {
	cases := map[string]struct {
		screen string
		want   bool
	}{
		"one placeholder":      {box("❯ [Pasted text #1 +12 lines]"), true},
		"two placeholders":     {box("❯ [Pasted text #1 +12 lines] [Pasted text #2 +3 lines]"), true},
		"no line count":        {box("❯ [Pasted text #1]"), true},
		"nbsp and trailing":    {box("❯ [Pasted text #1 +12 lines]  "), true},
		"blank lines in frame": {rule + "\n\n❯ [Pasted text #1 +2 lines]\n\n" + rule, true},
		"permission dialog":    {box("❯ 1. Yes"), false},
		"empty prompt":         {box("❯ "), false},
		"typed text":           {box("❯ some typed text"), false},
		"placeholder + typed":  {box("❯ [Pasted text #1 +12 lines] and more"), false},
		"typed + placeholder":  {box("❯ fix [Pasted text #1 +12 lines]"), false},
		"scrollback, no frame": {"❯ [Pasted text #1 +12 lines]\nworking on it\n" + box("❯ "), false},
		"rule above only":      {rule + "\n❯ [Pasted text #1 +12 lines]\nhello\n", false},
		"working spinner":      {"✻ Pondering… (3s · esc to interrupt)\n" + box("❯ "), false},
		"empty screen":         {"", false},
	}
	for name, c := range cases {
		if got := StuckPaste(c.screen); got != c.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
}

const claude = `[{"id": "s1", "name": "spec-s1", "tool": "claude", "status": "idle", "running": true}]`

// screens writes the scripted capture-pane output, one file per capture.
func screens(t *testing.T, data string, list ...string) {
	t.Helper()
	dir := filepath.Join(data, "screens")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, s := range list {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(i)), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// fastFollowup shortens the timing; the window must outlast the fake tmux
// processes the loop spawns.
func fastFollowup(t *testing.T, window time.Duration) {
	t.Helper()
	oi, ow := Interval, Window
	Interval, Window = 5*time.Millisecond, window
	t.Cleanup(func() { Interval, Window = oi, ow })
}

func presses(t *testing.T, data string) [][]string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(data, "keys"))
	var out [][]string
	for _, e := range entries {
		raw, _ := os.ReadFile(filepath.Join(data, "keys", e.Name()))
		parts := strings.Split(string(raw), "\x00")
		out = append(out, parts[:len(parts)-1])
	}
	return out
}

func bells(data string) int {
	raw, _ := os.ReadFile(filepath.Join(data, "bells"))
	return strings.Count(string(raw), "window-attention")
}

func waitBells(t *testing.T, data string, want int) {
	t.Helper()
	for i := 0; i < 100 && bells(data) < want; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if got := bells(data); got != want {
		t.Fatalf("bells %d, want %d", got, want)
	}
}

var enter = []string{"-L", "agentmgr", "send-keys", "-t", "am_s1", "Enter"}

func TestFollowupSubmitsAStuckPasteOnce(t *testing.T) {
	fastFollowup(t, 5*time.Second)
	data := fakes(t, claude)
	stuck, clear := box("❯ [Pasted text #1 +12 lines]"), box("❯ ")
	screens(t, data, stuck, stuck, clear)
	if got := (Exec{}).Followup("s1"); got != Submitted {
		t.Fatal(got)
	}
	if got := presses(t, data); len(got) != 1 || !slices.Equal(got[0], enter) {
		t.Fatal(got)
	}
	waitBells(t, data, 0)
}

func TestFollowupNeedsTwoConsecutiveMatches(t *testing.T) {
	fastFollowup(t, 5*time.Second)
	data := fakes(t, claude)
	stuck, clear := box("❯ [Pasted text #1 +12 lines]"), box("❯ ")
	screens(t, data, stuck, clear)
	if got := (Exec{}).Followup("s1"); got != "" || len(presses(t, data)) != 0 {
		t.Fatal(got, presses(t, data))
	}
}

func TestFollowupStopsWhenSessionTurnsWorking(t *testing.T) {
	fastFollowup(t, 5*time.Second)
	data := fakes(t, claude)
	stuck := box("❯ [Pasted text #1 +12 lines]")
	screens(t, data, stuck, stuck, stuck)
	working := `[{"id": "s1", "tool": "claude", "status": "working", "running": true}]`
	if err := os.WriteFile(filepath.Join(data, "sessions.1"), []byte(working), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := (Exec{}).Followup("s1"); got != "" || len(presses(t, data)) != 0 {
		t.Fatal(got, presses(t, data))
	}
}

func TestFollowupReportsStillStuckAfterOnePress(t *testing.T) {
	fastFollowup(t, 5*time.Second)
	data := fakes(t, claude)
	screens(t, data, box("❯ [Pasted text #1 +12 lines]"))
	if got := (Exec{OS: "linux"}).Followup("s1"); got != StillStuck {
		t.Fatal(got)
	}
	if got := presses(t, data); len(got) != 1 {
		t.Fatal(got)
	}
	waitBells(t, data, 1)
}

func TestFollowupSkipsNonClaudeAndGone(t *testing.T) {
	fastFollowup(t, 5*time.Second)
	for _, sessions := range []string{
		`[{"id": "s1", "tool": "codex", "status": "idle", "running": true}]`,
		`[{"id": "s1", "status": "idle", "running": true}]`,
		`[{"id": "s1", "tool": "claude", "status": "dead", "running": false}]`,
		`[]`,
	} {
		data := fakes(t, sessions)
		screens(t, data, box("❯ [Pasted text #1 +12 lines]"))
		if got := (Exec{OS: "linux"}).Followup("s1"); got != "" || len(presses(t, data)) != 0 {
			t.Fatal(sessions, got)
		}
		if _, err := os.Stat(filepath.Join(data, "capture.n")); err == nil {
			t.Fatal("captured the pane of", sessions)
		}
	}
}

func TestFollowupIgnoresCaptureFailure(t *testing.T) {
	fastFollowup(t, 5*time.Second)
	data := fakes(t, claude)
	screens(t, data, box("❯ [Pasted text #1 +12 lines]"))
	if err := os.WriteFile(filepath.Join(data, "capture_rc"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := (Exec{}).Followup("s1"); got != "" || len(presses(t, data)) != 0 {
		t.Fatal(got)
	}
}

func TestFollowupIdlePromptWaitsOutTheWindowWithoutKeys(t *testing.T) {
	fastFollowup(t, 300*time.Millisecond)
	data := fakes(t, claude)
	screens(t, data, box("❯ "))
	if got := (Exec{}).Followup("s1"); got != "" || len(presses(t, data)) != 0 {
		t.Fatal(got)
	}
}
