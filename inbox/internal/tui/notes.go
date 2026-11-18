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

// notesHead is the No task group's header text; folded, it says all read
// instead of counting zero.
func (m *Model) notesHead() string {
	n := inbox.Unread(m.looseNotes())
	if n == 0 && m.notesFolded {
		return "No task · all read"
	}
	return "No task · " + strconv.Itoa(n) + " unread"
}

// foldNotes collapses or expands the No task group, keeps the cursor on it
// and saves the choice.
func (m *Model) foldNotes(fold bool) {
	m.notesFolded = fold
	row := "nh:"
	if loose := m.looseNotes(); !fold && len(loose) > 0 {
		row = "n:" + noteValue(loose[0])
	}
	m.refocus(row)
	m.saveState()
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
			m.note, m.scr, m.flash, m.noteFrom = n, scNote, "", scList
			return
		}
	}
}

// nextNoteRow is the list row to open after finishing n: the next row of the
// Inbox list, or in the cockpit the next row when it is an unread note.
func (m *Model) nextNoteRow(n inbox.Note) (string, bool) {
	if m.noteFrom != scList {
		return "", false
	}
	cur := "n:" + noteValue(n)
	next, ok := m.nextInboxRow(cur)
	if !ok || m.inboxOpen {
		return next, ok
	}
	if strings.HasPrefix(next, "s:") {
		return next, true
	}
	id, isNote := strings.CutPrefix(next, "n:")
	if !isNote {
		return "", false
	}
	for _, c := range m.snap.notes {
		if noteValue(c) == id && c.Unread() {
			return next, true
		}
	}
	return "", false
}

// leaveNote goes back to the list or the thread the note was opened from.
func (m *Model) leaveNote(flash string, ok bool) tea.Cmd {
	if m.noteFrom == scThread {
		cmd := m.backToThread(flash)
		m.flashOK = ok
		return cmd
	}
	m.scr, m.flash, m.flashOK, m.flashAt = scList, flash, ok, m.now()
	m.refocus("n:" + noteValue(m.note))
	return nil
}

func (m *Model) markCmd(id int64, all, read bool) tea.Cmd {
	ib, client, w, sessions := m.ib, m.am, m.watched(), m.snap.sessions
	return func() tea.Msg {
		var err error
		if all {
			err = ib.MarkAllNotes(true)
		} else {
			err = ib.MarkNotes([]int64{id}, read)
		}
		return notesMsg{err, load(ib, client, w, sessions, false)}
	}
}

func (m *Model) onNotes(msg notesMsg) {
	focused := m.focusedRow()
	m.snap = msg.snap
	m.closeEmptyInbox()
	m.refreshNote()
	m.refreshThread()
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
	ib, client, w, sessions := m.ib, m.am, m.watched(), m.snap.sessions
	return func() tea.Msg {
		res, err := ib.Reply(n.ID, text)
		return noteSentMsg{res, err, load(ib, client, w, sessions, false)}
	}
}

func (m *Model) onNoteSent(msg noteSentMsg) tea.Cmd {
	m.busy, m.snap = "", msg.snap
	m.refreshNote()
	if msg.err != nil {
		m.scr, m.flash, m.flashOK = scNote, msg.err.Error(), false
		return nil
	}
	m.ta.Reset()
	m.replyFor = 0
	flash := msg.res.Describe()
	if msg.res.OK {
		flash = "reply sent to " + m.note.SessionName + inbox.FollowupText(msg.res.Followup, m.note.SessionID)
	}
	if m.noteFrom != scThread {
		m.cursor = min(m.cursor, max(len(m.listItems())-1, 0))
	}
	return m.leaveNote(flash, msg.res.OK)
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
		return retriedMsg{flash, err == nil && res.OK, load(ib, client, watch{}, sessions, false)}
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
		if !n.CanReply() {
			m.flash, m.flashOK = "reply "+n.Delivery+": retry it first", false
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
	case "T":
		return m, m.openRefile(n, scNote)
	case "enter":
		next, hasNext := m.nextNoteRow(n)
		fromThread := m.noteFrom == scThread
		cmd := m.leaveNote("", false)
		if hasNext {
			m.openRow(next)
		} else if fromThread {
			cmd = tea.Batch(cmd, m.openNextInThread(""))
		}
		if n.Unread() {
			return m, tea.Batch(cmd, m.markCmd(n.ID, false, true))
		}
		return m, cmd
	case "esc", "q":
		return m, m.leaveNote("", false)
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
	mark, markStyle := "✓ read", &readStyle
	if n.Unread() {
		mark, markStyle = "● unread", &cursorStyle
	}
	head := pyfmt.Clip(view.NoteHead(n, m.now()), max(m.w-lipgloss.Width(mark)-1, 0))
	line := bannerStyle.Render(head) + " " + markStyle.Render(mark)
	top := []section{{lines: []string{line}}}
	if m.copied != "" {
		top = append(top, section{lines: view.WrapRest("» "+m.copied, m.w, "", "  "), style: &bannerStyle})
	}
	if m.flash != "" {
		top = append(top, section{lines: view.WrapRest("» "+m.flash, m.w, "", "  "), style: &bannerStyle})
	}
	return frame{s: scNote, top: top, body: []section{{lines: view.PanelNoteLines(n, m.w, m.now(), m.paint)}},
		cur: -1, help: true}
}

func (m *Model) viewReply() frame {
	head := pyfmt.Clip("reply to "+m.note.Label()+"  "+m.note.Title, m.w)
	return frame{s: scReply, top: []section{{lines: []string{head}}},
		body: []section{{lines: strings.Split(m.ta.View(), "\n")}}, cur: -1, help: true}
}
