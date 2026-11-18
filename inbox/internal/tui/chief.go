package tui

import (
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

// finalStatuses end agent-manager's message life; anything else is polled.
var finalStatuses = []string{"delivered", "dropped", "answered"}

// chiefMsg is the last message sent to chief and where it stands.
type chiefMsg struct {
	id, status string
	// chat is the stored row the status is mirrored into.
	chat     int64
	checking bool
	// failed counts consecutive status polls that gave nothing.
	failed int
	// doneAt is when the status turned final.
	doneAt time.Time
}

func (c chiefMsg) final() bool {
	return slices.Contains(finalStatuses, c.status) || c.status == statusUnknown
}

// statusUnknown is shown when agent-manager cannot report the message.
const statusUnknown = "sent"

// maxPollFailures is how many empty polls in a row end tracking.
const maxPollFailures = 3

type chiefSentMsg struct {
	id, err string
	chat    int64
}
type chiefStatusMsg struct{ id, status, err string }

func newChiefBox() textarea.Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.Placeholder = "message to chief"
	return ta
}

// openChief opens the compose box bound to task ("" for none); esc and a
// send go back to from.
func (m *Model) openChief(task string, from screen) tea.Cmd {
	m.scr, m.flash, m.chiefTask, m.chiefFrom = scChief, "", task, from
	return m.chiefTA.Focus()
}

// chiefText is what chief receives: a thread's message leads with its task and title.
func chiefText(task, title, text string) string {
	if task == "" {
		return text
	}
	return "[" + pyfmt.RStrip("#"+task+" "+title) + "] " + text
}

// chiefSendCmd stores the chat, sends it, then records the message id or the error.
func (m *Model) chiefSendCmd(text string) tea.Cmd {
	m.busy = "Sending to chief…"
	ib, client, task, mu := m.ib, m.am, m.chiefTask, m.chiefMu
	msg := chiefText(task, m.snap.titles[task], text)
	return func() tea.Msg {
		mu.Lock()
		defer mu.Unlock()
		c, err := ib.AddChat(task, text)
		if err != nil {
			return chiefSentMsg{err: err.Error()}
		}
		id, errText := client.Chief(msg)
		status := "queued"
		if errText != "" {
			status = "failed"
		}
		if err := ib.UpdateChat(c.ID, id, status, errText); err != nil && errText == "" {
			errText = "sent as " + id + ", not recorded: " + err.Error()
		}
		return chiefSentMsg{id, errText, c.ID}
	}
}

// leaveChief goes back to the screen c was pressed on.
func (m *Model) leaveChief(flash string, ok bool) tea.Cmd {
	if m.chiefFrom == scThread {
		cmd := m.backToThread(flash)
		m.flashOK = ok
		return cmd
	}
	m.scr, m.flash, m.flashOK, m.flashAt = scList, flash, ok, m.now()
	return nil
}

func (m *Model) onChiefSent(msg chiefSentMsg) tea.Cmd {
	m.busy = ""
	if msg.err != "" && msg.id == "" {
		m.scr, m.flash, m.flashOK = scChief, msg.err, false
		return m.chiefTA.Focus()
	}
	m.chiefTA.Reset()
	m.chief = chiefMsg{id: msg.id, status: "queued", chat: msg.chat}
	flash, ok := "message "+msg.id+" queued for chief", true
	if msg.err != "" {
		flash, ok = msg.err, false
	}
	return tea.Batch(m.leaveChief(flash, ok), m.chiefPoll())
}

// chiefPoll asks for the status of the tracked message unless one ask is out.
func (m *Model) chiefPoll() tea.Cmd {
	if m.chief.id == "" || m.chief.final() || m.chief.checking {
		return nil
	}
	m.chief.checking = true
	client, id := m.am, m.chief.id
	return func() tea.Msg {
		status, errText := client.ChiefStatus(id)
		return chiefStatusMsg{id, status, errText}
	}
}

// onChiefStatus tracks the status and mirrors a change into the stored chat.
func (m *Model) onChiefStatus(msg chiefStatusMsg) tea.Cmd {
	if msg.id != m.chief.id {
		return nil
	}
	m.chief.checking = false
	was := m.chief.status
	if msg.err != "" || msg.status == "" {
		if m.chief.failed++; m.chief.failed >= maxPollFailures {
			m.chief.status = statusUnknown
		}
	} else {
		m.chief.failed, m.chief.status = 0, msg.status
	}
	if m.chief.final() {
		m.chief.doneAt = m.now()
	}
	if m.chief.status == was || m.chief.chat == 0 {
		return nil
	}
	ib, chat, status := m.ib, m.chief.chat, m.chief.status
	return func() tea.Msg {
		if err := ib.UpdateChat(chat, "", status, ""); err != nil {
			return chiefStatusMsg{err: "chat status not recorded: " + err.Error()}
		}
		return nil
	}
}

// expireChief drops a final status after FlashFor.
func (m *Model) expireChief() {
	if m.chief.final() && m.now().Sub(m.chief.doneAt) >= FlashFor {
		m.chief = chiefMsg{}
	}
}

func (m *Model) keyChief(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, m.leaveChief("", false)
	case "ctrl+d":
		text := m.chiefTA.Value()
		if pyfmt.Blank(text) {
			return m, nil
		}
		return m, m.chiefSendCmd(text)
	}
	var cmd tea.Cmd
	m.chiefTA, cmd = m.chiefTA.Update(msg)
	return m, cmd
}

func (m *Model) viewChief() frame {
	head := "message chief"
	if m.chiefTask != "" {
		head = pyfmt.Clip(chiefText(m.chiefTask, m.snap.titles[m.chiefTask], "")+"· message chief", m.w)
	}
	top := []section{{lines: []string{head}}}
	if m.flash != "" {
		top = append(top, section{lines: view.WrapRest("» "+m.flash, m.w, "", "  "), style: &bannerStyle})
	}
	return frame{s: scChief, top: top,
		body: []section{{lines: strings.Split(m.chiefTA.View(), "\n")}}, cur: -1, help: true}
}

// chiefStatusLine is the header's text while a message is tracked.
func (m *Model) chiefStatusLine() string {
	return "✉ chief · message " + m.chief.id + " " + m.chief.status
}

// chatDone are the statuses a stored chat is not worked on any more.
func chatDone() []string { return append([]string{"failed", statusUnknown}, finalStatuses...) }

// maxChatMisses is how many empty status polls in a row end a chat's tracking.
const maxChatMisses = maxPollFailures

type chiefPassMsg struct{}

// chiefPass works every stored chat but the header's tracked one, oldest
// first: a chat that never got a message id is sent, the rest get a status
// poll. A send that fails stops the pass so later chats stay in order.
func (m *Model) chiefPass() tea.Cmd {
	if m.passing {
		return nil
	}
	m.passing = true
	ib, client, mu, skip, misses := m.ib, m.am, m.chiefMu, m.chief.chat, m.chatMisses
	return func() tea.Msg {
		mu.Lock()
		defer mu.Unlock()
		chats, err := ib.PendingChats(chatDone())
		if err != nil {
			return chiefPassMsg{}
		}
		titles, _ := ib.TaskTitles()
		for _, c := range chats {
			if c.ID == skip {
				continue
			}
			if c.MessageID == "" {
				id, errText := client.Chief(chiefText(c.Task, titles[c.Task], c.Text))
				if errText != "" {
					return chiefPassMsg{}
				}
				if ib.UpdateChat(c.ID, id, "queued", "") != nil {
					return chiefPassMsg{}
				}
				continue
			}
			status, errText := client.ChiefStatus(c.MessageID)
			if errText != "" || status == "" {
				if misses[c.ID]++; misses[c.ID] >= maxChatMisses {
					status = statusUnknown
					delete(misses, c.ID)
					_ = ib.UpdateChat(c.ID, "", status, "")
				}
				continue
			}
			delete(misses, c.ID)
			if status != c.Status {
				_ = ib.UpdateChat(c.ID, "", status, "")
			}
		}
		return chiefPassMsg{}
	}
}
