package tui

import (
	"slices"
	"strings"
	"time"

	bkey "github.com/charmbracelet/bubbles/key"
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
	checking   bool
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

type chiefSentMsg struct{ id, err string }
type chiefStatusMsg struct{ id, status, err string }

func newChiefBox() textarea.Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.Placeholder = "message to chief"
	ta.KeyMap.InsertNewline = bkey.NewBinding(bkey.WithKeys("ctrl+j", "alt+enter"))
	return ta
}

func (m *Model) openChief() tea.Cmd {
	m.scr, m.flash = scChief, ""
	return m.chiefTA.Focus()
}

func (m *Model) chiefSendCmd(text string) tea.Cmd {
	m.busy = "Sending to chief…"
	client := m.am
	return func() tea.Msg {
		id, errText := client.Chief(text)
		return chiefSentMsg{id, errText}
	}
}

func (m *Model) onChiefSent(msg chiefSentMsg) tea.Cmd {
	m.busy = ""
	if msg.err != "" {
		m.scr, m.flash, m.flashOK = scChief, msg.err, false
		return m.chiefTA.Focus()
	}
	m.chiefTA.Reset()
	m.chief = chiefMsg{id: msg.id, status: "queued"}
	m.scr, m.flash, m.flashOK, m.flashAt = scList, "message "+msg.id+" queued for chief", true, m.now()
	return m.chiefPoll()
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

func (m *Model) onChiefStatus(msg chiefStatusMsg) {
	if msg.id != m.chief.id {
		return
	}
	m.chief.checking = false
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
		m.scr, m.flash = scList, ""
		return m, nil
	case "enter":
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
	top := []section{{lines: []string{"message chief"}}}
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
