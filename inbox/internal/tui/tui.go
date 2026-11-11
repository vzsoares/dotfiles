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
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/guard"
	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/md"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

const (
	PollEvery     = 2 * time.Second
	SessionsEvery = 10 * time.Second
	// FlashFor is how long a success flash stays; polls clear it.
	FlashFor = 6 * time.Second

	freePlaceholder = "answer (empty cancels)"
)

type screen int

const (
	scList screen = iota
	scSession
	scWalk
	scFree
	scFull
	scReview
	scJump
	scTalkPick
	scTalkNote
	scConfirm
	// scLocked and scFreeBlank are never m.scr: they name the footer of a walk
	// on a locked prod ask and of an empty free-text box.
	scLocked
	scFreeBlank
	scNote
	scReply
	scChief
	// scListNotes names the list's footer and help while notes are shown.
	scListNotes
)

// snapshot is one read of everything the panel shows.
type snapshot struct {
	open        []inbox.Decision
	held        []inbox.Decision
	undelivered []inbox.Batch
	byID        map[string]inbox.Decision
	sessions    am.Sessions
	drafts      map[string]inbox.Draft
	notes       []inbox.Note
	failedNotes []inbox.Note
	// gen is the panel's draftGen when the read started.
	gen       int
	refreshed bool
	err       error
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
type draftsSavedMsg struct{ err error }

// draftOp is one queued draft write: s saved for id, nil s deleting it, or
// every draft of session clear dropped.
type draftOp struct {
	id    string
	s     *view.Staged
	clear string
}

type entry struct {
	d      inbox.Decision
	staged *view.Staged
}

// pass is one walk through a session's questions in order; staged answers
// are kept as drafts and recorded only at Send.
type pass struct {
	order    []string
	cur      string
	staged   map[string]view.Staged
	notes    []string
	recorded []string
	gone     map[string]bool
	result   *inbox.Delivery
}

// stopped is the flash for leaving the pass; drafts lists what is saved.
func (p *pass) stopped(how string, drafts map[string]view.Staged) string {
	kept := "nothing recorded"
	if len(p.recorded) > 0 {
		kept = strings.Join(p.recorded, ", ") + " recorded earlier"
	}
	for id := range p.staged {
		if _, ok := drafts[id]; ok {
			kept += ", drafts kept"
			break
		}
	}
	return strings.Join(append([]string{how + ": " + kept}, p.notes...), "; ")
}

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

	cursor int
	scroll int
	// walkScroll is the walk's offset kept while View full or Free text is open.
	walkScroll int
	// shown is how many body rows the last layout fitted; a page is half.
	shown   int
	flash   string
	flashOK bool
	flashAt time.Time

	sid, sname string
	known      map[string]bool
	markers    []string

	p *pass
	// drafts mirrors the db's drafts; draftGen counts local changes so a
	// poll read before one cannot overwrite it.
	drafts   map[string]view.Staged
	draftGen int
	draftOps []draftOp
	saving   bool
	quitting bool
	jumpFrom screen

	ta textarea.Model
	ti textinput.Model
	// freeID and freeRev name the question revision the free-text box was typed against.
	freeID  string
	freeRev int64

	// md takes its colour profile from a lipgloss renderer; tests inject one.
	md *md.Renderer

	talkFrom screen
	talkOpen []inbox.Decision
	marked   map[string]bool

	// prodBlock is why prod-action asks are locked here; it applies only
	// while ib.User is false.
	prodBlock string
	// confirm is the Approve letter scConfirm stages on y.
	confirm string
	// note is the one scNote and scReply show.
	note inbox.Note
	// chiefTA is the compose box for chief; its text stays across esc.
	chiefTA textarea.Model
	chief   chiefMsg
	// replyFor is the note the textarea holds a reply for.
	replyFor int64
}

func New(ib *inbox.Inbox, client am.Client) *Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Placeholder = freePlaceholder
	ti := textinput.New()
	ti.Placeholder = "note (optional)"
	// A blinking cursor keeps a timer running in Update; static is enough.
	ta.Cursor.SetMode(cursor.CursorStatic)
	ti.Cursor.SetMode(cursor.CursorStatic)
	return &Model{
		ib: ib, am: client, now: inbox.Now, ta: ta, ti: ti, chiefTA: newChiefBox(), md: md.New(lipgloss.DefaultRenderer()),
		prodBlock: "guard not run", drafts: map[string]view.Staged{},
		sp: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		after: func(d time.Duration, msg tea.Msg) tea.Cmd {
			return tea.Tick(d, func(time.Time) tea.Msg { return msg })
		},
	}
}

// Watch runs the panel until the user quits.
func Watch(client am.Client) func(*inbox.Inbox) error {
	return func(ib *inbox.Inbox) error {
		ok, reason := guard.Current()
		ib.User = ok
		m := New(ib, client)
		m.prodBlock = reason
		_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
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
	if s.drafts, s.err = ib.Drafts(); s.err != nil {
		return s
	}
	if s.notes, s.err = ib.Notes(false); s.err != nil {
		return s
	}
	if s.failedNotes, s.err = ib.FailedNotes(); s.err != nil {
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
		for _, id := range m.p.order {
			set[id] = true
		}
	}
	for _, d := range m.talkOpen {
		set[d.ID] = true
	}
	return slices.Collect(maps.Keys(set))
}

func (m *Model) loadCmd(refresh bool) tea.Cmd {
	ib, client, ids, sessions, gen := m.ib, m.am, m.watchedIDs(), m.snap.sessions, m.draftGen
	return func() tea.Msg {
		s := load(ib, client, ids, sessions, refresh)
		s.gen = gen
		return loadedMsg{s}
	}
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
	m.refreshNote()
	if s.gen == m.draftGen && !m.saving && len(m.draftOps) == 0 {
		m.drafts = map[string]view.Staged{}
		for id, d := range s.drafts {
			m.drafts[id] = view.Staged{Kind: d.Kind, Option: d.Option, Text: d.Text, Rev: d.Rev}
		}
	}
	if s.refreshed {
		m.sessionsAt = m.now()
	}
	if m.flashOK && m.now().Sub(m.flashAt) >= FlashFor {
		m.flash, m.flashOK = "", false
	}
	cmds := m.ring()
	m.expireChief()
	if c := m.chiefPoll(); c != nil {
		cmds = append(cmds, c)
	}
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

// ring is one banner per decision or unread note not seen before; the first
// read only learns what is already there.
func (m *Model) ring() []tea.Cmd {
	var cmds []tea.Cmd
	if m.seen != nil {
		for _, n := range view.Notices(m.snap.open, m.snap.notes, m.seen) {
			client := m.am
			cmds = append(cmds, func() tea.Msg { client.Notify(n.Title, n.Body); return nil })
		}
	} else {
		m.seen = map[string]bool{}
	}
	for _, d := range m.snap.open {
		m.seen[d.ID] = true
	}
	for _, n := range m.snap.notes {
		m.seen[n.Label()] = true
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
	for _, id := range p.order {
		d, ok := m.decision(id)
		if !ok {
			continue
		}
		if d.Status != "open" {
			if !p.gone[id] {
				p.gone[id] = true
				m.unstage(id)
				p.notes = append(p.notes, closedMarker(d))
			}
			curGone = curGone || id == p.cur
			continue
		}
		if s, ok := p.staged[id]; ok && s.Rev != d.Rev {
			m.unstage(id)
			p.notes = append(p.notes, id+" amended; asked again")
		}
	}
	for _, d := range m.sessionOpen() {
		if !slices.Contains(p.order, d.ID) {
			p.order = append(p.order, d.ID)
			p.notes = append(p.notes, d.ID+" asked; added to the walk")
		}
	}
	switch m.scr {
	case scWalk, scFree, scFull, scConfirm:
		if curGone {
			m.advance()
		}
	case scReview, scJump:
		fromWalk := m.scr == scJump && m.jumpFrom == scWalk
		if len(m.entries()) == 0 || (!fromWalk && m.next() != "") {
			m.advance()
		}
	}
}

// unstage forgets id's staged answer; the store already dropped its draft.
func (m *Model) unstage(id string) {
	delete(m.p.staged, id)
	delete(m.drafts, id)
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

// startPass resumes from the session's drafts at the first undrafted question.
func (m *Model) startPass() {
	m.p = &pass{staged: map[string]view.Staged{}, gone: map[string]bool{}}
	for _, d := range m.sessionOpen() {
		m.p.order = append(m.p.order, d.ID)
		if s, ok := m.drafts[d.ID]; ok && s.Rev == d.Rev {
			m.p.staged[d.ID] = s
		}
	}
	m.advance()
}

// next is the first open question of the pass with nothing staged, or "".
func (m *Model) next() string {
	p := m.p
	for _, id := range p.order {
		if _, ok := p.staged[id]; ok || p.gone[id] {
			continue
		}
		if d, ok := m.decision(id); ok && d.Status == "open" {
			return id
		}
	}
	return ""
}

// advance shows the first undrafted question, else the review.
func (m *Model) advance() {
	p := m.p
	for _, id := range p.order {
		d, ok := m.decision(id)
		if ok && d.Status != "open" && !p.gone[id] {
			p.gone[id] = true
			m.unstage(id)
			p.notes = append(p.notes, id+" already "+d.Status)
		}
	}
	if id := m.next(); id != "" {
		m.show(id)
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

// show puts id on screen with the cursor on its staged answer.
func (m *Model) show(id string) {
	m.p.cur = id
	m.scr, m.cursor, m.scroll = scWalk, 0, 0
	d, ok := m.decision(id)
	s, staged := m.p.staged[id]
	if !ok || !staged {
		return
	}
	want := s.Option
	switch s.Kind {
	case "text":
		want = "free"
	case "skip":
		want = "skip"
	}
	for i, it := range questionMenu(d, m.locked(d)) {
		if it.value == want {
			m.cursor = i
		}
	}
}

// step shows the open question dir places from the current one (the review
// counts as past the last); false when there is none.
func (m *Model) step(dir int) bool {
	p := m.p
	i := slices.Index(p.order, p.cur)
	if i < 0 {
		i = len(p.order)
	}
	for i += dir; i >= 0 && i < len(p.order); i += dir {
		id := p.order[i]
		if d, ok := m.decision(id); ok && d.Status == "open" && !p.gone[id] {
			m.show(id)
			return true
		}
	}
	return false
}

func (m *Model) entries() []entry {
	var out []entry
	for _, id := range m.p.order {
		d, ok := m.decision(id)
		if !ok || d.Status != "open" || m.p.gone[id] {
			continue
		}
		e := entry{d: d}
		if s, ok := m.p.staged[id]; ok && s.Kind != "skip" {
			e.staged = &s
		}
		out = append(out, e)
	}
	return out
}

func (m *Model) stage(s view.Staged) tea.Cmd {
	id := m.p.cur
	m.p.staged[id] = s
	m.advance()
	return m.saveDraft(id, s)
}

// saveDraft mirrors s as id's draft and queues its write. A prod Approve is
// kept only in the pass, so a restart asks it again with its confirm.
func (m *Model) saveDraft(id string, s view.Staged) tea.Cmd {
	if d, ok := m.decision(id); ok && d.Prod != nil && s.Kind == "pick" && d.OptionText(s.Option) == "Approve" {
		delete(m.drafts, id)
		return m.queueDrafts(draftOp{id: id})
	}
	m.drafts[id] = s
	return m.queueDrafts(draftOp{id: id, s: &s})
}

func (m *Model) queueDrafts(ops ...draftOp) tea.Cmd {
	m.draftGen++
	m.draftOps = append(m.draftOps, ops...)
	return m.writeDrafts()
}

// writeDrafts runs the queued writes; one batch at a time, so they land in
// the order they were made.
func (m *Model) writeDrafts() tea.Cmd {
	if m.saving || len(m.draftOps) == 0 {
		return nil
	}
	ops, ib := m.draftOps, m.ib
	m.draftOps, m.saving = nil, true
	return func() tea.Msg {
		for _, op := range ops {
			var err error
			switch {
			case op.clear != "":
				err = ib.ClearDrafts(op.clear)
			case op.s == nil:
				err = ib.DeleteDraft(op.id)
			default:
				err = ib.SaveDraft(inbox.Draft{ID: op.id, Kind: op.s.Kind, Option: op.s.Option, Text: op.s.Text, Rev: op.s.Rev})
			}
			if err != nil {
				return draftsSavedMsg{err}
			}
		}
		return draftsSavedMsg{}
	}
}

// quit leaves once every queued draft write has landed; a second ctrl+c
// leaves at once.
func (m *Model) quit() tea.Cmd {
	if !m.saving && len(m.draftOps) == 0 {
		return tea.Quit
	}
	m.quitting = true
	return m.writeDrafts()
}

func (m *Model) onDraftsSaved(msg draftsSavedMsg) tea.Cmd {
	// A poll started while the write ran may have read the db before it.
	m.saving = false
	m.draftGen++
	if msg.err != nil {
		note := "draft not saved: " + msg.err.Error()
		if m.p != nil {
			m.p.notes = append(m.p.notes, note)
		} else {
			m.flash, m.flashOK = note, false
		}
	}
	if msg.err != nil {
		m.quitting = false
	}
	if cmd := m.writeDrafts(); cmd != nil || !m.quitting {
		return cmd
	}
	return tea.Quit
}

func (p *pass) answers() int {
	n := 0
	for _, s := range p.staged {
		if s.Kind != "skip" {
			n++
		}
	}
	return n
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
			if s.Kind == "skip" {
				continue
			}
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

func (m *Model) onSent(msg sentMsg) tea.Cmd {
	p := m.p
	m.busy = ""
	m.snap = msg.snap
	ok := msg.res != nil && msg.res.OK
	if p == nil {
		m.toList(msg.flash)
		m.flashOK = ok
		return nil
	}
	for id, s := range p.staged {
		if s.Kind != "skip" {
			delete(p.staged, id)
		}
	}
	p.recorded = append(p.recorded, msg.recorded...)
	p.notes = append(p.notes, msg.notes...)
	p.result = msg.res
	for _, id := range msg.recorded {
		p.gone[id] = true
	}
	if len(msg.amended) > 0 {
		p.notes = append(p.notes, strings.Join(msg.amended, ", ")+" amended; shown again")
		m.advance()
		return nil
	}
	for _, id := range p.order {
		delete(m.drafts, id)
	}
	m.toList(strings.Join(append([]string{msg.flash}, p.notes...), "; "))
	m.flashOK = ok
	return m.queueDrafts(draftOp{clear: m.sid})
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
	msg.open = slices.DeleteFunc(msg.open, m.locked)
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

// locked is a prod-action ask this panel may not answer.
func (m *Model) locked(d inbox.Decision) bool { return d.Prod != nil && !m.ib.User }

// notice is the locked prod ask's line on its question screen.
func (m *Model) notice(d inbox.Decision) string {
	if !m.locked(d) {
		return ""
	}
	return "prod approvals only from the decisions terminal (" + m.prodBlock + ")"
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
		m.unstage(id)
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
	scr, cursor := m.scr, m.cursor
	_, cmd := m.update(msg)
	switch {
	case m.scr == scr:
	case scr == scWalk:
		m.walkScroll, m.scroll = m.scroll, 0
	case m.scr == scWalk && (scr == scFull || scr == scFree || scr == scConfirm):
		m.scroll = m.walkScroll
	default:
		m.scroll = 0
	}
	if m.w > 0 && m.h > 0 && m.busy == "" {
		_, m.scroll, m.shown = m.layout(m.scr != scr || m.cursor != cursor)
	}
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
		m.chiefTA.SetWidth(max(m.w, 1))
		m.chiefTA.SetHeight(max(m.rows()-4, 1))
		m.ti.Width = max(m.w-3, 1)
		return m, nil
	case tea.MouseMsg:
		if m.busy == "" && msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.scroll = max(m.scroll-3, 0)
			case tea.MouseButtonWheelDown:
				m.scroll += 3
			}
		}
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
		return m, m.onSent(msg)
	case draftsSavedMsg:
		return m, m.onDraftsSaved(msg)
	case talkMsg:
		return m, m.onTalk(msg)
	case resolvedMsg:
		m.busy, m.snap = "", msg.snap
		m.finishTalk(msg.resolved, msg.flash)
		return m, nil
	case chiefSentMsg:
		return m, m.onChiefSent(msg)
	case chiefStatusMsg:
		m.onChiefStatus(msg)
		return m, nil
	case notesMsg:
		m.onNotes(msg)
		return m, nil
	case noteSentMsg:
		m.onNoteSent(msg)
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
			return len(questionMenu(d, m.locked(d)))
		}
	case scReview:
		return len(reviewMenu)
	case scJump:
		return len(m.jumpItems())
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
		if m.quitting {
			return m, tea.Quit
		}
		return m, m.quit()
	}
	if m.busy != "" {
		return m, nil
	}
	if m.scr == scConfirm {
		return m.keyConfirm(k)
	}
	if n := m.scrollBy(k); n != 0 {
		m.scroll = max(m.scroll+n, 0)
		return m, nil
	}
	switch m.scr {
	case scFree:
		return m.keyFree(msg)
	case scFull:
		return m.keyFull(msg)
	case scTalkNote:
		return m.keyNote(msg)
	case scReply:
		return m.keyReply(msg)
	case scChief:
		return m.keyChief(msg)
	}
	n := m.menuLen()
	switch k {
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
		return m, nil
	case "down", "j":
		m.cursor = min(m.cursor+1, max(n-1, 0))
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
	case scJump:
		return m.keyJump(k)
	case scTalkPick:
		return m.keyTalkPick(k)
	case scNote:
		return m.keyNoteScreen(k)
	}
	return m, nil
}

// scrollBy is how far k scrolls the body, 0 when k does not scroll here.
func (m *Model) scrollBy(k string) int {
	half := max(m.shown/2, 1)
	switch k {
	case "pgdown":
		return half
	case "pgup":
		return -half
	}
	if m.scr == scFree || m.scr == scTalkNote || m.scr == scReply || m.scr == scChief {
		return 0
	}
	switch k {
	case "ctrl+d", "ctrl+f":
		return half
	case "ctrl+u", "ctrl+b":
		return -half
	}
	if m.scr == scFull || m.scr == scNote {
		switch k {
		case "down", "j":
			return 1
		case "up", "k":
			return -1
		}
	}
	return 0
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
	for _, n := range m.snap.failedNotes {
		out = append(out, item{pyfmt.Clip("retry reply "+n.Label(), room), "nr:" + noteValue(n)})
	}
	return append(out, m.noteItems(room)...)
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
		return m, m.quit()
	case "r":
		return m, m.retryFirst()
	case "c":
		return m, m.openChief()
	case "m":
		if n, ok := m.cursorNote(); ok {
			return m, m.markCmd(n.ID, false, n.Unread())
		}
	case "M":
		if inbox.Unread(m.snap.notes) > 0 {
			return m, m.markCmd(0, true, true)
		}
	case "enter":
		items := m.listItems()
		if m.cursor >= len(items) {
			return m, nil
		}
		kind, sid, _ := strings.Cut(items[m.cursor].value, ":")
		switch kind {
		case "r":
			return m, m.retryCmd(sid)
		case "nr":
			id, _ := strconv.ParseInt(sid, 10, 64)
			return m, m.retryNoteCmd(id)
		case "n":
			m.openNote(sid)
			return m, nil
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
		items := questionMenu(d, m.locked(d))
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
	case "left", "shift+tab":
		m.step(-1)
	case "right":
		if !m.step(1) && m.next() == "" {
			m.advance()
		}
	case "l":
		m.openJump()
	}
	switch choice {
	case "":
	case "back":
		m.toList(m.p.stopped("back", m.drafts))
	case "skip":
		return m, m.stage(view.Staged{Kind: "skip", Rev: d.Rev})
	case "free":
		if m.locked(d) {
			return m, nil
		}
		m.ta.Reset()
		m.ta.Placeholder = freePlaceholder
		m.replyFor = 0
		m.freeID, m.freeRev = d.ID, d.Rev
		if s, ok := m.p.staged[d.ID]; ok && s.Kind == "text" {
			m.ta.SetValue(s.Text)
			m.freeRev = s.Rev
		}
		m.flash = ""
		m.scr = scFree
		return m, m.ta.Focus()
	case "full":
		m.scr = scFull
	case "talk":
		return m, m.startTalk(scWalk)
	default:
		switch {
		case m.locked(d) || d.OptionText(choice) == "":
		case d.Prod != nil && d.OptionText(choice) == "Approve":
			m.confirm = choice
			m.scr = scConfirm
		default:
			return m, m.stage(view.Staged{Kind: "pick", Option: choice, Rev: d.Rev})
		}
	}
	return m, nil
}

// keyConfirm stages Approve only on y; any other key goes back unstaged.
func (m *Model) keyConfirm(k string) (tea.Model, tea.Cmd) {
	d, ok := m.decision(m.p.cur)
	m.scr = scWalk
	if ok && k == "y" && !m.locked(d) && d.OptionText(m.confirm) == "Approve" {
		return m, m.stage(view.Staged{Kind: "pick", Option: m.confirm, Rev: d.Rev})
	}
	return m, nil
}

// fullLines is View full: the context block, then the question, its context
// and options, nothing cut.
func (m *Model) fullLines(d inbox.Decision) []string {
	out := view.PanelContextBlock(m.brief(d), m.w, m.paint)
	return append(append(out, ""), view.PanelDecisionLines(d, m.w, m.paint)...)
}

func (m *Model) brief(d inbox.Decision) view.Brief {
	return view.BriefsFor([]inbox.Decision{d}, m.snap.sessions, m.now())[0]
}

func (m *Model) keyFree(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, m.saveFree(false)
	case "ctrl+d":
		return m, m.saveFree(true)
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

// saveFree leaves the free-text box: send stages the text and advances, else it
// is kept as a draft. A blank box saves nothing. A different or amended question
// than the one the text was typed against keeps the box open until the next press.
func (m *Model) saveFree(send bool) tea.Cmd {
	d, ok := m.decision(m.p.cur)
	text := m.ta.Value()
	if !ok || pyfmt.Blank(text) {
		m.scr, m.flash = scWalk, ""
		return nil
	}
	if d.ID != m.freeID || d.Rev != m.freeRev {
		m.freeID, m.freeRev = d.ID, d.Rev
		m.flash, m.flashOK = "question changed; review before saving", false
		return nil
	}
	m.flash = ""
	s := view.Staged{Kind: "text", Text: text, Rev: d.Rev}
	if send {
		return m.stage(s)
	}
	m.scr = scWalk
	m.p.staged[d.ID] = s
	return m.saveDraft(d.ID, s)
}

func (m *Model) keyFull(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "v":
		m.scr = scWalk
	}
	return m, nil
}

func (m *Model) keyReview(k string) (tea.Model, tea.Cmd) {
	choice := ""
	switch k {
	case "enter":
		choice = reviewMenu[min(m.cursor, len(reviewMenu)-1)].value
	case "esc":
		choice = "cancel"
	case "left", "shift+tab":
		m.step(-1)
	case "l":
		choice = "change"
	}
	switch choice {
	case "send":
		m.busy = sendingText(m.p.answers(), m.sname)
		return m, m.sendCmd()
	case "change":
		m.openJump()
	case "cancel":
		m.toList(m.p.stopped("cancelled", m.drafts))
	}
	return m, nil
}

// jumpItems is every open question of the pass with its draft, then Back.
func (m *Model) jumpItems() []item {
	var out []item
	for _, id := range m.p.order {
		d, ok := m.decision(id)
		if !ok || d.Status != "open" || m.p.gone[id] {
			continue
		}
		answer := "—"
		if s, ok := m.p.staged[id]; ok {
			var staged *view.Staged
			if s.Kind != "skip" {
				staged = &s
			}
			answer = view.StagedText(d, staged)
		}
		q := pyfmt.Clip(d.Question, max(m.w/3, 10))
		out = append(out, item{id + "  " + q + " → " + answer, id})
	}
	return append(out, item{"Back", ""})
}

func (m *Model) openJump() {
	m.jumpFrom, m.scr, m.cursor = m.scr, scJump, 0
	for i, it := range m.jumpItems() {
		if it.value == m.p.cur && it.value != "" {
			m.cursor = i
		}
	}
}

func (m *Model) keyJump(k string) (tea.Model, tea.Cmd) {
	items := m.jumpItems()
	back := k == "esc" || (k == "enter" && items[min(m.cursor, len(items)-1)].value == "")
	switch {
	case back && m.jumpFrom == scWalk && m.p.cur != "" && !m.p.gone[m.p.cur]:
		m.show(m.p.cur)
	case back && m.jumpFrom == scWalk:
		m.advance()
	case back:
		m.scr, m.cursor = scReview, 0
	case k == "enter":
		m.show(items[m.cursor].value)
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
