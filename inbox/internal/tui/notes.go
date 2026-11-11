package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

var (
	unreadStyle = lipgloss.NewStyle().Bold(true)
	readStyle   = lipgloss.NewStyle().Faint(true)
)

type notesMsg struct {
	err  error
	snap snapshot
}

type noteSentMsg struct {
	res  inbox.NoteDelivery
	err  error
	snap snapshot
}

func noteValue(n inbox.Note) string { return strconv.FormatInt(n.ID, 10) }

// notesHead is the Notes group's header text; folded, it says all read
// instead of counting zero.
func (m *Model) notesHead() string {
	n := inbox.Unread(m.snap.notes)
	if n == 0 && m.notesFolded {
		return "Notes · all read"
	}
	return "Notes · " + strconv.Itoa(n) + " unread"
}

// foldNotes collapses or expands the Notes group, keeps the cursor on it
// and saves the choice.
func (m *Model) foldNotes(fold bool) {
	m.notesFolded = fold
	row := "nh:"
	if !fold && len(m.snap.notes) > 0 {
		row = "n:" + noteValue(m.snap.notes[0])
	}
	m.refocus(row)
	if err := writeState(m.stateFile, panelState{NotesFolded: fold}); err != nil {
		m.flash, m.flashOK = "could not save fold state: "+err.Error(), false
	}
}

// noteItems are the list's note rows, after every decision and retry row;
// folded, the one header row stands for them.
func (m *Model) noteItems(room int) []item {
	if m.notesFolded && len(m.snap.notes) > 0 {
		return []item{{pyfmt.Clip(m.notesHead(), room), "nh:"}}
	}
	out := make([]item, len(m.snap.notes))
	for i, n := range m.snap.notes {
		out[i] = item{pyfmt.Clip(view.NoteLabel(n, m.now()), room), "n:" + noteValue(n)}
	}
	return out
}

// cursorNote is the note under the list cursor.
func (m *Model) cursorNote() (inbox.Note, bool) {
	items := m.listItems()
	if m.cursor >= len(items) {
		return inbox.Note{}, false
	}
	id, ok := strings.CutPrefix(items[m.cursor].value, "n:")
	if !ok {
		return inbox.Note{}, false
	}
	for _, n := range m.snap.notes {
		if noteValue(n) == id {
			return n, true
		}
	}
	return inbox.Note{}, false
}

// refreshNote takes the open note's latest row from the snapshot.
func (m *Model) refreshNote() {
	for _, list := range [][]inbox.Note{m.snap.notes, m.snap.failedNotes} {
		for _, n := range list {
			if n.ID == m.note.ID {
				m.note = n
				return
			}
		}
	}
}

func (m *Model) openNote(id string) {
	for _, n := range m.snap.notes {
		if noteValue(n) == id {
			m.note, m.scr, m.flash = n, scNote, ""
			return
		}
	}
}

func (m *Model) markCmd(id int64, all, read bool) tea.Cmd {
	ib, client, watched, sessions := m.ib, m.am, m.watchedIDs(), m.snap.sessions
	return func() tea.Msg {
		var err error
		if all {
			err = ib.MarkAllNotes(true)
		} else {
			err = ib.MarkNotes([]int64{id}, read)
		}
		return notesMsg{err, load(ib, client, watched, sessions, false)}
	}
}

func (m *Model) onNotes(msg notesMsg) {
	focused := m.focusedRow()
	m.snap = msg.snap
	m.refreshNote()
	m.refocus(focused)
	if msg.err != nil {
		m.flash, m.flashOK = msg.err.Error(), false
	}
	if n := m.menuLen(); n > 0 {
		m.cursor = min(m.cursor, n-1)
	}
}

func (m *Model) replyCmd(n inbox.Note, text string) tea.Cmd {
	m.busy = "Sending reply to " + n.SessionName + "…"
	ib, client, watched, sessions := m.ib, m.am, m.watchedIDs(), m.snap.sessions
	return func() tea.Msg {
		res, err := ib.Reply(n.ID, text)
		return noteSentMsg{res, err, load(ib, client, watched, sessions, false)}
	}
}

func (m *Model) onNoteSent(msg noteSentMsg) {
	m.busy, m.snap = "", msg.snap
	m.refreshNote()
	if msg.err != nil {
		m.scr, m.flash, m.flashOK = scNote, msg.err.Error(), false
		return
	}
	flash := msg.res.Describe()
	if msg.res.OK {
		flash = "reply sent to " + m.note.SessionName + inbox.FollowupText(msg.res.Followup, m.note.SessionID)
	}
	m.scr, m.flash, m.flashOK, m.flashAt = scList, flash, msg.res.OK, m.now()
	m.cursor = min(m.cursor, max(m.menuLen()-1, 0))
	for i, it := range m.listItems() {
		if it.value == "n:"+noteValue(m.note) {
			m.cursor = i
		}
	}
}

func (m *Model) retryNoteCmd(id int64) tea.Cmd {
	m.busy = "Retrying reply n" + strconv.FormatInt(id, 10) + "…"
	ib, client, sessions := m.ib, m.am, m.snap.sessions
	return func() tea.Msg {
		res, err := ib.RetryNote(id)
		flash := res.Describe()
		if err != nil {
			flash = err.Error()
		}
		return retriedMsg{flash, err == nil && res.OK, load(ib, client, nil, sessions, false)}
	}
}

// retryFirst retries the first failure, answers before note replies.
func (m *Model) retryFirst() tea.Cmd {
	switch {
	case len(m.snap.undelivered) > 0:
		return m.retryCmd(m.snap.undelivered[0].SessionID)
	case len(m.snap.failedNotes) > 0:
		return m.retryNoteCmd(m.snap.failedNotes[0].ID)
	}
	return nil
}

func (m *Model) keyNoteScreen(k string) (tea.Model, tea.Cmd) {
	n := m.note
	switch k {
	case "m":
		return m, m.markCmd(n.ID, false, n.Unread())
	case "f":
		if n.Reply != "" || n.Delivery != "none" {
			m.flash, m.flashOK = "already replied", false
			return m, nil
		}
		// A reply refused by the store keeps its text for the next f.
		if m.replyFor != n.ID {
			m.ta.Reset()
			m.replyFor = n.ID
		}
		m.ta.Placeholder = "reply (empty cancels)"
		m.scr, m.flash = scReply, ""
		return m, m.ta.Focus()
	case "esc", "q":
		m.scr, m.flash = scList, ""
	}
	return m, nil
}

func (m *Model) keyReply(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.scr = scNote
		return m, nil
	case "ctrl+d":
		if pyfmt.Blank(m.ta.Value()) {
			m.scr = scNote
			return m, nil
		}
		return m, m.replyCmd(m.note, m.ta.Value())
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

func (m *Model) viewNote() frame {
	n := m.note
	top := []section{{lines: []string{pyfmt.Clip(view.NoteHead(n, m.now()), m.w)}, style: &bannerStyle}}
	if m.flash != "" {
		top = append(top, section{lines: view.WrapRest("» "+m.flash, m.w, "", "  "), style: &bannerStyle})
	}
	return frame{s: scNote, top: top, body: []section{{lines: view.PanelNoteLines(n, m.w, m.paint)}},
		cur: -1, help: true}
}

func (m *Model) viewReply() frame {
	head := pyfmt.Clip("reply to "+m.note.Label()+"  "+m.note.Title, m.w)
	return frame{s: scReply, top: []section{{lines: []string{head}}},
		body: []section{{lines: strings.Split(m.ta.View(), "\n")}}, cur: -1, help: true}
}
