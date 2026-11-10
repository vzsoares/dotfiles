// Package tui is `zen-inbox watch`: a Bubble Tea panel with one row per
// asking session; a session's questions are walked, reviewed, then sent.
package tui

import (
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

const (
	PollEvery     = 2 * time.Second
	SessionsEvery = 10 * time.Second
	// FlashFor is how long a success flash stays; polls clear it.
	FlashFor = 6 * time.Second
)

type screen int

const (
	scList screen = iota
	scSession
	scWalk
	scFree
	scFull
	scReview
	scChange
	scTalkPick
	scTalkNote
)

// snapshot is one read of everything the panel shows.
type snapshot struct {
	open        []inbox.Decision
	held        []inbox.Decision
	undelivered []inbox.Batch
	byID        map[string]inbox.Decision
	sessions    am.Sessions
	refreshed   bool
	err         error
}

type tickMsg struct{}
type loadedMsg struct{ snap snapshot }
type sentMsg struct {
	recorded, amended, notes []string
	res                      *inbox.Delivery
	flash                    string
	snap                     snapshot
}
type talkMsg struct {
	ok   bool
	open []inbox.Decision
	snap snapshot
}
type resolvedMsg struct {
	resolved []string
	flash    string
	snap     snapshot
}
type retriedMsg struct {
	flash string
	ok    bool
	snap  snapshot
}

type entry struct {
	d      inbox.Decision
	staged *view.Staged
}

// pass is one walk through a session's questions; nothing in it reaches the
// db before Send.
type pass struct {
	queue    []string
	cur      string
	visited  []string
	staged   map[string]view.Staged
	notes    []string
	recorded []string
	gone     map[string]bool
	result   *inbox.Delivery
}

func (p *pass) stopped(how string) string {
	kept := "nothing recorded"
	if len(p.recorded) > 0 {
		kept = strings.Join(p.recorded, ", ") + " recorded earlier"
	}
	return strings.Join(append([]string{how + ": " + kept}, p.notes...), "; ")
}

func (p *pass) queued(id string) bool { return id == p.cur || slices.Contains(p.queue, id) }

func (p *pass) seen(id string) bool { return slices.Contains(p.visited, id) }

// Model is the panel. Every db read and subprocess runs in a tea.Cmd.
type Model struct {
	ib *inbox.Inbox
	am am.Client
	// after schedules msg; tests replace it to drive polls by hand.
	after func(time.Duration, tea.Msg) tea.Cmd
	now   func() time.Time

	w, h     int
	scr      screen
	showHelp bool
	busy     string
	sp       spinner.Model
	spinning bool // a spinner tick is in flight

	snap       snapshot
	loaded     bool
	sessionsAt time.Time
	seen       map[string]bool

	cursor  int
	scroll  int
	flash   string
	flashOK bool
	flashAt time.Time

	sid, sname string
	known      map[string]bool
	markers    []string

	p *pass

	ta textarea.Model
	ti textinput.Model
	vp viewport.Model

	talkFrom screen
	talkOpen []inbox.Decision
	marked   map[string]bool
}

func New(ib *inbox.Inbox, client am.Client) *Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Placeholder = "answer (empty cancels)"
	ti := textinput.New()
	ti.Placeholder = "note (optional)"
	// A blinking cursor keeps a timer running in Update; static is enough.
	ta.Cursor.SetMode(cursor.CursorStatic)
	ti.Cursor.SetMode(cursor.CursorStatic)
	return &Model{
		ib: ib, am: client, now: inbox.Now, ta: ta, ti: ti,
		sp: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		after: func(d time.Duration, msg tea.Msg) tea.Cmd {
			return tea.Tick(d, func(time.Time) tea.Msg { return msg })
		},
	}
}

// Watch runs the panel until the user quits.
func Watch(client am.Client) func(*inbox.Inbox) error {
	return func(ib *inbox.Inbox) error {
		_, err := tea.NewProgram(New(ib, client), tea.WithAltScreen()).Run()
		if errors.Is(err, tea.ErrInterrupted) {
			return nil
		}
		return err
	}
}

// ---------------------------------------------------------------- loading --

func load(ib *inbox.Inbox, client am.Client, ids []string, sessions am.Sessions, refresh bool) snapshot {
	s := snapshot{sessions: sessions, byID: map[string]inbox.Decision{}}
	if refresh {
		s.sessions, s.refreshed = client.Sessions(), true
	}
	if s.open, s.err = ib.Select("status = 'open'"); s.err != nil {
		return s
	}
	if s.held, s.err = ib.Select("delivery = 'held'"); s.err != nil {
		return s
	}
	if s.undelivered, s.err = ib.Undelivered(); s.err != nil {
		return s
	}
	for _, id := range ids {
		if d, err := ib.Get(id); err == nil {
			s.byID[id] = d
		}
	}
	return s
}

func (m *Model) watchedIDs() []string {
	set := map[string]bool{}
	for id := range m.known {
		set[id] = true
	}
	if m.p != nil {
		for _, id := range append(append([]string(nil), m.p.visited...), m.p.queue...) {
			set[id] = true
		}
	}
	for _, d := range m.talkOpen {
		set[d.ID] = true
	}
	return slices.Collect(maps.Keys(set))
}

func (m *Model) loadCmd(refresh bool) tea.Cmd {
	ib, client, ids, sessions := m.ib, m.am, m.watchedIDs(), m.snap.sessions
	return func() tea.Msg { return loadedMsg{load(ib, client, ids, sessions, refresh)} }
}

func (m *Model) Init() tea.Cmd { return m.loadCmd(true) }

// decision is the freshest copy of id: from the open list, else by id.
func (m *Model) decision(id string) (inbox.Decision, bool) {
	for _, d := range m.snap.open {
		if d.ID == id {
			return d, true
		}
	}
	d, ok := m.snap.byID[id]
	return d, ok
}

func (m *Model) sessionOpen() []inbox.Decision {
	var out []inbox.Decision
	for _, d := range m.snap.open {
		if d.SessionID == m.sid {
			out = append(out, d)
		}
	}
	return out
}

func (m *Model) apply(s snapshot) []tea.Cmd {
	if s.err != nil {
		m.flash = s.err.Error()
		return nil
	}
	m.snap, m.loaded = s, true
	if s.refreshed {
		m.sessionsAt = m.now()
	}
	if m.flashOK && m.now().Sub(m.flashAt) >= FlashFor {
		m.flash, m.flashOK = "", false
	}
	cmds := m.ring()
	// A send or talk in flight changes the db under the pass; its own reply
	// message reconciles the screen instead.
	switch {
	case m.busy != "":
	case m.scr == scSession:
		m.liveSession()
	case m.p != nil:
		m.livePass()
	}
	if n := m.menuLen(); n > 0 {
		m.cursor = min(m.cursor, n-1)
	}
	return cmds
}

// ring is one bell per session with decisions not seen before; the first
// read only learns what is already there.
func (m *Model) ring() []tea.Cmd {
	var cmds []tea.Cmd
	if m.seen != nil {
		for _, msg := range view.BellMessages(m.snap.open, m.seen) {
			client := m.am
			cmds = append(cmds, func() tea.Msg { client.Bell(msg); return nil })
		}
	} else {
		m.seen = map[string]bool{}
	}
	for _, d := range m.snap.open {
		m.seen[d.ID] = true
	}
	return cmds
}

// closedMarker says how a decision left the walk while it was on screen.
func closedMarker(d inbox.Decision) string {
	switch {
	case d.Status == "withdrawn":
		return d.ID + " withdrawn by " + d.SessionName
	case d.AnswerKind == "talk":
		return d.ID + " resolved in talk elsewhere"
	case d.AnswerKind == "pick":
		return d.ID + " answered elsewhere: " + d.AnswerOption + ": " + d.OptionText(d.AnswerOption)
	}
	return d.ID + " answered elsewhere: free text: " + d.AnswerText
}

func (m *Model) liveSession() {
	open := m.sessionOpen()
	isOpen := map[string]bool{}
	for _, d := range open {
		isOpen[d.ID] = true
	}
	for id, live := range m.known {
		if !live || isOpen[id] {
			continue
		}
		m.known[id] = false
		if d, ok := m.snap.byID[id]; ok {
			m.markers = append(m.markers, closedMarker(d))
		}
	}
	for id := range isOpen {
		if _, ok := m.known[id]; !ok {
			m.known[id] = true
		}
	}
	if len(open) == 0 {
		flash := "nothing open for that session"
		if len(m.markers) > 0 {
			flash = strings.Join(m.markers, "; ")
		}
		m.toList(flash)
	}
}

func (m *Model) livePass() {
	p := m.p
	curGone := false
	ids := append([]string(nil), p.visited...)
	for _, id := range ids {
		d, ok := m.decision(id)
		if !ok {
			continue
		}
		if d.Status != "open" {
			if !p.gone[id] {
				p.gone[id] = true
				delete(p.staged, id)
				p.notes = append(p.notes, closedMarker(d))
			}
			curGone = curGone || id == p.cur
			continue
		}
		if s, ok := p.staged[id]; ok && s.Rev != d.Rev {
			delete(p.staged, id)
			p.notes = append(p.notes, id+" amended; asked again")
			if !p.queued(id) {
				p.queue = append(p.queue, id)
			}
		}
	}
	for _, d := range m.sessionOpen() {
		if !p.seen(d.ID) && !p.queued(d.ID) {
			p.queue = append(p.queue, d.ID)
			p.notes = append(p.notes, d.ID+" asked; added to the walk")
		}
	}
	switch m.scr {
	case scWalk, scFree, scFull:
		if curGone {
			m.advance()
		}
	case scReview, scChange:
		if len(p.queue) > 0 || len(m.entries()) == 0 {
			m.advance()
		}
	}
}

// --------------------------------------------------------------- the pass --

func (m *Model) toList(flash string) {
	m.scr, m.p, m.flash, m.cursor, m.scroll = scList, nil, flash, 0, 0
	m.flashOK, m.flashAt = false, m.now()
	m.known, m.markers, m.talkOpen = nil, nil, nil
}

func (m *Model) enterSession(sid string) {
	m.sid, m.known, m.markers = sid, map[string]bool{}, nil
	open := m.sessionOpen()
	if len(open) == 0 {
		m.toList("nothing open for that session")
		return
	}
	m.sname = open[0].SessionName
	for _, d := range open {
		m.known[d.ID] = true
	}
	m.scr, m.cursor, m.scroll, m.flash = scSession, 0, 0, ""
}

func (m *Model) startPass() {
	m.p = &pass{staged: map[string]view.Staged{}, gone: map[string]bool{}}
	for _, d := range m.sessionOpen() {
		m.p.queue = append(m.p.queue, d.ID)
	}
	m.advance()
}

// advance moves to the next open question in the queue, else the review.
func (m *Model) advance() {
	p := m.p
	for len(p.queue) > 0 {
		id := p.queue[0]
		p.queue = p.queue[1:]
		d, ok := m.decision(id)
		if !ok || d.Status != "open" {
			if ok && !p.gone[id] {
				p.gone[id] = true
				delete(p.staged, id)
				p.notes = append(p.notes, id+" already "+d.Status)
			}
			continue
		}
		p.cur = id
		if !p.seen(id) {
			p.visited = append(p.visited, id)
		}
		m.scr, m.cursor, m.scroll = scWalk, 0, 0
		return
	}
	p.cur = ""
	if len(m.entries()) == 0 {
		flash := strings.Join(p.notes, "; ")
		if flash == "" {
			flash = "nothing open"
		}
		m.toList(flash)
		return
	}
	m.scr, m.cursor, m.scroll = scReview, 0, 0
}

func (m *Model) entries() []entry {
	var out []entry
	for _, id := range m.p.visited {
		d, ok := m.decision(id)
		if !ok || d.Status != "open" {
			continue
		}
		e := entry{d: d}
		if s, ok := m.p.staged[id]; ok {
			e.staged = &s
		}
		out = append(out, e)
	}
	return out
}

func (m *Model) stage(s view.Staged) {
	m.p.staged[m.p.cur] = s
	m.advance()
}

func sendingText(n int, name string) string {
	noun := " answers"
	if n == 1 {
		noun = " answer"
	}
	return "Sending " + strconv.Itoa(n) + noun + " to " + name + "…"
}

func (m *Model) sendCmd() tea.Cmd {
	byID := maps.Clone(m.p.staged)
	ids := slices.Collect(maps.Keys(byID))
	inbox.SortIDs(ids)
	ib, client, sid, name, prior := m.ib, m.am, m.sid, m.sname, m.p.result
	watched, sessions := m.watchedIDs(), m.snap.sessions
	return func() tea.Msg {
		msg := sentMsg{res: prior}
		for _, id := range ids {
			s := byID[id]
			var letter, text *string
			if s.Kind == "pick" {
				letter = &s.Option
			} else {
				text = &s.Text
			}
			rev := s.Rev
			res, err := ib.Answer(id, letter, text, "", &rev)
			var ie *inbox.Error
			switch {
			case err == nil:
				msg.recorded = append(msg.recorded, id)
				if res != nil {
					msg.res = res
				}
			case errors.As(err, &ie) && ie.Amended:
				msg.amended = append(msg.amended, id)
			default:
				status := "closed"
				if d, gerr := ib.Get(id); gerr == nil {
					status = d.Status
				}
				msg.notes = append(msg.notes, id+" already "+status)
			}
		}
		if flash, err := ib.Describe(msg.res, sid, name); err == nil {
			msg.flash = flash
		} else {
			msg.flash = err.Error()
		}
		msg.snap = load(ib, client, watched, sessions, false)
		return msg
	}
}

func (m *Model) onSent(msg sentMsg) {
	p := m.p
	m.busy = ""
	m.snap = msg.snap
	ok := msg.res != nil && msg.res.OK
	if p == nil {
		m.toList(msg.flash)
		m.flashOK = ok
		return
	}
	p.staged = map[string]view.Staged{}
	p.recorded = append(p.recorded, msg.recorded...)
	p.notes = append(p.notes, msg.notes...)
	p.result = msg.res
	for _, id := range msg.recorded {
		p.gone[id] = true
	}
	if len(msg.amended) > 0 {
		p.notes = append(p.notes, strings.Join(msg.amended, ", ")+" amended; shown again")
		p.queue = append(msg.amended, p.queue...)
		m.advance()
		return
	}
	m.toList(strings.Join(append([]string{msg.flash}, p.notes...), "; "))
	m.flashOK = ok
}

// ------------------------------------------------------------------- talk --

func (m *Model) startTalk(from screen) tea.Cmd {
	m.talkFrom, m.busy = from, "Talking to "+m.sname+"…"
	ib, client, sid, watched, sessions := m.ib, m.am, m.sid, m.watchedIDs(), m.snap.sessions
	return func() tea.Msg {
		if !client.Popup(sid) {
			return talkMsg{snap: load(ib, client, watched, sessions, false)}
		}
		open, _ := ib.SessionOpen(sid)
		return talkMsg{ok: true, open: open, snap: load(ib, client, watched, sessions, false)}
	}
}

func (m *Model) onTalk(msg talkMsg) tea.Cmd {
	m.busy = ""
	m.snap = msg.snap
	switch {
	case !msg.ok:
		m.finishTalk(nil, "talk unavailable; attach with: "+am.AttachCommand(m.sid))
	case len(msg.open) == 0:
		m.finishTalk(nil, "nothing open")
	default:
		m.talkOpen, m.marked = msg.open, map[string]bool{}
		m.scr, m.cursor = scTalkPick, 0
	}
	return nil
}

func (m *Model) chosen() []string {
	var out []string
	for _, d := range m.talkOpen {
		if m.marked[d.ID] {
			out = append(out, d.ID)
		}
	}
	return out
}

func (m *Model) resolveCmd(note string) tea.Cmd {
	m.busy = "Resolving…"
	ib, client, sid, name, ids := m.ib, m.am, m.sid, m.sname, m.chosen()
	watched, sessions := m.watchedIDs(), m.snap.sessions
	return func() tea.Msg {
		var resolved, notes []string
		var result *inbox.Delivery
		for _, id := range ids {
			res, err := ib.Resolve(id, note)
			if err != nil {
				notes = append(notes, err.Error())
				continue
			}
			resolved = append(resolved, id)
			if res != nil {
				result = res
			}
		}
		var flash []string
		if len(resolved) > 0 {
			flash = append(flash, "resolved "+strings.Join(resolved, ", "))
		}
		if result != nil {
			if d, err := ib.Describe(result, sid, name); err == nil {
				flash = append(flash, d)
			}
		}
		return resolvedMsg{resolved, strings.Join(append(flash, notes...), "; "),
			load(ib, client, watched, sessions, false)}
	}
}

func (m *Model) finishTalk(resolved []string, flash string) {
	m.talkOpen = nil
	if m.talkFrom == scSession || m.p == nil {
		m.toList(flash)
		return
	}
	p := m.p
	for _, id := range resolved {
		delete(p.staged, id)
		p.gone[id] = true
	}
	if flash != "" {
		p.notes = append(p.notes, flash)
	}
	for _, id := range resolved {
		if id == p.cur {
			m.advance()
			return
		}
	}
	m.scr = scWalk
}

// ----------------------------------------------------------------- update --

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.update(msg)
	if m.busy != "" && !m.spinning {
		m.spinning = true
		cmd = tea.Batch(cmd, m.sp.Tick)
	}
	return m, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.ta.SetWidth(max(m.w, 1))
		m.ta.SetHeight(max(m.rows()-4, 1))
		m.ti.Width = max(m.w-3, 1)
		m.vp.Width, m.vp.Height = m.w, max(m.rows()-m.footerRows(scFull), 1)
		return m, nil
	case spinner.TickMsg:
		if m.busy == "" {
			m.spinning = false
			return m, nil
		}
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(msg)
		return m, cmd
	case tickMsg:
		return m, m.loadCmd(m.now().Sub(m.sessionsAt) >= SessionsEvery)
	case loadedMsg:
		cmds := m.apply(msg.snap)
		return m, tea.Batch(append(cmds, m.after(PollEvery, tickMsg{}))...)
	case sentMsg:
		m.onSent(msg)
		return m, nil
	case talkMsg:
		return m, m.onTalk(msg)
	case resolvedMsg:
		m.busy, m.snap = "", msg.snap
		m.finishTalk(msg.resolved, msg.flash)
		return m, nil
	case retriedMsg:
		m.busy, m.snap = "", msg.snap
		m.toList(msg.flash)
		m.flashOK = msg.ok
		return m, nil
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) menuLen() int {
	switch m.scr {
	case scList:
		return len(m.listItems())
	case scSession:
		return len(sessionMenu)
	case scWalk:
		if d, ok := m.decision(m.p.cur); ok {
			return len(questionMenu(d))
		}
	case scReview:
		return len(reviewMenu)
	case scChange:
		return len(m.entries()) + 1
	case scTalkPick:
		return len(m.talkOpen)
	}
	return 0
}

var sessionMenu = []item{{"Answer", "answer"}, {"Talk", "talk"}, {"Back", "back"}}
var reviewMenu = []item{{"Send", "send"}, {"Change one", "change"}, {"Cancel", "cancel"}}

func (m *Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		return m, tea.Quit
	}
	if m.busy != "" {
		return m, nil
	}
	switch m.scr {
	case scFree:
		return m.keyFree(msg)
	case scFull:
		return m.keyFull(msg)
	case scTalkNote:
		return m.keyNote(msg)
	}
	n := m.menuLen()
	switch k {
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
		return m, nil
	case "down", "j":
		m.cursor = min(m.cursor+1, max(n-1, 0))
		return m, nil
	case "pgdown", "ctrl+f":
		m.scroll += max(m.h/2, 1)
		return m, nil
	case "pgup", "ctrl+b":
		m.scroll = max(m.scroll-max(m.h/2, 1), 0)
		return m, nil
	case "?":
		m.showHelp = !m.showHelp
		return m, nil
	}
	switch m.scr {
	case scList:
		return m.keyList(k)
	case scSession:
		return m.keySession(k)
	case scWalk:
		return m.keyWalk(k)
	case scReview:
		return m.keyReview(k)
	case scChange:
		return m.keyChange(k)
	case scTalkPick:
		return m.keyTalkPick(k)
	}
	return m, nil
}

func (m *Model) listItems() []item {
	room := max(m.w, view.MinWidth) - 2
	var out []item
	at := m.now()
	for _, r := range view.Rows(m.snap.open, m.snap.sessions) {
		out = append(out, item{pyfmt.Clip(view.RowLabel(r, at), room), "s:" + r.SessionID})
	}
	for _, b := range m.snap.undelivered {
		label := "retry " + b.Decisions[0].SessionName + ": " + strconv.Itoa(len(b.Decisions)) + " answer(s)"
		out = append(out, item{pyfmt.Clip(label, room), "r:" + b.SessionID})
	}
	return out
}

func (m *Model) undeliveredName(sid string) string {
	for _, b := range m.snap.undelivered {
		if b.SessionID == sid {
			return b.Decisions[0].SessionName
		}
	}
	return sid
}

func (m *Model) keyList(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "q", "esc":
		return m, tea.Quit
	case "r":
		if len(m.snap.undelivered) > 0 {
			return m, m.retryCmd(m.snap.undelivered[0].SessionID)
		}
	case "enter":
		items := m.listItems()
		if m.cursor >= len(items) {
			return m, nil
		}
		kind, sid, _ := strings.Cut(items[m.cursor].value, ":")
		if kind == "r" {
			return m, m.retryCmd(sid)
		}
		m.enterSession(sid)
	}
	return m, nil
}

func (m *Model) retryCmd(sid string) tea.Cmd {
	m.busy = "Retrying delivery to " + m.undeliveredName(sid) + "…"
	ib, client, sessions := m.ib, m.am, m.snap.sessions
	return func() tea.Msg {
		res, err := ib.Retry(sid)
		flash := ""
		if err != nil {
			flash = err.Error()
		} else if flash, err = ib.Describe(res, sid, ""); err != nil {
			flash = err.Error()
		}
		ok := err == nil && res != nil && res.OK
		return retriedMsg{flash, ok, load(ib, client, nil, sessions, false)}
	}
}

func (m *Model) keySession(k string) (tea.Model, tea.Cmd) {
	choice := ""
	switch k {
	case "enter":
		choice = sessionMenu[min(m.cursor, len(sessionMenu)-1)].value
	case "t":
		choice = "talk"
	case "esc":
		choice = "back"
	}
	switch choice {
	case "answer":
		m.startPass()
	case "talk":
		return m, m.startTalk(scSession)
	case "back":
		m.toList("")
	}
	return m, nil
}

func (m *Model) keyWalk(k string) (tea.Model, tea.Cmd) {
	d, ok := m.decision(m.p.cur)
	if !ok {
		return m, nil
	}
	choice := ""
	switch k {
	case "enter":
		items := questionMenu(d)
		choice = items[min(m.cursor, len(items)-1)].value
	case "a", "b", "c", "d":
		choice = k
	case "f":
		choice = "free"
	case "s":
		choice = "skip"
	case "t":
		choice = "talk"
	case "v":
		choice = "full"
	case "esc":
		choice = "back"
	}
	switch choice {
	case "":
	case "back":
		m.toList(m.p.stopped("back"))
	case "skip":
		delete(m.p.staged, d.ID)
		m.advance()
	case "free":
		m.ta.Reset()
		m.scr = scFree
		return m, m.ta.Focus()
	case "full":
		m.vp.SetContent(strings.Join(m.fullLines(d), "\n"))
		m.vp.GotoTop()
		m.scr = scFull
	case "talk":
		return m, m.startTalk(scWalk)
	default:
		if d.OptionText(choice) != "" {
			m.stage(view.Staged{Kind: "pick", Option: choice, Rev: d.Rev})
		}
	}
	return m, nil
}

func (m *Model) fullLines(d inbox.Decision) []string {
	var out []string
	for _, line := range strings.Split(view.FullText(d, m.brief(d)), "\n") {
		out = append(out, view.Wrap(line, m.w, "")...)
	}
	return out
}

func (m *Model) brief(d inbox.Decision) view.Brief {
	return view.BriefsFor([]inbox.Decision{d}, m.snap.sessions, m.now())[0]
}

func (m *Model) keyFree(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.scr = scWalk
		return m, nil
	case "ctrl+d":
		text := m.ta.Value()
		d, ok := m.decision(m.p.cur)
		if !ok || pyfmt.Blank(text) {
			m.scr = scWalk
			return m, nil
		}
		m.stage(view.Staged{Kind: "text", Text: text, Rev: d.Rev})
		return m, nil
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

func (m *Model) keyFull(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "v":
		m.scr = scWalk
		return m, nil
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m *Model) keyReview(k string) (tea.Model, tea.Cmd) {
	choice := ""
	switch k {
	case "enter":
		choice = reviewMenu[min(m.cursor, len(reviewMenu)-1)].value
	case "esc":
		choice = "cancel"
	}
	switch choice {
	case "send":
		m.busy = sendingText(len(m.p.staged), m.sname)
		return m, m.sendCmd()
	case "change":
		m.scr, m.cursor = scChange, 0
	case "cancel":
		m.toList(m.p.stopped("cancelled"))
	}
	return m, nil
}

func (m *Model) keyChange(k string) (tea.Model, tea.Cmd) {
	entries := m.entries()
	switch {
	case k == "esc" || (k == "enter" && m.cursor >= len(entries)):
		m.scr, m.cursor = scReview, 0
	case k == "enter":
		m.p.queue = append([]string{entries[m.cursor].d.ID}, m.p.queue...)
		m.advance()
	}
	return m, nil
}

func (m *Model) keyTalkPick(k string) (tea.Model, tea.Cmd) {
	switch k {
	case " ", "space":
		if m.cursor < len(m.talkOpen) {
			id := m.talkOpen[m.cursor].ID
			m.marked[id] = !m.marked[id]
		}
	case "enter":
		if len(m.chosen()) == 0 {
			m.finishTalk(nil, "all left open")
			return m, nil
		}
		m.ti.Reset()
		m.scr = scTalkNote
		return m, m.ti.Focus()
	case "esc":
		m.finishTalk(nil, "all left open")
	}
	return m, nil
}

func (m *Model) keyNote(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		return m, m.resolveCmd(m.ti.Value())
	case "esc":
		return m, m.resolveCmd("")
	}
	var cmd tea.Cmd
	m.ti, cmd = m.ti.Update(msg)
	return m, cmd
}
