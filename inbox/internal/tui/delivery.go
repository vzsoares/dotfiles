package tui

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

// deliveryScanEvery is how often the panel copies agent-manager's delivery
// problems into the log, well inside its ~12 h row window.
const deliveryScanEvery = 60 * time.Second

type deliveryScanMsg struct {
	rep inbox.ScanReport
	err error
}

type deliveryActMsg struct {
	flash string
	ok    bool
	snap  snapshot
}

// deliveryScanDue starts a scan when none is in flight and the last began
// deliveryScanEvery ago.
func (m *Model) deliveryScanDue() tea.Cmd {
	if m.dlScanning || (!m.dlScanAt.IsZero() && m.now().Sub(m.dlScanAt) < deliveryScanEvery) {
		return nil
	}
	m.dlScanning, m.dlScanAt = true, m.now()
	ib := m.ib
	return func() tea.Msg {
		rep, err := ib.ScanDelivery()
		return deliveryScanMsg{rep, err}
	}
}

// onDeliveryScan keeps why state.db could not be read, shown on the Delivery
// screen rather than flashed every minute, and rereads when the log moved.
func (m *Model) onDeliveryScan(msg deliveryScanMsg) tea.Cmd {
	m.dlScanning = false
	switch {
	case msg.err != nil:
		m.dlWhy = msg.err.Error()
		return nil
	case msg.rep.Unavailable != "":
		m.dlWhy = "unavailable: " + msg.rep.Unavailable
	default:
		m.dlWhy = ""
	}
	if msg.rep.Open != len(m.openDelivery()) || msg.rep.FalseDropsWeek != m.snap.falseWeek {
		return m.loadCmd(false)
	}
	return nil
}

// openDelivery are the snapshot's open entries.
func (m *Model) openDelivery() []inbox.Entry {
	var out []inbox.Entry
	for _, e := range m.snap.delivery {
		if e.Open() {
			out = append(out, e)
		}
	}
	return out
}

// deliveryShown are the entries the Delivery screen lists: open ones, and
// the closed ones too after a.
func (m *Model) deliveryShown() []inbox.Entry {
	if m.dlAll {
		return m.snap.delivery
	}
	return m.openDelivery()
}

// deliveryRow is the cockpit's Delivery row, shown while an entry is open;
// red when one is lost.
func (m *Model) deliveryRow(room int) (listRow, bool) {
	open := m.openDelivery()
	if len(open) == 0 {
		return listRow{}, false
	}
	label, lost := view.DeliveryRow(open)
	st := &bannerStyle
	if lost {
		st = &failStyle
	}
	return listRow{item{pyfmt.Clip(label, room), "dl:"}, gInbox, st}, true
}

func (m *Model) openDeliveryScreen() {
	m.scr, m.cursor, m.flash = scDelivery, 0, ""
}

func (m *Model) shownEntry() (inbox.Entry, bool) {
	for _, e := range m.snap.delivery {
		if e.ID == m.dlEntry {
			return e, true
		}
	}
	return inbox.Entry{}, false
}

// cursorEntry is the entry the key acts on: the one shown, else the one
// under the list cursor.
func (m *Model) cursorEntry() (inbox.Entry, bool) {
	if m.scr == scDeliveryEntry {
		return m.shownEntry()
	}
	if es := m.deliveryShown(); m.cursor < len(es) {
		return es[m.cursor], true
	}
	return inbox.Entry{}, false
}

func (m *Model) deliveryActCmd(e inbox.Entry, resend bool) tea.Cmd {
	m.busy = "Dismissing entry " + strconv.FormatInt(e.ID, 10) + "…"
	if resend {
		m.busy = "Resending to " + e.Recipient() + "…"
	}
	ib, client, w, sessions := m.ib, m.am, m.watched(), m.snap.sessions
	return func() tea.Msg {
		var flash string
		var err error
		if resend {
			_, flash, err = ib.Resend(e.ID)
		} else {
			_, err = ib.Dismiss(e.ID)
			flash = "dismissed entry " + strconv.FormatInt(e.ID, 10)
		}
		if err != nil {
			flash = err.Error()
		}
		return deliveryActMsg{flash, err == nil, load(ib, client, w, sessions, false)}
	}
}

func (m *Model) onDeliveryAct(msg deliveryActMsg) {
	m.busy = ""
	if msg.snap.err == nil {
		m.snap = msg.snap
	}
	if m.scr == scDeliveryEntry {
		if e, ok := m.shownEntry(); !ok || (!e.Open() && !m.dlAll) {
			m.scr = scDelivery
		}
	}
	m.cursor = min(m.cursor, max(len(m.deliveryShown())-1, 0))
	m.flash, m.flashOK, m.flashAt = msg.flash, msg.ok, m.now()
}

func (m *Model) keyDelivery(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc", "q":
		if m.scr == scDeliveryEntry {
			m.scr, m.flash = scDelivery, ""
			return m, nil
		}
		m.scr, m.flash = scList, ""
		m.refocus("dl:")
		return m, nil
	case "enter":
		if e, ok := m.cursorEntry(); ok && m.scr == scDelivery {
			m.scr, m.dlEntry, m.flash = scDeliveryEntry, e.ID, ""
		}
	case "a":
		m.dlAll = !m.dlAll
		return m, m.loadCmd(false)
	case "R":
		if e, ok := m.cursorEntry(); ok {
			if !e.Open() || e.Body == "" {
				m.flash, m.flashOK = "nothing to resend for entry "+strconv.FormatInt(e.ID, 10), false
				return m, nil
			}
			return m, m.deliveryActCmd(e, true)
		}
	case "x":
		if e, ok := m.cursorEntry(); ok && e.Open() {
			return m, m.deliveryActCmd(e, false)
		}
	}
	return m, nil
}

func (m *Model) deliveryHead() string {
	open := len(m.openDelivery())
	head := "Delivery · " + strconv.Itoa(open) + " open · " + plural(m.snap.falseWeek, "false drop") + " this week"
	if m.dlAll {
		head += " · closed shown"
	}
	return head
}

func (m *Model) deliveryTop() []section {
	top := []section{{lines: []string{pyfmt.Clip(m.deliveryHead(), m.w)}, style: &headerStyle}}
	if m.dlWhy != "" {
		top = append(top, section{lines: view.WrapRest(m.dlWhy, m.w, "", "  "), style: &failStyle})
	}
	if m.flash != "" {
		style := &bannerStyle
		if m.flashOK {
			style = &okStyle
		}
		top = append(top, section{lines: view.WrapRest("» "+m.flash, m.w, "", "  "), style: style})
	}
	return top
}

func (m *Model) viewDelivery() frame {
	es := m.deliveryShown()
	if len(es) == 0 {
		msg := "Nothing open · false drops are logged closed; a shows them"
		if m.dlAll {
			msg = "Nothing logged"
		}
		return frame{s: scDelivery, top: m.deliveryTop(), body: []section{{lines: []string{"", msg}, style: &readStyle}}, cur: -1, help: true}
	}
	at := m.now()
	items := make([]item, len(es))
	for i, e := range es {
		items[i] = item{view.DeliveryLine(e, at), strconv.FormatInt(e.ID, 10)}
	}
	body := menuSection(items, m.cursor, len(items), m.w)
	for i, e := range es {
		if i != m.cursor && e.Kind == inbox.KindLost && e.Open() {
			body[i].style = &failStyle
		} else if i != m.cursor && !e.Open() {
			body[i].style = &readStyle
		}
	}
	return frame{s: scDelivery, top: m.deliveryTop(), body: body, cur: m.cursor, help: true}
}

// entryLines are one entry's fields, then its body.
func entryLines(e inbox.Entry, width int, at time.Time) []string {
	field := func(k, v string) []string {
		return view.WrapRest(k+strings.Repeat(" ", max(10-len(k), 1))+v, width, "", strings.Repeat(" ", 10))
	}
	state := e.State
	if e.Resolution != "" {
		state += " · " + e.Resolution
	}
	out := field("kind", view.DeliveryKind(e))
	out = append(out, field("state", state)...)
	out = append(out, field("from", e.Sender)...)
	out = append(out, field("to", e.Recipient()+recipientID(e))...)
	out = append(out, field("sent", e.When()+" ("+view.Age(e.When(), at)+" ago)")...)
	out = append(out, field("source", e.Source+" "+e.SourceID)...)
	if e.Reason != "" {
		out = append(out, field("reason", e.Reason)...)
	}
	if e.Body == "" {
		return append(out, "", "(no body kept)")
	}
	out = append(out, "")
	for _, line := range strings.Split(e.Body, "\n") {
		out = append(out, view.WrapRest(line, width, "", "")...)
	}
	return out
}

func recipientID(e inbox.Entry) string {
	if e.RecipientID == "" || e.RecipientID == e.Recipient() {
		return ""
	}
	return " (" + e.RecipientID + ")"
}

func (m *Model) viewDeliveryEntry() frame {
	e, ok := m.shownEntry()
	if !ok {
		return frame{s: scDeliveryEntry, top: m.deliveryTop(), cur: -1}
	}
	head := pyfmt.Clip("Delivery entry "+strconv.FormatInt(e.ID, 10), m.w)
	top := append([]section{{lines: []string{head}, style: &headerStyle}}, m.deliveryTop()[1:]...)
	return frame{s: scDeliveryEntry, top: top, body: []section{{lines: entryLines(e, m.w, m.now())}}, cur: -1, help: true}
}

// settleStatus is what the panel shows for a chief message: a dropped one,
// or a status poll that failed, is first checked against agent-manager's
// state.db and chief's transcript. A drop it cannot check, and no scan logged,
// is logged unverified with the chat's text.
func settleStatus(ib *inbox.Inbox, id string, chat int64, status, errText string) (string, string) {
	if status != "dropped" && errText == "" && status != "" {
		return status, errText
	}
	c, err := ib.CheckMessage(id)
	switch {
	case err != nil:
	case c.Status == "delivered" || c.Status == "false drop":
		return "delivered", ""
	case c.Status == inbox.KindLost:
		return inbox.StatusLost, ""
	case strings.HasPrefix(c.Status, "held: "):
		return c.Status, ""
	case status == "dropped" && chat != 0 && c.Entry == nil:
		why := c.Status
		if c.Why != "" {
			why = "unavailable: " + c.Why
		}
		if ct, err := ib.Chat(chat); err == nil {
			_ = ib.LogStatusDrop(id, "", am.ChiefName, ct.Text, why)
		}
	}
	return status, errText
}
