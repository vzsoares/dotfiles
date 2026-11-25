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

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
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
	scAll
	scTalkWho
	scTalkPick
	scTalkNote
	scReviewNote
	scConfirm
	// scLocked, scFreeBlank and scConfirmManual are never m.scr: they name the
	// footer of a walk on a locked prod ask, of an empty free-text box and of
	// a manual ask's confirm.
	scLocked
	scFreeBlank
	scConfirmManual
	scNote
	scReply
	scChief
	scThread
	scRefile
	scDelivery
	scDeliveryEntry
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
	// waiting, waitingNotes and waitingChats are kept for a session that is
	// down; the delivery pass sends them.
	waiting      []inbox.Batch
	waitingNotes []inbox.Note
	waitingChats []inbox.Chat
	// titles is each task's newest brief title, for thread rows.
	titles map[string]string
	// thread is the open thread's timeline, read only while one is open.
	thread *inbox.Thread
	// delivery are the open delivery entries (all of them while the Delivery
	// screen shows closed ones); falseWeek counts this week's false drops.
	delivery  []inbox.Entry
	falseWeek int
	// lastClosed stamps the latest close or note read, for the empty list.
	lastClosed string
	// gen is the panel's draftGen when the read started.
	gen       int
	refreshed bool
	err       error
}

type tickMsg struct{}
type loadedMsg struct {
	snap  snapshot
	quota quota
}
type sentMsg struct {
	recorded, amended, notes []string
	res                      *inbox.Delivery
	flash                    string
	snap                     snapshot
}
type talkMsg struct {
	// err is why the popup could not open; the settle pick is skipped.
	err  string
	open []inbox.Decision
	// told is why the agent got no talk message; the popup still opens.
	told string
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
	// note is the last typed review note, refilled if the send is amended.
	note string
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
	// blocks are the raw code blocks the last layout labelled, 1-based by
	// position; yank is set after y until the number key.
	blocks []string
	yank   bool
	copied string
	clip   func(string)

	sid, sname string
	known      map[string]bool
	markers    []string
	// visited is every decision a pass showed, per session, so its list
	// still names those closed while the user was away.
	visited map[string][]string

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
	// ti is the note box: review and talk notes, and the refile task.
	ti textarea.Model
	// freeID and freeRev name the question revision the free-text box was typed against.
	freeID  string
	freeRev int64

	// md takes its colour profile from a lipgloss renderer; tests inject one.
	md *md.Renderer

	talkFrom screen
	// talkCursor is the cursor of the screen the talk started from.
	talkCursor int
	talkTold   string
	talkOpen   []inbox.Decision
	marked     map[string]bool

	// prodBlock is why prod-action asks are locked here; it applies only
	// while ib.User is false.
	prodBlock string
	// confirm is the letter scConfirm stages on y, confirmRev the rev it was
	// shown at.
	confirm    string
	confirmRev int64
	// note is the one scNote and scReply show.
	note inbox.Note
	// chiefTA is the compose box for chief; its text stays across esc.
	chiefTA textarea.Model
	chief   chiefMsg
	// replyFor is the note the textarea holds a reply for.
	replyFor int64
	// quota is the Claude rate-limit file each poll reads from quotaFile;
	// loc is where its reset times are shown.
	quota     quota
	quotaFile string
	// stateFile keeps notesFolded and threadsOpen across restarts.
	stateFile   string
	notesFolded bool
	threadsOpen map[string]bool
	// inboxOpen shows the flat Inbox list in place of the cockpit; listAt is
	// the list row an item was opened from.
	inboxOpen bool
	listAt    int
	// passing marks a delivery pass in flight and chatMisses counts its empty
	// status polls per chat; nagged are the down targets already bannered.
	nagged     map[string]bool
	passing    bool
	passAt     time.Time
	chatMisses map[int64]int
	loc        *time.Location

	thread threadView
	// passFrom, noteFrom and chiefFrom are where leaving a walk, a note or
	// the chief compose goes back to: the list or the thread.
	passFrom, noteFrom, chiefFrom screen
	// chiefTask is the task the chief compose is bound to, "" for none.
	chiefTask  string
	refileNote inbox.Note
	refileFrom screen
	// dlAll shows closed delivery entries; dlEntry is the one scDeliveryEntry
	// shows; dlWhy is why the last scan could not read agent-manager's state.db.
	dlAll      bool
	dlEntry    int64
	dlWhy      string
	dlScanning bool
	dlScanAt   time.Time
}

func New(ib *inbox.Inbox, client am.Client) *Model {
	ta := newInput(freePlaceholder)
	ti := newInput("note (optional)")
	return &Model{
		ib: ib, am: client, now: inbox.Now, ta: ta, ti: ti, chiefTA: newChiefBox(), md: md.New(lipgloss.DefaultRenderer()), clip: copyText,
		prodBlock: "guard not run", drafts: map[string]view.Staged{}, threadsOpen: map[string]bool{}, nagged: map[string]bool{}, chatMisses: map[int64]int{}, visited: map[string][]string{}, quotaFile: quotaPath(), stateFile: statePath(), loc: time.Local,
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

// watch is what a read fetches beyond the list: decisions by id and the open thread.
type watch struct {
	ids  []string
	task string
	// deliveryAll reads closed delivery entries too.
	deliveryAll bool
}

func load(ib *inbox.Inbox, client am.Client, w watch, sessions am.Sessions, refresh bool) snapshot {
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
	if s.waiting, s.err = ib.Waiting(); s.err != nil {
		return s
	}
	if s.waitingNotes, s.err = ib.WaitingNotes(); s.err != nil {
		return s
	}
	if s.waitingChats, s.err = ib.WaitingChats(); s.err != nil {
		return s
	}
	if s.titles, s.err = ib.TaskTitles(); s.err != nil {
		return s
	}
	if s.delivery, s.err = ib.Entries(w.deliveryAll); s.err != nil {
		return s
	}
	if s.falseWeek, s.err = ib.FalseDrops(inbox.Now().Add(-7 * 24 * time.Hour)); s.err != nil {
		return s
	}
	if w.task != "" {
		th, err := ib.Thread(w.task)
		if s.err = err; err != nil {
			return s
		}
		s.thread = &th
	}
	// Only the empty list shows it.
	if len(s.open)+len(s.undelivered)+len(s.notes)+len(s.failedNotes)+len(s.waiting)+len(s.waitingChats) == 0 {
		if s.lastClosed, s.err = ib.LastClosed(); s.err != nil {
			return s
		}
	}
	for _, id := range w.ids {
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

func (m *Model) watched() watch { return watch{m.watchedIDs(), m.thread.task, m.dlAll} }

func (m *Model) loadCmd(refresh bool) tea.Cmd {
	ib, client, w, sessions, gen, qpath := m.ib, m.am, m.watched(), m.snap.sessions, m.draftGen, m.quotaFile
	return func() tea.Msg {
		s := load(ib, client, w, sessions, refresh)
		s.gen = gen
		return loadedMsg{s, readQuota(qpath)}
	}
}

func (m *Model) Init() tea.Cmd {
	st := readState(m.stateFile)
	m.notesFolded = st.NotesFolded
	for _, task := range st.ThreadsOpen {
		m.threadsOpen[task] = true
	}
	return m.loadCmd(true)
}

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
	focused := m.focusedRow()
	d, n := m.inboxCount()
	hadInbox := d+n > 0
	m.snap, m.loaded = s, true
	if hadInbox {
		m.closeEmptyInbox()
	}
	m.refreshNote()
	m.refreshThread()
	m.refocus(focused)
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
	cmds := append(m.ring(), m.nag()...)
	m.expireChief()
	if c := m.chiefPoll(); c != nil {
		cmds = append(cmds, c)
	}
	if c := m.chiefPassDue(); c != nil {
		cmds = append(cmds, c)
	}
	if c := m.deliveryScanDue(); c != nil {
		cmds = append(cmds, c)
	}
	m.pruneThreadsOpen()
	// A send or talk in flight changes the db under the pass; its own reply
	// message reconciles the screen instead.
	switch {
	case m.busy != "":
	case m.p == nil && (m.scr == scSession || m.scr == scAll || m.scr == scTalkWho):
		m.liveSession()
	case m.p != nil:
		m.livePass()
	}
	if n := m.menuLen(); n > 0 {
		m.cursor = min(m.cursor, n-1)
	}
	return cmds
}

// closeEmptyInbox leaves the Inbox list once nothing is left in it.
func (m *Model) closeEmptyInbox() {
	if m.inboxOpen && m.scr == scList && len(m.listItems()) == 0 {
		m.inboxOpen = false
	}
}

// focusedRow is the list row under the cursor, "" off the list.
func (m *Model) focusedRow() string {
	if m.scr != scList {
		return ""
	}
	if items := m.listItems(); m.cursor < len(items) {
		return items[m.cursor].value
	}
	return ""
}

// refocus keeps the cursor on row after rows above it come or go; a row
// folded into its thread leaves the cursor on the thread.
func (m *Model) refocus(row string) {
	if row == "" {
		return
	}
	items := m.listItems()
	// Several Inbox rows can share a session's key: stay on the nearest.
	best := -1
	for i, it := range items {
		if it.value == row && (best < 0 || abs(i-m.cursor) < abs(best-m.cursor)) {
			best = i
		}
	}
	if best >= 0 {
		m.cursor = best
		return
	}
	if t, ok := m.threadOf(row); ok {
		for i, it := range items {
			if it.value == t.value() {
				m.cursor = i
				return
			}
		}
	}
}

// refreshThread takes the open thread's timeline from the snapshot and opens
// it at the first unread the first time.
func (m *Model) refreshThread() {
	th := m.snap.thread
	if th == nil || th.Task != m.thread.task {
		return
	}
	m.thread.data, m.thread.loaded = *th, true
	// A read note past VisibleFor is only in the thread, not in snap.notes.
	for _, it := range th.Items {
		if it.Note != nil && it.Note.ID == m.note.ID {
			m.note = *it.Note
		}
	}
	if m.scr != scThread {
		return
	}
	if !m.thread.placed {
		m.placeThread()
	}
	m.cursor = min(m.cursor, max(len(th.Items)-1, 0))
}

// ring is one banner per decision or unread note not seen before; the first
// read only learns what is already there.
func (m *Model) ring() []tea.Cmd {
	var cmds []tea.Cmd
	if m.seen != nil {
		for _, n := range view.Notices(m.snap.open, m.snap.notes, m.seen) {
			client := m.am
			cmds = append(cmds, func() tea.Msg { client.Notify(n.Title, n.Body); client.Push(n.Push); return nil })
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
			delete(p.staged, id)
			p.notes = append(p.notes, id+" changed: review your draft")
		}
	}
	for _, d := range m.sessionOpen() {
		if !slices.Contains(p.order, d.ID) {
			p.order = append(p.order, d.ID)
			p.notes = append(p.notes, d.ID+" asked; added to the walk")
		}
	}
	m.remember()
	switch m.scr {
	case scWalk, scFree, scFull, scConfirm:
		if curGone {
			m.advance()
		}
	case scReview, scReviewNote, scJump:
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
	m.thread, m.passFrom, m.noteFrom, m.chiefFrom = threadView{}, scList, scList, scList
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
// Decisions an earlier pass showed that have closed since stay in the order,
// gone, so the list shows them dimmed.
func (m *Model) startPass() {
	m.p = &pass{staged: map[string]view.Staged{}, gone: map[string]bool{}}
	open := map[string]bool{}
	for _, d := range m.sessionOpen() {
		open[d.ID] = true
		m.p.order = append(m.p.order, d.ID)
		if s, ok := m.drafts[d.ID]; ok && s.Rev == d.Rev {
			m.p.staged[d.ID] = s
		}
	}
	for _, id := range m.visited[m.sid] {
		if !open[id] {
			m.p.order = append(m.p.order, id)
			m.p.gone[id] = true
		}
	}
	inbox.SortIDs(m.p.order)
	m.remember()
	m.advance()
}

// remember keeps the pass's decisions for the session's next list, less
// those this panel recorded itself.
func (m *Model) remember() {
	m.visited[m.sid] = slices.DeleteFunc(slices.Clone(m.p.order), func(id string) bool {
		return slices.Contains(m.p.recorded, id)
	})
}

// draft is d's draft: the pass's staged answer, else the stored one; stale
// when d was amended after it was staged.
func (m *Model) draft(d inbox.Decision) (s view.Staged, ok, stale bool) {
	if m.p != nil {
		if s, ok := m.p.staged[d.ID]; ok {
			return s, true, false
		}
	}
	s, ok = m.drafts[d.ID]
	return s, ok, ok && s.Rev != d.Rev
}

// draftText is d's marker and draft for the list and Review all; an amend
// since staging adds "changed: review".
func (m *Model) draftText(d inbox.Decision) (string, string) {
	s, ok, stale := m.draft(d)
	switch {
	case !ok:
		return "○", ""
	case s.Kind == "skip":
		return "–", tagStale("skipped", stale)
	case stale && s.Kind == "pick" && d.OptionText(s.Option) == "":
		return "●", tagStale(s.Option+": (option removed)", stale)
	}
	return "●", tagStale(view.StagedText(d, &s), stale)
}

func tagStale(text string, stale bool) string {
	if stale {
		return text + " · changed: review"
	}
	return text
}

// progress is how many of sid's open decisions have a current draft.
func (m *Model) progress(sid string) (drafted, open int) {
	for _, d := range m.snap.open {
		if d.SessionID != sid {
			continue
		}
		open++
		if _, ok, stale := m.draft(d); ok && !stale {
			drafted++
		}
	}
	return drafted, open
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
	if !ok {
		return
	}
	s, staged, _ := m.draft(d)
	if !staged {
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

// saveDraft mirrors s as id's draft and queues its write. A confirmed pick is
// kept only in the pass, so a restart asks it again with its confirm.
func (m *Model) saveDraft(id string, s view.Staged) tea.Cmd {
	if d, ok := m.decision(id); ok && s.Kind == "pick" && d.NeedsConfirm(s.Option) {
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

func (m *Model) sendCmd(note string) tea.Cmd {
	byID := maps.Clone(m.p.staged)
	ids := slices.Collect(maps.Keys(byID))
	inbox.SortIDs(ids)
	ib, client, sid, name, prior := m.ib, m.am, m.sid, m.sname, m.p.result
	w, sessions := m.watched(), m.snap.sessions
	return func() tea.Msg {
		msg := sentMsg{res: prior}
		task := ""
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
			d, err := ib.Record(id, letter, text, note, &rev)
			var ie *inbox.Error
			switch {
			case err == nil:
				msg.recorded = append(msg.recorded, id)
				task = d.Task
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
		if len(msg.recorded) > 0 {
			res, err := ib.FlushSession(sid)
			if err != nil {
				msg.flash = err.Error()
				msg.snap = load(ib, client, w, sessions, false)
				return msg
			}
			if res != nil {
				msg.res = res
			}
		}
		if flash, err := ib.Describe(msg.res, sid, task, name); err == nil {
			msg.flash = flash
		} else {
			msg.flash = err.Error()
		}
		msg.snap = load(ib, client, w, sessions, false)
		return msg
	}
}

func (m *Model) onSent(msg sentMsg) tea.Cmd {
	p := m.p
	m.busy = ""
	m.snap = msg.snap
	ok := msg.res != nil && (msg.res.OK || msg.res.Waiting)
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
	from, sid := m.passFrom, m.sid
	cmd := m.leavePass(strings.Join(append([]string{msg.flash}, p.notes...), "; "))
	m.flashOK = ok
	return tea.Batch(cmd, m.openNextAfterPass(ok, from, sid), m.queueDrafts(draftOp{clear: m.sid}))
}

// ------------------------------------------------------------------- talk --

// startTalk opens the pick of decisions to talk about.
func (m *Model) startTalk(from screen) {
	if len(m.sessionOpen()) == 0 {
		m.toList("nothing open")
		return
	}
	m.talkFrom, m.talkCursor, m.marked = from, m.cursor, map[string]bool{}
	m.scr, m.cursor = scTalkWho, 0
}

// leaveTalkWho goes back to the screen the talk started from.
func (m *Model) leaveTalkWho() {
	switch {
	case m.talkFrom == scWalk && m.p != nil && !m.p.gone[m.p.cur]:
		m.show(m.p.cur)
	case m.talkFrom == scWalk && m.p != nil:
		m.advance()
	default:
		m.scr, m.cursor = m.talkFrom, m.talkCursor
	}
}

// talkMessage tells the agent which decisions the user is about to talk about.
func talkMessage(picked []inbox.Decision) string {
	lines := []string{"zen-inbox: the user is opening a talk with you about " +
		strconv.Itoa(len(picked)) + " open decision(s). Nothing is answered yet:"}
	for _, d := range picked {
		lines = append(lines, "- "+d.ID+": "+pyfmt.Clip(strings.Join(strings.Fields(d.Question), " "), 200))
	}
	return strings.Join(lines, "\n")
}

// talkPicked is the marked decisions, or the whole set when none is.
func (m *Model) talkPicked() []inbox.Decision {
	open := m.sessionOpen()
	ids := m.chosen(open)
	if len(ids) == 0 {
		return open
	}
	return slices.DeleteFunc(slices.Clone(open), func(d inbox.Decision) bool { return !slices.Contains(ids, d.ID) })
}

// talkCmd checks the popup can open, tells the agent, then opens the popup on its pane.
func (m *Model) talkCmd() tea.Cmd {
	msg := talkMessage(m.talkPicked())
	m.busy = "Talking to " + m.sname + "…"
	ib, client, sid, w, sessions := m.ib, m.am, m.sid, m.watched(), m.snap.sessions
	return func() tea.Msg {
		if err := client.CanPopup(sid); err != nil {
			return talkMsg{err: err.Error(), snap: load(ib, client, w, sessions, false)}
		}
		_, _, told := client.Send(sid, msg)
		if err := client.Popup(sid); err != nil {
			return talkMsg{err: err.Error(), told: told, snap: load(ib, client, w, sessions, false)}
		}
		open, _ := ib.SessionOpen(sid)
		return talkMsg{open: open, told: told, snap: load(ib, client, w, sessions, false)}
	}
}

func (m *Model) onTalk(msg talkMsg) tea.Cmd {
	m.busy = ""
	m.snap = msg.snap
	m.talkTold = ""
	if msg.told != "" {
		m.talkTold = "agent not told: " + msg.told
	}
	msg.open = slices.DeleteFunc(msg.open, m.locked)
	switch {
	case msg.err != "":
		m.finishTalk(nil, "talk popup failed: "+msg.err+"; c to chat with the agent, or attach: "+am.AttachCommand(m.sid))
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

// notice is the line under the question: why a prod ask is locked, else
// its draft and why a manual ask's Done is refused here.
func (m *Model) notice(d inbox.Decision) string {
	if m.locked(d) {
		return "prod approvals only from the cockpit terminal (" + m.prodBlock + ")"
	}
	var parts []string
	if _, text := m.draftText(d); text != "" {
		parts = append(parts, "draft: "+text)
	}
	if d.Kind == inbox.KindManual && !m.ib.User {
		parts = append(parts, "Done only from the cockpit terminal ("+m.prodBlock+")")
	}
	return strings.Join(parts, " · ")
}

func (m *Model) chosen(from []inbox.Decision) []string {
	var out []string
	for _, d := range from {
		if m.marked[d.ID] {
			out = append(out, d.ID)
		}
	}
	return out
}

func (m *Model) resolveCmd(note string) tea.Cmd {
	m.busy = "Resolving…"
	ib, client, sid, name, ids := m.ib, m.am, m.sid, m.sname, m.chosen(m.talkOpen)
	w, sessions := m.watched(), m.snap.sessions
	return func() tea.Msg {
		var resolved, notes []string
		var result *inbox.Delivery
		task := ""
		for _, id := range ids {
			if d, gerr := ib.Get(id); gerr == nil {
				task = d.Task
			}
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
			if d, err := ib.Describe(result, sid, task, name); err == nil {
				flash = append(flash, d)
			}
		}
		return resolvedMsg{resolved, strings.Join(append(flash, notes...), "; "),
			load(ib, client, w, sessions, false)}
	}
}

func (m *Model) finishTalk(resolved []string, flash string) {
	m.talkOpen = nil
	if m.talkTold != "" {
		flash = strings.TrimPrefix(m.talkTold+"; "+flash, "; ")
		m.talkTold = ""
	}
	if m.talkFrom == scAll {
		if open := m.sessionOpen(); len(open) > 0 {
			m.scr, m.p, m.flash, m.scroll = scAll, nil, flash, 0
			m.cursor = min(m.talkCursor, len(open)-1)
			m.flashOK, m.flashAt = false, m.now()
			return
		}
	}
	if m.talkFrom != scWalk || m.p == nil {
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
	case m.scr == scWalk && (scr == scFull || scr == scFree || scr == scConfirm || scr == scTalkWho):
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
		for _, ta := range []*textarea.Model{&m.ta, &m.chiefTA, &m.ti} {
			sizeInput(ta, m.w, m.rows()-4)
		}
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
		m.quota = msg.quota
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
	case chiefPassMsg:
		m.onChiefPass(msg)
		return m, nil
	case chiefSentMsg:
		return m, m.onChiefSent(msg)
	case chiefStatusMsg:
		if msg.id == "" && msg.err != "" {
			m.flash, m.flashOK = msg.err, false
			return m, nil
		}
		return m, m.onChiefStatus(msg)
	case notesMsg:
		m.onNotes(msg)
		return m, nil
	case noteSentMsg:
		return m, m.onNoteSent(msg)
	case refiledMsg:
		return m, m.onRefiled(msg)
	case deliveryScanMsg:
		return m, m.onDeliveryScan(msg)
	case deliveryActMsg:
		m.onDeliveryAct(msg)
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
	case scAll, scTalkWho:
		return len(m.sessionOpen())
	case scTalkPick:
		return len(m.talkOpen)
	case scDelivery:
		return len(m.deliveryShown())
	}
	return 0
}

var sessionMenu = []item{{"Answer", "answer"}, {"Review all", "all"}, {"Talk", "talk"}, {"Back", "back"}}
var reviewMenu = []item{{"Send", "send"}, {"Send with note", "note"}, {"Change one", "change"}, {"Cancel", "cancel"}}

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
	m.copied = ""
	if m.keyYank(k) {
		return m, nil
	}
	if m.jumpEnds(k) {
		return m, nil
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
	case scReviewNote:
		return m.keyReviewNote(msg)
	case scReply:
		return m.keyReply(msg)
	case scChief:
		return m.keyChief(msg)
	case scRefile:
		return m.keyRefile(msg)
	}
	n := m.menuLen()
	switch k {
	case "up", "k":
		if m.wraps() && n > 0 && m.cursor <= 0 {
			m.cursor = n - 1
			return m, nil
		}
		m.cursor = max(m.cursor-1, 0)
		return m, nil
	case "down", "j":
		if m.wraps() && n > 0 && m.cursor >= n-1 {
			m.cursor = 0
			return m, nil
		}
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
	case scAll:
		return m.keyAll(k)
	case scTalkWho:
		return m.keyTalkWho(k)
	case scTalkPick:
		return m.keyTalkPick(k)
	case scNote:
		return m.keyNoteScreen(k)
	case scThread:
		return m.keyThread(k)
	case scDelivery, scDeliveryEntry:
		return m.keyDelivery(k)
	}
	return m, nil
}

// jumpEnds moves to the top (g) or bottom (G) of the cursor lists and
// the body screens; the thread and the text-entry screens handle their own.
func (m *Model) jumpEnds(k string) bool {
	if k != "g" && k != "G" {
		return false
	}
	switch m.scr {
	case scFull, scNote, scConfirm, scDeliveryEntry:
		m.scroll = 0
		if k == "G" {
			m.scroll = 1 << 30
		}
		return true
	case scThread:
		return false
	}
	n := m.menuLen()
	if n == 0 {
		return false
	}
	m.cursor = 0
	if k == "G" {
		m.cursor = n - 1
	}
	m.scroll = 0
	return true
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
	if m.scr == scFree || m.scr == scTalkNote || m.scr == scReviewNote || m.scr == scReply || m.scr == scChief || m.scr == scRefile {
		return 0
	}
	if m.scr == scThread {
		m.thread.follow = false
	}
	switch k {
	case "ctrl+d", "ctrl+f":
		return half
	case "ctrl+u", "ctrl+b":
		return -half
	case "J":
		return 3
	case "K":
		return -3
	}
	if m.scr == scFull || m.scr == scNote || m.scr == scThread || m.scr == scConfirm || m.scr == scDeliveryEntry {
		switch k {
		case "down", "j":
			return 1
		case "up", "k":
			return -1
		}
	}
	return 0
}

// listItems are the cockpit's rows, most urgent first: retries, threads
// (prod first), then the notes without a task.
func (m *Model) listItems() []item {
	rows := m.listRows()
	out := make([]item, len(rows))
	for i, r := range rows {
		out[i] = r.item
	}
	return out
}

// askRows are the asking sessions, those with a prod ask first; prod marks them.
func (m *Model) askRows() ([]view.Row, map[string]bool) {
	prod := map[string]bool{}
	for _, d := range m.snap.open {
		if d.Prod != nil {
			prod[d.SessionID] = true
		}
	}
	rows := view.Rows(m.snap.open, m.snap.sessions)
	slices.SortStableFunc(rows, func(a, b view.Row) int {
		switch {
		case prod[a.SessionID] == prod[b.SessionID]:
			return 0
		case prod[a.SessionID]:
			return -1
		}
		return 1
	})
	return rows, prod
}

// listKeys are the cockpit footer's keys for the row under the cursor.
func (m *Model) listKeys() []ranked {
	items := m.listItems()
	var keys []ranked
	if len(items) > 1 {
		keys = append(keys, ranked{key{"↑↓", ""}, 7})
	}
	kind := ""
	if m.cursor < len(items) {
		kind, _, _ = strings.Cut(items[m.cursor].value, ":")
	}
	row := ""
	if m.cursor < len(items) {
		row = items[m.cursor].value
	}
	t, inThread := m.rowThread(row)
	switch kind {
	case "t":
		fold := "expand"
		if m.threadsOpen[t.task] {
			fold = "fold"
		}
		keys = append(keys, ranked{key{"enter", fold}, 0}, ranked{key{"o", "thread"}, 4})
		if t.open > 0 {
			keys = append(keys, ranked{key{"a", "answer"}, 1})
		}
		if t.unread() > 0 {
			keys = append(keys, ranked{key{"m", "read all"}, 3})
		}
	case "i", "dl":
		keys = append(keys, ranked{key{"enter", "open"}, 0})
	case "s":
		keys = append(keys, ranked{key{"enter", "open"}, 0}, ranked{key{"a", "answer"}, 1})
	case "r", "nr", "w", "nw", "cw":
		keys = append(keys, ranked{key{"enter", "retry"}, 0})
		if kind == "r" || kind == "w" {
			keys = append(keys, ranked{key{"R", "to chief"}, 1})
		}
	case "nh":
		keys = append(keys, ranked{key{"enter", "expand"}, 0})
	case "n":
		keys = append(keys, ranked{key{"enter", "open"}, 0})
		if n, ok := m.cursorNote(); ok && n.Task == "" {
			keys = append(keys, ranked{key{"z", "fold"}, 6})
		}
		keys = append(keys, ranked{key{"T", "refile"}, 6})
		if n, ok := m.cursorNote(); ok {
			mark := "read"
			if !n.Unread() {
				mark = "unread"
			}
			keys = append(keys, ranked{key{"m", mark}, 3})
			if n.CanReply() {
				keys = append(keys, ranked{key{"f", "reply"}, 4})
			}
		}
	}
	if inThread && kind != "t" {
		keys = append(keys, ranked{key{"←", "fold"}, 6})
	}
	if !retryRow(kind) && len(m.snap.undelivered)+len(m.snap.failedNotes)+len(m.waitRows()) > 0 {
		keys = append(keys, ranked{key{"r", "retry"}, 1})
	}
	keys = append(keys, ranked{key{"c", "chief"}, 2})
	if inbox.Unread(m.snap.notes) > 0 {
		keys = append(keys, ranked{key{"M", "all read"}, 6})
	}
	return append(keys, ranked{key{"?", "help"}, 5}, ranked{key{"q", "quit"}, 3})
}

// retryRow is whether a list row of that kind is a failed or waiting delivery.
func retryRow(kind string) bool { return slices.Contains([]string{"r", "nr", "w", "nw", "cw"}, kind) }

// retryRowCmd retries the delivery a failed or waiting row stands for.
func (m *Model) retryRowCmd(kind, id string) tea.Cmd {
	switch kind {
	case "r", "w":
		return m.retryCmd(id)
	case "nr", "nw":
		n, _ := strconv.ParseInt(id, 10, 64)
		return m.retryNoteCmd(n)
	}
	return m.passNowCmd()
}

// toChiefCmd sends a session's failed or waiting answers to chief.
func (m *Model) toChiefCmd(sid string) tea.Cmd {
	m.busy = "Sending " + m.undeliveredName(sid) + "'s answers to chief…"
	ib, client, sessions := m.ib, m.am, m.snap.sessions
	return func() tea.Msg {
		res, err := ib.RetryToChief(sid)
		flash := ""
		if err != nil {
			flash = err.Error()
		} else {
			flash = "sent " + strings.Join(res.IDs, ", ") + " to chief"
		}
		return retriedMsg{flash, err == nil, load(ib, client, watch{}, sessions, false)}
	}
}

func (m *Model) undeliveredName(sid string) string {
	for _, b := range slices.Concat(m.snap.undelivered, m.snap.waiting) {
		if b.SessionID == sid {
			return b.Decisions[0].SessionName
		}
	}
	return sid
}

func (m *Model) keyList(k string) (tea.Model, tea.Cmd) {
	if k == "enter" || k == "a" || k == "f" {
		m.listAt = m.cursor
	}
	if !m.inboxOpen {
		if ok, cmd := m.keyThreadRow(k); ok {
			return m, cmd
		}
	}
	switch k {
	case "q", "esc":
		if m.inboxOpen {
			m.inboxOpen = false
			m.refocus("i:")
			return m, nil
		}
		return m, m.quit()
	case "r":
		if kind, id, _ := strings.Cut(m.focusedRow(), ":"); retryRow(kind) {
			return m, m.retryRowCmd(kind, id)
		}
		return m, m.retryFirst()
	case "c":
		return m, m.openChief(m.rowTask(m.focusedRow()), scList)
	case "R":
		if kind, id, _ := strings.Cut(m.focusedRow(), ":"); kind == "r" || kind == "w" {
			return m, m.toChiefCmd(id)
		}
	case "T":
		if n, ok := m.cursorNote(); ok {
			return m, m.openRefile(n, scList)
		}
	case "z":
		row := m.focusedRow()
		if n, ok := m.cursorNote(); (ok && n.Task == "") || row == "nh:" {
			m.foldNotes(!m.notesFolded)
		}
	case "m":
		if n, ok := m.cursorNote(); ok {
			return m, m.markCmd(n.ID, false, n.Unread())
		}
	case "M":
		if inbox.Unread(m.snap.notes) > 0 {
			return m, m.markCmd(0, true, true)
		}
	case "a":
		if items := m.listItems(); m.cursor < len(items) {
			if sid, ok := strings.CutPrefix(items[m.cursor].value, "s:"); ok {
				m.enterSession(sid)
				if m.scr == scSession {
					m.startPass()
				}
			}
		}
	case "f":
		if n, ok := m.cursorNote(); ok {
			m.openNote(noteValue(n))
			return m.keyNoteScreen("f")
		}
	case "enter":
		items := m.listItems()
		if m.cursor >= len(items) {
			return m, nil
		}
		kind, sid, _ := strings.Cut(items[m.cursor].value, ":")
		switch kind {
		case "i":
			m.inboxOpen, m.cursor = true, 0
			return m, nil
		case "dl":
			m.openDeliveryScreen()
			return m, nil
		case "r", "nr", "w", "nw", "cw":
			return m, m.retryRowCmd(kind, sid)
		case "n":
			m.openNote(sid)
			return m, nil
		case "nh":
			m.foldNotes(false)
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
		} else if flash, err = ib.Describe(res, sid, "", ""); err != nil {
			flash = err.Error()
		}
		ok := err == nil && res != nil && res.OK
		return retriedMsg{flash, ok, load(ib, client, watch{}, sessions, false)}
	}
}

func (m *Model) keySession(k string) (tea.Model, tea.Cmd) {
	choice := ""
	switch k {
	case "enter":
		choice = sessionMenu[min(m.cursor, len(sessionMenu)-1)].value
	case "r":
		choice = "all"
	case "t":
		choice = "talk"
	case "esc":
		choice = "back"
	}
	switch choice {
	case "answer":
		m.startPass()
		// Decisions closed since an earlier pass are not in the snapshot yet.
		return m, m.loadCmd(false)
	case "all":
		m.scr, m.cursor = scAll, 0
	case "talk":
		m.startTalk(scSession)
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
		m.openJump()
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
		if s, ok, stale := m.draft(d); ok && s.Kind == "text" {
			m.ta.SetValue(s.Text)
			if !stale {
				m.freeRev = s.Rev
			}
		}
		m.flash = ""
		m.scr = scFree
		return m, m.ta.Focus()
	case "full":
		m.scr = scFull
	case "talk":
		m.startTalk(scWalk)
	default:
		switch {
		case m.locked(d), !m.ib.User && d.NeedsConfirm(choice), d.OptionText(choice) == "":
		case d.NeedsConfirm(choice):
			m.confirm, m.confirmRev = choice, d.Rev
			m.scr = scConfirm
		default:
			return m, m.stage(view.Staged{Kind: "pick", Option: choice, Rev: d.Rev})
		}
	}
	return m, nil
}

// keyConfirm stages the pick only on y; any other key goes back unstaged,
// but a manual ask's step scrolls.
func (m *Model) keyConfirm(k string) (tea.Model, tea.Cmd) {
	d, ok := m.decision(m.p.cur)
	if ok && d.Kind == inbox.KindManual {
		if m.jumpEnds(k) {
			return m, nil
		}
		if n := m.scrollBy(k); n != 0 {
			m.scroll = max(m.scroll+n, 0)
			return m, nil
		}
	}
	m.scr = scWalk
	if ok && k == "y" && d.Rev != m.confirmRev {
		m.p.notes = append(m.p.notes, d.ID+" changed: read it again before confirming")
		return m, nil
	}
	if ok && k == "y" && m.ib.User && d.NeedsConfirm(m.confirm) {
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
	submit, cancel, cmd := keyInput(&m.ta, msg)
	switch {
	case cancel:
		return m, m.saveFree(false)
	case submit:
		return m, m.saveFree(true)
	}
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
	case "n":
		choice = "note"
	}
	switch choice {
	case "send":
		m.busy = sendingText(m.p.answers(), m.sname)
		m.p.note = ""
		return m, m.sendCmd("")
	case "note":
		m.ti.Reset()
		m.ti.Placeholder = "note (optional)"
		m.ti.SetValue(m.p.note)
		m.scr = scReviewNote
		return m, m.ti.Focus()
	case "change":
		m.openJump()
	case "cancel":
		return m, m.leavePass(m.p.stopped("cancelled", m.drafts))
	}
	return m, nil
}

// jumpItems is the session's decision list: every open question of the
// pass with its draft marker, then Back.
func (m *Model) jumpItems() []item {
	var out []item
	for _, id := range m.p.order {
		d, ok := m.decision(id)
		if !ok || d.Status != "open" || m.p.gone[id] {
			continue
		}
		mark, answer := m.draftText(d)
		label := mark + " " + id + "  " + pyfmt.Clip(md.Plain(d.Question), max(m.w/3, 10))
		if answer != "" {
			label += " → " + answer
		}
		out = append(out, item{label, id})
	}
	return append(out, item{"Back", ""})
}

// closedItems are the pass's decisions that closed while it ran or since an
// earlier pass, with how they closed.
func (m *Model) closedItems() []string {
	var out []string
	for _, id := range m.p.order {
		d, ok := m.decision(id)
		if !ok || d.Status == "open" {
			continue
		}
		out = append(out, "  × "+id+"  "+pyfmt.Clip(md.Plain(d.Question), max(m.w/3, 10))+" · "+closedState(d))
	}
	return out
}

func closedState(d inbox.Decision) string {
	switch {
	case d.Status == "withdrawn":
		return "withdrawn"
	case d.AnswerKind == "talk":
		return "resolved in talk"
	case d.AnswerKind == "pick":
		return "answered: " + d.AnswerOption
	}
	return "answered: text"
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
	case back && m.jumpFrom == scReview:
		m.scr, m.cursor = scReview, 0
	case back:
		return m, m.leavePass(m.p.stopped("back", m.drafts))
	case k == "enter":
		m.show(items[m.cursor].value)
	}
	return m, nil
}

func (m *Model) keyAll(k string) (tea.Model, tea.Cmd) {
	open := m.sessionOpen()
	switch k {
	case "enter":
		if m.cursor < len(open) {
			id := open[m.cursor].ID
			m.startPass()
			if m.p != nil && slices.Contains(m.p.order, id) && !m.p.gone[id] {
				m.show(id)
			}
			return m, m.loadCmd(false)
		}
	case "t":
		m.startTalk(scAll)
	case "esc":
		m.scr, m.cursor = scSession, 1
	}
	return m, nil
}

func (m *Model) keyTalkWho(k string) (tea.Model, tea.Cmd) {
	open := m.sessionOpen()
	switch k {
	case " ", "space":
		if m.cursor < len(open) {
			id := open[m.cursor].ID
			m.marked[id] = !m.marked[id]
		}
	case "enter":
		return m, m.talkCmd()
	case "esc":
		m.leaveTalkWho()
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
		if len(m.chosen(m.talkOpen)) == 0 {
			m.finishTalk(nil, "all left open")
			return m, nil
		}
		m.ti.Reset()
		m.ti.Placeholder = "note (optional)"
		m.scr = scTalkNote
		return m, m.ti.Focus()
	case "esc":
		m.finishTalk(nil, "all left open")
	}
	return m, nil
}

func (m *Model) keyNote(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	submit, cancel, cmd := keyInput(&m.ti, msg)
	switch {
	case submit:
		return m, m.resolveCmd(strings.TrimSpace(m.ti.Value()))
	case cancel:
		return m, m.resolveCmd("")
	}
	return m, cmd
}

// keyReviewNote reads one note for the whole batch; empty sends plain.
func (m *Model) keyReviewNote(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	submit, cancel, cmd := keyInput(&m.ti, msg)
	switch {
	case submit:
		m.busy = sendingText(m.p.answers(), m.sname)
		m.p.note = strings.TrimSpace(m.ti.Value())
		return m, m.sendCmd(m.p.note)
	case cancel:
		m.scr, m.cursor = scReview, 0
		return m, nil
	}
	return m, cmd
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// wraps is whether up and down wrap around: only in the Inbox list.
func (m *Model) wraps() bool { return m.scr == scList && m.inboxOpen }
