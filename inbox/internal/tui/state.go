package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// panelState is the cockpit's view state kept across restarts, beside the
// quota cache rather than in the inbox db so it needs no migration.
type panelState struct {
	NotesFolded bool `json:"notes_folded"`
}

func statePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "zen", "inbox-panel.json")
}

func readState(path string) panelState {
	var s panelState
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &s) != nil {
		return panelState{}
	}
	return s
}

func writeState(path string, s panelState) error {
	if path == "" {
		return nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
