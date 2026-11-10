package am

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Outcomes of Followup.
const (
	Submitted  = "submitted"
	StillStuck = "still stuck"
)

// Followup timing; tests shorten both.
var (
	Interval = time.Second
	Window   = 15 * time.Second
)

var pasteLine = regexp.MustCompile(`^❯ (\[Pasted text #\d+( \+\d+ lines)?\] ?)+$`)

func isRule(line string) bool {
	line = strings.TrimSpace(line)
	return line != "" && strings.Trim(line, "─") == ""
}

// StuckPaste reports whether the screen's input box holds nothing but unsent
// paste placeholders. The box must be framed by ─ rules, so scrollback and
// dialogs never match.
func StuckPaste(screen string) bool {
	lines := strings.Split(strings.ReplaceAll(screen, "\u00a0", " "), "\n")
	for i, line := range lines {
		if !pasteLine.MatchString(strings.TrimRight(line, " ")) {
			continue
		}
		above, below := i-1, i+1
		for above >= 0 && strings.TrimSpace(lines[above]) == "" {
			above--
		}
		for below < len(lines) && strings.TrimSpace(lines[below]) == "" {
			below++
		}
		if above >= 0 && below < len(lines) && isRule(lines[above]) && isRule(lines[below]) {
			return true
		}
	}
	return false
}

func capture(sessionID string) (string, bool) {
	r, err := run([]string{"tmux", "-L", Socket, "capture-pane", "-p", "-t", "am_" + sessionID})
	return r.stdout, err == nil && r.code == 0
}

func pressEnter(sessionID string) bool {
	r, err := run([]string{"tmux", "-L", Socket, "send-keys", "-t", "am_" + sessionID, "Enter"})
	return err == nil && r.code == 0
}

// Followup submits a paste that agent-manager's Enter left unsent. It presses
// Enter once, only after two consecutive captures show the stuck paste, and
// never changes delivery state: errors end it silently with "".
func (e Exec) Followup(sessionID string) string {
	sess := Find(e.Sessions(), sessionID)
	if tool, _ := sess["tool"].(string); tool != "claude" || Status(sess) == "gone" {
		return ""
	}
	deadline := time.Now().Add(Window)
	streak, pressed := 0, false
	for {
		screen, ok := capture(sessionID)
		if !ok {
			return ""
		}
		stuck := StuckPaste(screen)
		if pressed {
			if !stuck {
				return Submitted
			}
			e.Bell(fmt.Sprintf("%s: pasted answer still stuck", name(sessionID, sess)))
			return StillStuck
		}
		if st := Status(Find(e.Sessions(), sessionID)); st == "gone" || st == "working" {
			return ""
		}
		switch {
		case stuck:
			streak++
		case streak > 0:
			return ""
		}
		if streak >= 2 {
			if !pressEnter(sessionID) {
				return ""
			}
			pressed = true
		} else if time.Now().After(deadline) {
			return ""
		}
		time.Sleep(Interval)
	}
}

func name(sessionID string, s Session) string {
	if n, ok := s["name"].(string); ok && n != "" {
		return n
	}
	return sessionID
}
