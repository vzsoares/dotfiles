package am

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ProjectsEnv overrides where Claude Code's transcripts are read from.
const ProjectsEnv = "ZEN_CLAUDE_PROJECTS"

// ProjectsDir is $ZEN_CLAUDE_PROJECTS, else $CLAUDE_CONFIG_DIR/projects, else
// ~/.claude/projects.
func ProjectsDir() string {
	if v := os.Getenv(ProjectsEnv); v != "" {
		return v
	}
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return filepath.Join(v, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// projectDir is Claude Code's directory name for a cwd.
func projectDir(cwd string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, cwd)
}

// Transcripts are the jsonl files that may hold a message sent to s at
// sentAt: those named for its agent session ids in any project dir, and,
// since a rotation clears a session in place, every jsonl in those dirs and
// in its cwd's dir written at or after sentAt.
func Transcripts(projects string, s StateSession, sentAt time.Time) []string {
	if projects == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	dirs := map[string]bool{}
	for _, id := range s.AgentIDs {
		matches, _ := filepath.Glob(filepath.Join(projects, "*", id+".jsonl"))
		for _, p := range matches {
			add(p)
			dirs[filepath.Dir(p)] = true
		}
	}
	if s.Cwd != "" {
		dirs[filepath.Join(projects, projectDir(s.Cwd))] = true
	}
	for dir := range dirs {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		for _, p := range matches {
			if st, err := os.Stat(p); err == nil && !st.ModTime().Before(sentAt.Add(-time.Second)) {
				add(p)
			}
		}
	}
	return out
}

// matchRunes is how much of a body is compared: agent-manager may wrap or
// clip a long message on delivery.
const matchRunes = 200

// squash folds runs of whitespace, which a paste into the prompt can change.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

func matchKey(body string) string {
	k := []rune(squash(body))
	if len(k) > matchRunes {
		k = k[:matchRunes]
	}
	return string(k)
}

// Marker starts the user turn agent-manager pastes for a delivered message.
const Marker = "[agent-manager] Message from"

// InTranscripts reports whether body is in a user turn of one of files that
// carries Marker; tool results do not count, since a tool can echo a message
// it never delivered, and the marker keeps a short body from matching any turn.
func InTranscripts(files []string, body string) bool {
	key := matchKey(body)
	if key == "" {
		return false
	}
	for _, f := range files {
		if found, _ := inFile(f, key); found {
			return true
		}
	}
	return false
}

type turn struct {
	Type    string `json:"type"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func inFile(path, key string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<16)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && bytes.Contains(line, []byte(`"user"`)) && userText(line, key) {
			return true, nil
		}
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
}

func userText(line []byte, key string) bool {
	var t turn
	if json.Unmarshal(line, &t) != nil || t.Type != "user" {
		return false
	}
	var text string
	if json.Unmarshal(t.Message.Content, &text) == nil {
		return delivered(text, key)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(t.Message.Content, &blocks) != nil {
		return false
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return delivered(strings.Join(parts, "\n"), key)
}

func delivered(text, key string) bool {
	return strings.Contains(text, Marker) && strings.Contains(squash(text), key)
}
