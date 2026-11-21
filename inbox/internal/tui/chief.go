package tui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
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
	// waiting means chief is down and the chat is kept for the pass.
	waiting bool
	snap    snapshot
}
type chiefStatusMsg struct{ id, status, err string }

func newChiefBox() textarea.Model {
	return newInput("message to chief")
}

// openChief opens the compose box bound to task ("" for none); esc and a
// send go back to from.
func (m *Model) openChief(task string, from screen) tea.Cmd {
	m.scr, m.flash, m.chiefTask, m.chiefFrom = scChief, "", task, from
	return m.chiefTA.Focus()
}

// chiefSendCmd stores the chat and sends it, or leaves it waiting for chief.
func (m *Model) chiefSendCmd(text string) tea.Cmd {
	m.busy = "Sending to chief…"
	ib, client, task := m.ib, m.am, m.chiefTask
	w, sessions := m.watched(), m.snap.sessions
	return func() tea.Msg {
		c, err := ib.SendChat(task, text)
		msg := chiefSentMsg{id: c.MessageID, err: c.Error, chat: c.ID, waiting: err == nil && c.Status == "waiting"}
		if err != nil && msg.err == "" {
			msg.err = err.Error()
		}
		msg.snap = load(ib, client, w, sessions, false)
		return msg
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
	if msg.snap.err == nil {
		m.snap = msg.snap
	}
	if msg.waiting {
		m.chiefTA.Reset()
		return m.leaveChief("queued for chief: sent when chief is back", true)
	}
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
	submit, cancel, cmd := keyInput(&m.chiefTA, msg)
	switch {
	case cancel:
		return m, m.leaveChief("", false)
	case submit:
		text := m.chiefTA.Value()
		if pyfmt.Blank(text) {
			return m, nil
		}
		return m, m.chiefSendCmd(text)
	}
	return m, cmd
}

func (m *Model) viewChief() frame {
	head := "message chief"
	if m.chiefTask != "" {
		head = pyfmt.Clip(inbox.ChatMessage(m.chiefTask, m.snap.titles[m.chiefTask], "", "")+"· message chief", m.w)
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

// chatDone are the statuses a stored chat's message is not polled in: final
// ones, and those of a chat the delivery pass still has to send.
func chatDone() []string {
	return append([]string{"failed", "waiting", "sending", statusUnknown}, finalStatuses...)
}

// maxChatMisses is how many empty status polls in a row end a chat's tracking.
const maxChatMisses = maxPollFailures

// chiefPassMsg ends a delivery pass; snap is read only when it sent or
// failed something.
type chiefPassMsg struct {
	outcomes []inbox.Outcome
	err      error
	snap     *snapshot
}

// chiefPassEvery is how often the delivery pass runs.
const chiefPassEvery = 15 * time.Second

// chiefPassDue starts a pass when none is in flight and the last one began
// chiefPassEvery ago.
func (m *Model) chiefPassDue() tea.Cmd {
	if m.now().Sub(m.passAt) < chiefPassEvery {
		return nil
	}
	return m.chiefPass()
}

// chiefPass sends what waits for a session that is back (inbox.Pass), then
// polls the status of every stored chat with a message id but the header's
// tracked one.
func (m *Model) chiefPass() tea.Cmd {
	if m.passing {
		return nil
	}
	m.passing, m.passAt = true, m.now()
	ib, client, skip, misses := m.ib, m.am, m.chief.chat, m.chatMisses
	w, sessions := m.watched(), m.snap.sessions
	return func() tea.Msg {
		outcomes, err := ib.Pass()
		msg := chiefPassMsg{outcomes: outcomes, err: err}
		pollChats(ib, client, skip, misses)
		// Read after the polls, which can take seconds: an earlier read would
		// roll the panel back over what changed meanwhile.
		if moved(outcomes) {
			s := load(ib, client, w, sessions, false)
			msg.snap = &s
		}
		return msg
	}
}

// pollChats mirrors agent-manager's status into every stored chat with a
// message id but skip; maxChatMisses empty polls in a row end a chat's tracking.
func pollChats(ib *inbox.Inbox, client am.Client, skip int64, misses map[int64]int) {
	polls, err := ib.PendingChats(chatDone())
	if err != nil {
		return
	}
	for _, c := range polls {
		if c.ID == skip || c.MessageID == "" {
			continue
		}
		status, errText := client.ChiefStatus(c.MessageID)
		if errText != "" || status == "" {
			if misses[c.ID]++; misses[c.ID] >= maxChatMisses {
				_ = ib.UpdateChat(c.ID, "", statusUnknown, "")
				delete(misses, c.ID)
			}
			continue
		}
		delete(misses, c.ID)
		if status != c.Status {
			_ = ib.UpdateChat(c.ID, "", status, "")
		}
	}
}

// moved is whether a pass sent or failed anything.
func moved(outcomes []inbox.Outcome) bool {
	return slices.ContainsFunc(outcomes, func(o inbox.Outcome) bool { return o.Sent || o.Failed })
}

// passFlash is what a pass did: each failure's text, then the targets it
// delivered to with the counts; ok is false when a send failed.
func passFlash(outcomes []inbox.Outcome) (flash string, ok bool) {
	nouns := map[string][2]string{"answers": {"answer", "answers"}, "reply": {"reply", "replies"}, "chat": {"message", "messages"}}
	var sent, parts []string
	for _, o := range outcomes {
		switch {
		case o.Failed:
			parts = append(parts, o.Text)
		case o.Sent:
			noun := nouns[o.Kind][1]
			if o.Count == 1 {
				noun = nouns[o.Kind][0]
			}
			sent = append(sent, o.Name+": "+strconv.Itoa(o.Count)+" "+noun)
		}
	}
	ok = len(parts) == 0 && len(sent) > 0
	if len(sent) > 0 {
		parts = append(parts, "sent to "+strings.Join(sent, " · "))
	}
	return strings.Join(parts, "; "), ok
}

// onChiefPass shows what the pass delivered on the list.
func (m *Model) onChiefPass(msg chiefPassMsg) {
	m.passing = false
	if msg.snap != nil && msg.snap.err == nil && m.busy == "" {
		focused := m.focusedRow()
		m.snap = *msg.snap
		m.refreshNote()
		m.refreshThread()
		m.refocus(focused)
	}
	if m.scr != scList {
		return
	}
	switch flash, ok := passFlash(msg.outcomes); {
	case msg.err != nil:
		m.flash, m.flashOK, m.flashAt = msg.err.Error(), false, m.now()
	case flash != "":
		m.flash, m.flashOK, m.flashAt = flash, ok, m.now()
	}
}

// passNowCmd runs a pass on a keypress and reports every line of it.
func (m *Model) passNowCmd() tea.Cmd {
	m.busy = "Sending what waits…"
	ib, client, sessions := m.ib, m.am, m.snap.sessions
	return func() tea.Msg {
		outcomes, err := ib.Pass()
		lines := make([]string, len(outcomes))
		for i, o := range outcomes {
			lines[i] = o.Text
		}
		flash := strings.Join(lines, "; ")
		switch {
		case err != nil:
			flash = err.Error()
		case flash == "":
			flash = "nothing waiting"
		}
		_, ok := passFlash(outcomes)
		return retriedMsg{flash, err == nil && ok, load(ib, client, watch{}, sessions, false)}
	}
}
