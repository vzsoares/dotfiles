package tui

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

// copyScreen is whether y copies a code block here.
func (m *Model) copyScreen() bool {
	switch m.scr {
	case scNote, scWalk, scFull, scThread, scSession, scAll:
		return true
	}
	return false
}

// copiedLines is the copy toast, wrapped; nil when there is none.
func (m *Model) copiedLines() []string {
	if m.copied == "" {
		return nil
	}
	return view.WrapRest("» "+m.copied, m.w, "", "  ")
}

// keyYank handles y and the block number after it; it reports whether it
// consumed k. Any other key after y cancels the copy and acts as usual.
func (m *Model) keyYank(k string) bool {
	pending := m.yank
	m.yank = false
	if !m.copyScreen() {
		return false
	}
	if !pending {
		if k != "y" {
			return false
		}
		m.layout(false)
		if len(m.blocks) == 0 {
			m.copied = "no code block here"
			return true
		}
		m.yank, m.copied = true, "copy which block? press its number"
		return true
	}
	n, err := strconv.Atoi(k)
	if err != nil || len(k) != 1 {
		return false
	}
	m.layout(false)
	if n >= 1 && n <= len(m.blocks) {
		m.clip(m.blocks[n-1])
		m.copied = "copied " + k
	}
	return true
}

// copyText sets the terminal clipboard by OSC 52, which tmux (set-clipboard
// on) and SSH carry through, and also pbcopy on a local Mac.
func copyText(s string) {
	_, _ = os.Stdout.WriteString(ansi.SetSystemClipboard(s))
	if runtime.GOOS == "darwin" && os.Getenv("SSH_CONNECTION") == "" {
		c := exec.Command("pbcopy")
		c.Stdin = strings.NewReader(s)
		_ = c.Run()
	}
}
