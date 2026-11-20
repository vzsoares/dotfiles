package tui

import (
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// inputCols is the widest an input wraps; narrower panes wrap at their width.
const inputCols = 92

// newInput is the one free-text box: multi-line, soft wrapped, enter adds a
// line (see keyInput). A blinking cursor keeps a timer running in Update.
func newInput(placeholder string) textarea.Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.Placeholder = placeholder
	ta.Cursor.SetMode(cursor.CursorStatic)
	return ta
}

// sizeInput fits ta to a w x h pane, wrapping at inputCols at most.
func sizeInput(ta *textarea.Model, w, h int) {
	ta.SetWidth(max(min(w, inputCols), 1))
	ta.SetHeight(max(h, 1))
}

// keyInput routes a key for an input screen: ctrl+d submits, esc cancels,
// anything else edits ta.
func keyInput(ta *textarea.Model, msg tea.KeyMsg) (submit, cancel bool, cmd tea.Cmd) {
	switch msg.String() {
	case "ctrl+d":
		return true, false, nil
	case "esc":
		return false, true, nil
	}
	*ta, cmd = ta.Update(msg)
	return false, false, cmd
}
