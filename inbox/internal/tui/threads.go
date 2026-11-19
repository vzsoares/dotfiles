package tui

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/md"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

// thread is one task's list entry: its asking sessions (prod first) and its
// visible notes (newest first).
type thread struct {
	task, title string
	// noteAt is the newest note's time when its title stands in for a missing one.
	noteAt string
	noteID int64
	rows   []view.Row
	notes  []inbox.Note
	prod   bool
	open   int
	held   int
	newest string
}

func (t thread) size() int { return len(t.rows) + len(t.notes) }

func (t thread) unread() int { return inbox.Unread(t.notes) }

// values are the list values of the thread's child rows.
func (t thread) values() []string {
	var out []string
	for _, r := range t.rows {
		out = append(out, "s:"+r.SessionID)
	}
	for _, n := range t.notes {
		out = append(out, "n:"+noteValue(n))
	}
	return out
}

// value is the thread's list row: its header, or its one child when single.
func (t thread) value() string {
	if t.size() == 1 {
		return t.values()[0]
	}
	return "t:" + t.task
}

// threads groups the asks and notes with a task, most urgent first: a prod
// ask, an open ask, unread notes, then the newest activity.
func (m *Model) threads() []thread {
	rows, prod := m.askRows()
	byTask := map[string]*thread{}
	var order []string
	get := func(task string) *thread {
		if t, ok := byTask[task]; ok {
			return t
		}
		order = append(order, task)
		byTask[task] = &thread{task: task, title: m.snap.titles[task]}
		return byTask[task]
	}
	waiting := view.HeldWaiting(m.snap.held, m.snap.open)
	for _, r := range rows {
		t := get(r.First)
		t.held += len(waiting[[2]string{r.SessionID, r.First}])
		t.rows = append(t.rows, r)
		t.prod = t.prod || prod[r.SessionID]
		for _, d := range m.snap.open {
			if d.SessionID == r.SessionID && d.Task == r.First {
				t.open++
			}
		}
		t.newest = max(t.newest, r.Newest)
	}
	for _, n := range m.snap.notes {
		if n.Task == "" {
			continue
		}
		t := get(n.Task)
		t.notes = append(t.notes, n)
		if m.snap.titles[n.Task] == "" && (n.CreatedAt > t.noteAt || n.CreatedAt == t.noteAt && n.ID > t.noteID) {
			t.title, t.noteAt, t.noteID = n.Title, n.CreatedAt, n.ID
		}
		t.newest = max(t.newest, n.CreatedAt)
	}
	out := make([]thread, len(order))
	for i, task := range order {
		out[i] = *byTask[task]
	}
	first := func(b bool) int {
		if b {
			return 0
		}
		return 1
	}
	slices.SortStableFunc(out, func(a, b thread) int {
		return cmp.Or(cmp.Compare(first(a.prod), first(b.prod)), cmp.Compare(first(a.open > 0), first(b.open > 0)),
			cmp.Compare(first(a.unread() > 0), first(b.unread() > 0)), cmp.Compare(b.newest, a.newest),
			inbox.CompareID(a.task, b.task))
	})
	return out
}

// looseNotes are the visible notes without a task.
func (m *Model) looseNotes() []inbox.Note {
	var out []inbox.Note
	for _, n := range m.snap.notes {
		if n.Task == "" {
			out = append(out, n)
		}
	}
	return out
}

// threadOf is the thread a list value belongs to.
func (m *Model) threadOf(value string) (thread, bool) {
	for _, t := range m.threads() {
		if value == "t:"+t.task || slices.Contains(t.values(), value) {
			return t, true
		}
	}
	return thread{}, false
}

const (
	gInbox = iota
	gFailed
	gThreads
	gNoTask
)

// listRow is one cockpit row with the group it sits in and its style.
type listRow struct {
	item
	group int
	style *lipgloss.Style
}

// askLabel is a session's row: PROD, then its RowLabel with drafting progress.
func (m *Model) askLabel(r view.Row, prod bool) string {
	r.Drafted, _ = m.progress(r.SessionID)
	label := view.RowLabel(r, m.now())
	if prod {
		label = "PROD · " + label
	}
	return label
}

func noteStyle(n inbox.Note) *lipgloss.Style {
	if n.Unread() {
		return &unreadStyle
	}
	return &readStyle
}

// threadRows are the Threads group's rows: one per thread, a lone item flat,
// and an expanded thread's children indented under its header.
func (m *Model) threadRows(room int) []listRow {
	_, prod := m.askRows()
	at := m.now()
	var out []listRow
	ask := func(r view.Row, indent string, width int) listRow {
		var st *lipgloss.Style
		if prod[r.SessionID] {
			st = &failStyle
		}
		return listRow{item{indent + pyfmt.Clip(m.askLabel(r, prod[r.SessionID]), width), "s:" + r.SessionID}, gThreads, st}
	}
	for _, t := range m.threads() {
		if t.size() == 1 {
			if len(t.rows) == 1 {
				out = append(out, ask(t.rows[0], "", room))
			} else {
				n := t.notes[0]
				out = append(out, listRow{item{pyfmt.Clip(view.NoteLabel(n, at), room), "n:" + noteValue(n)}, gThreads,
					noteStyle(n)})
			}
			continue
		}
		open := m.threadsOpen[t.task]
		head := view.ThreadHead{Task: t.task, Title: t.title, Expanded: open, Prod: t.prod, Open: t.open, Held: t.held,
			Unread: t.unread(), Items: t.open + len(t.notes), Newest: t.newest}
		var st *lipgloss.Style
		switch {
		case t.prod:
			st = &failStyle
		case t.open > 0 || t.unread() > 0:
			st = &unreadStyle
		}
		out = append(out, listRow{item{pyfmt.Clip(view.ThreadLabel(head, at, room), room), "t:" + t.task}, gThreads, st})
		if !open {
			continue
		}
		for _, r := range t.rows {
			out = append(out, ask(r, "  ", room-2))
		}
		for _, n := range t.notes {
			out = append(out, listRow{item{"  " + pyfmt.Clip(view.ThreadNoteLabel(n, at), room-2), "n:" + noteValue(n)},
				gThreads, noteStyle(n)})
		}
	}
	return out
}

// listRows are the cockpit's rows in order: failed deliveries, threads, then
// the notes without a task.
func (m *Model) listRows() []listRow {
	room := max(m.w, view.MinWidth) - 2
	if m.inboxOpen {
		return m.inboxRows(room)
	}
	var out []listRow
	if m.loaded {
		out = append(out, listRow{item{pyfmt.Clip(m.inboxLabel(), room), "i:"}, gInbox, &unreadStyle})
	}
	for _, b := range m.snap.undelivered {
		label := "retry " + b.Decisions[0].SessionName + ": " + strconv.Itoa(len(b.Decisions)) + " answer(s) · " +
			view.FailReason(b)
		out = append(out, listRow{item{pyfmt.Clip(label, room), "r:" + b.SessionID}, gFailed, &failStyle})
	}
	for _, n := range m.snap.failedNotes {
		label := "retry reply " + n.Label() + " to " + n.SessionName
		if n.DeliveryError != "" {
			label += " · " + n.DeliveryError
		}
		out = append(out, listRow{item{pyfmt.Clip(label, room), "nr:" + noteValue(n)}, gFailed, &failStyle})
	}
	out = append(out, m.threadRows(room)...)
	loose := m.looseNotes()
	if m.notesFolded && len(loose) > 0 {
		return append(out, listRow{item{pyfmt.Clip(m.notesHead(), room), "nh:"}, gNoTask, nil})
	}
	for _, n := range loose {
		out = append(out, listRow{item{pyfmt.Clip(view.NoteLabel(n, m.now()), room), "n:" + noteValue(n)}, gNoTask,
			noteStyle(n)})
	}
	return out
}

// inboxCount is how many decisions and unread notes wait in the Inbox.
func (m *Model) inboxCount() (decisions, notes int) {
	return len(m.snap.open), inbox.Unread(m.snap.notes)
}

// inboxLabel is the Inbox row's text.
func (m *Model) inboxLabel() string {
	d, n := m.inboxCount()
	if d+n == 0 {
		return "Inbox · all clear"
	}
	return "Inbox · " + plural(d, "decision") + " · " + plural(n, "note")
}

// inboxRows are the open decisions, then the unread notes, across all threads.
func (m *Model) inboxRows(room int) []listRow {
	var out []listRow
	for _, d := range m.snap.open {
		label := d.SessionName + " · #" + d.Task + " · " + md.Plain(strings.Join(strings.Fields(d.Question), " "))
		var st *lipgloss.Style
		if d.Prod != nil {
			st = &failStyle
		}
		out = append(out, listRow{item{pyfmt.Clip(label, room), "s:" + d.SessionID}, gInbox, st})
	}
	for _, n := range m.snap.notes {
		if n.Unread() {
			out = append(out, listRow{item{pyfmt.Clip(view.NoteLabel(n, m.now()), room), "n:" + noteValue(n)}, gInbox,
				noteStyle(n)})
		}
	}
	return out
}

// nextInboxRow is the row after cur in the Inbox list.
func (m *Model) nextInboxRow(cur string) (string, bool) {
	items := m.listItems()
	at := slices.IndexFunc(items, func(it item) bool { return it.value == cur })
	if at < 0 || at+1 >= len(items) {
		return "", false
	}
	return items[at+1].value, true
}

// openNextAfterPass runs when a pass ends at from: it opens the row or thread
// item after the finished one when that is an unread note or an open decision
// of another session; otherwise the screen the pass came from stays up.
func (m *Model) openNextAfterPass(ok bool, from screen, sid string) tea.Cmd {
	switch {
	case !ok:
	case from == scThread:
		return m.openNextInThread(sid)
	case m.scr == scList:
		items := m.listItems()
		if m.listAt >= len(items) {
			m.cursor = max(len(items)-1, 0)
			break
		}
		row := items[m.listAt].value
		kind, id, _ := strings.Cut(row, ":")
		if kind == "s" && id != sid || kind == "n" && m.unreadNote(id) {
			m.openRow(row)
		}
	}
	return nil
}

func (m *Model) unreadNote(id string) bool {
	for _, n := range m.snap.notes {
		if noteValue(n) == id && n.Unread() {
			return true
		}
	}
	return false
}

// openNextInThread opens the item after the focused one when it is an unread
// note or an open decision outside session skip.
func (m *Model) openNextInThread(skip string) tea.Cmd {
	items := m.thread.data.Items
	at := m.thread.cursor + 1
	if at >= len(items) {
		return nil
	}
	it := items[at]
	switch {
	case it.Note != nil && it.Note.Unread():
		m.cursor = at
		m.leaveThread()
		m.note, m.noteFrom, m.scr, m.flash = *it.Note, scThread, scNote, ""
		m.scroll = 0
	case it.Decision != nil && it.Decision.SessionID != skip:
		if d, ok := m.decision(it.Decision.ID); ok && d.Status == "open" {
			m.cursor = at
			return m.walkFromThread(d.SessionID, d.ID)
		}
	}
	return nil
}

// openRow opens the note or the session a list row stands for.
func (m *Model) openRow(row string) {
	m.refocus(row)
	kind, id, _ := strings.Cut(row, ":")
	switch kind {
	case "n":
		m.openNote(id)
	case "s":
		m.enterSession(id)
	}
}

// saveState writes the fold state; a failure is flashed, not fatal.
func (m *Model) saveState() {
	var open []string
	for task, ok := range m.threadsOpen {
		if ok {
			open = append(open, task)
		}
	}
	inbox.SortIDs(open)
	if err := writeState(m.stateFile, panelState{NotesFolded: m.notesFolded, ThreadsOpen: open}); err != nil {
		m.flash, m.flashOK = "could not save fold state: "+err.Error(), false
	}
}

// pruneThreadsOpen forgets expanded threads whose task has no open ask,
// visible note or thread left, so the saved state does not grow forever.
// An empty snapshot prunes nothing: it may be a transient empty read.
func (m *Model) pruneThreadsOpen() {
	if len(m.threadsOpen) == 0 {
		return
	}
	live := map[string]bool{}
	for _, d := range m.snap.open {
		live[d.Task] = true
	}
	for _, n := range m.snap.notes {
		live[n.Task] = true
	}
	for _, t := range m.threads() {
		live[t.task] = true
	}
	if len(live) == 0 {
		return
	}
	pruned := false
	for task := range m.threadsOpen {
		if !live[task] {
			delete(m.threadsOpen, task)
			pruned = true
		}
	}
	if pruned {
		m.saveState()
	}
}

// foldThread expands or collapses task's thread with the cursor on its header.
func (m *Model) foldThread(task string, open bool) {
	if m.threadsOpen[task] == open {
		m.refocus("t:" + task)
		return
	}
	if open {
		m.threadsOpen[task] = true
	} else {
		delete(m.threadsOpen, task)
	}
	m.refocus("t:" + task)
	m.saveState()
}

// rowThread is the thread with more than one item that the list value is
// the header or a child of.
func (m *Model) rowThread(value string) (thread, bool) {
	t, ok := m.threadOf(value)
	return t, ok && t.size() > 1
}

// rowTask is the task a chat from the list row binds: any row in Threads.
func (m *Model) rowTask(value string) string {
	if t, ok := m.threadOf(value); ok {
		return t.task
	}
	return ""
}

// keyThreadRow handles the list keys that act on a whole thread; false when
// k is not one of them here.
func (m *Model) keyThreadRow(k string) (bool, tea.Cmd) {
	row := m.focusedRow()
	t, ok := m.rowThread(row)
	if !ok {
		return false, nil
	}
	header := row == "t:"+t.task
	switch {
	case (k == "z" && header) || (k == "enter" && header) || (k == "right" && header):
		m.foldThread(t.task, k == "right" || !m.threadsOpen[t.task])
	case k == "z" || k == "left":
		m.foldThread(t.task, false)
	case k == "o" && header:
		return true, m.openThread(t.task)
	case k == "a" && header:
		m.answerThread(t.rows)
	case k == "m" && header:
		return true, m.markIDsCmd(unreadIDs(t.notes))
	default:
		return false, nil
	}
	return true, nil
}

func unreadIDs(ns []inbox.Note) []int64 {
	var out []int64
	for _, n := range ns {
		if n.Unread() {
			out = append(out, n.ID)
		}
	}
	return out
}

// answerThread starts the walk of the first asking session, prod first.
func (m *Model) answerThread(rows []view.Row) {
	if len(rows) == 0 {
		return
	}
	m.enterSession(rows[0].SessionID)
	if m.scr == scSession {
		m.startPass()
	}
}

func (m *Model) markIDsCmd(ids []int64) tea.Cmd {
	if len(ids) == 0 {
		return nil
	}
	ib, client, w, sessions := m.ib, m.am, m.watched(), m.snap.sessions
	return func() tea.Msg {
		return notesMsg{ib.MarkNotes(ids, true), load(ib, client, w, sessions, false)}
	}
}

// ------------------------------------------------------------ thread view --

// threadView is the open thread: the task, its last read and the screen's
// own state, kept while a note, walk or compose opened from it is shown.
type threadView struct {
	task   string
	data   inbox.Thread
	loaded bool
	// placed is set once the screen opened at the first unread.
	placed bool
	// follow keeps the focused entry in view after tab moves it.
	follow bool
	cursor int
	scroll int
}

func (m *Model) openThread(task string) tea.Cmd {
	m.thread = threadView{task: task}
	m.scr, m.cursor, m.scroll, m.flash = scThread, 0, 0, ""
	return m.loadCmd(false)
}

// backToThread shows the thread again where it was left and reloads it.
func (m *Model) backToThread(flash string) tea.Cmd {
	m.scr, m.p, m.flash = scThread, nil, flash
	m.flashOK, m.flashAt = false, m.now()
	m.known, m.markers, m.talkOpen = nil, nil, nil
	m.cursor, m.scroll = m.thread.cursor, m.thread.scroll
	m.passFrom, m.noteFrom, m.chiefFrom = scList, scList, scList
	return m.loadCmd(false)
}

// leavePass ends a walk on the thread it was started from, else on the list.
func (m *Model) leavePass(flash string) tea.Cmd {
	if m.passFrom == scThread {
		return m.backToThread(flash)
	}
	m.toList(flash)
	return nil
}

// leaveThread keeps where the thread was so a screen opened from it returns there.
func (m *Model) leaveThread() {
	m.thread.cursor, m.thread.scroll = m.cursor, m.scroll
}

// placeThread opens the thread at its first unread note or open ask, else at the end.
func (m *Model) placeThread() {
	m.thread.placed = true
	items := m.thread.data.Items
	if len(items) == 0 {
		return
	}
	_, starts := m.threadBody()
	for i, it := range items {
		if (it.Note != nil && it.Note.Unread()) || (it.Decision != nil && it.Decision.Status == "open") {
			m.cursor, m.scroll = i, starts[i]
			return
		}
	}
	m.cursor, m.scroll = len(items)-1, 1<<30
}

// threadBody is the timeline with a › gutter on the focused entry, and the
// row each entry starts on.
func (m *Model) threadBody() ([]string, []int) {
	entries := view.PanelThreadLines(m.thread.data, max(m.w-2, view.MinWidth), m.now(), m.paint)
	var body []string
	starts := make([]int, len(entries))
	for i, lines := range entries {
		if i > 0 {
			body = append(body, "")
		}
		starts[i] = len(body)
		for j, l := range lines {
			lead := "  "
			switch {
			case i == m.cursor && j == 0:
				lead = cursorStyle.Render("›") + " "
			case l == "":
				lead = ""
			}
			body = append(body, lead+l)
		}
	}
	return body, starts
}

// threadCounts is the thread's open asks, unread notes and entries.
func (m *Model) threadCounts() string {
	open, unread := 0, 0
	for _, it := range m.thread.data.Items {
		if it.Decision != nil && it.Decision.Status == "open" {
			open++
		}
		if it.Note != nil && it.Note.Unread() {
			unread++
		}
	}
	var parts []string
	if open > 0 {
		parts = append(parts, strconv.Itoa(open)+" open")
	}
	if unread > 0 {
		parts = append(parts, strconv.Itoa(unread)+" unread")
	}
	return strings.Join(append(parts, strconv.Itoa(len(m.thread.data.Items))+" entries"), " · ")
}

func (m *Model) viewThread() frame {
	th := m.thread.data
	head := pyfmt.RStrip("#" + m.thread.task + " " + th.Title)
	counts := m.threadCounts()
	line := pyfmt.Clip(head, m.w)
	if room := m.w - lipgloss.Width(counts) - 2; room >= 8 {
		line = pyfmt.Clip(head, room)
		line += strings.Repeat(" ", m.w-lipgloss.Width(line)-lipgloss.Width(counts)) + counts
	}
	top := []section{{lines: []string{line}, style: &headerStyle}}
	if m.copied != "" {
		top = append(top, section{lines: view.WrapRest("» "+m.copied, m.w, "", "  "), style: &bannerStyle})
	}
	if m.flash != "" {
		top = append(top, section{lines: view.WrapRest("» "+m.flash, m.w, "", "  "), style: &bannerStyle})
	}
	if !m.thread.loaded {
		return frame{s: scThread, top: top, body: []section{{lines: []string{"loading…"}, style: &readStyle}}, cur: -1, help: true}
	}
	body, starts := m.threadBody()
	// The brief is the first thing given up when rows run short.
	if brief := view.PanelBriefText(view.Brief{Task: th.Task, Text: th.Brief}, m.w, m.paint); len(brief) > 0 &&
		m.rows()-count(top)-len(brief)-m.footRows(scThread) >= 8 {
		top = append(top, section{lines: brief, style: &readStyle})
	}
	cur := -1
	if m.thread.follow && m.cursor < len(starts) {
		cur = starts[m.cursor]
	}
	if len(body) == 0 {
		body = []string{"  nothing in this thread yet"}
	}
	return frame{s: scThread, top: top, body: []section{{lines: body}}, cur: cur, help: true}
}

// threadItem is the focused entry.
func (m *Model) threadItem() (inbox.ThreadItem, bool) {
	items := m.thread.data.Items
	if m.cursor < len(items) {
		return items[m.cursor], true
	}
	return inbox.ThreadItem{}, false
}

// threadNote is the focused note, else the newest note when newest is set.
func (m *Model) threadNote(newest bool) (inbox.Note, bool) {
	if it, ok := m.threadItem(); ok && it.Note != nil {
		return *it.Note, true
	}
	if newest {
		items := m.thread.data.Items
		for i := len(items) - 1; i >= 0; i-- {
			if items[i].Note != nil {
				return *items[i].Note, true
			}
		}
	}
	return inbox.Note{}, false
}

func (m *Model) keyThread(k string) (tea.Model, tea.Cmd) {
	items := m.thread.data.Items
	switch k {
	case "tab":
		m.cursor, m.thread.follow = min(m.cursor+1, max(len(items)-1, 0)), true
	case "shift+tab":
		m.cursor, m.thread.follow = max(m.cursor-1, 0), true
	case "g":
		m.scroll, m.thread.follow = 0, false
	case "G":
		m.scroll, m.thread.follow = 1<<30, false
	case "esc", "q":
		task := m.thread.task
		m.toList("")
		m.refocusThread(task)
	case "enter":
		it, ok := m.threadItem()
		switch {
		case !ok:
		case it.Note != nil:
			m.leaveThread()
			m.note, m.noteFrom, m.scr, m.flash = *it.Note, scThread, scNote, ""
			m.scroll = 0
		case it.Decision != nil && it.Decision.Status == "open":
			return m, m.walkFromThread(it.Decision.SessionID, it.Decision.ID)
		}
	case "f":
		if n, ok := m.threadNote(true); ok {
			m.leaveThread()
			m.note, m.noteFrom = n, scThread
			return m.keyNoteScreen("f")
		}
	case "m":
		var ns []inbox.Note
		for _, it := range items {
			if it.Note != nil {
				ns = append(ns, *it.Note)
			}
		}
		return m, m.markIDsCmd(unreadIDs(ns))
	case "a":
		var first *inbox.Decision
		for _, it := range items {
			if d := it.Decision; d != nil && d.Status == "open" && (first == nil || (d.Prod != nil && first.Prod == nil)) {
				first = d
			}
		}
		if first != nil {
			return m, m.walkFromThread(first.SessionID, "")
		}
	case "c":
		m.leaveThread()
		return m, m.openChief(m.thread.task, scThread)
	case "T":
		if n, ok := m.threadNote(false); ok {
			m.leaveThread()
			return m, m.openRefile(n, scThread)
		}
	}
	return m, nil
}

// walkFromThread walks sid's open asks, at id when given; leaving the
// decision list returns to the thread.
func (m *Model) walkFromThread(sid, id string) tea.Cmd {
	m.leaveThread()
	for i, it := range m.thread.data.Items {
		if d := it.Decision; d != nil && d.Status == "open" && d.SessionID == sid && (id == "" || d.ID == id) {
			m.thread.cursor = i
			break
		}
	}
	m.enterSession(sid)
	if m.scr != scSession {
		return nil
	}
	m.passFrom = scThread
	m.startPass()
	if id != "" && m.p != nil && slices.Contains(m.p.order, id) && !m.p.gone[id] {
		m.show(id)
	}
	return m.loadCmd(false)
}

// refocusThread puts the list cursor on task's row.
func (m *Model) refocusThread(task string) {
	for _, t := range m.threads() {
		if t.task == task {
			m.refocus(t.value())
			return
		}
	}
}

// ---------------------------------------------------------------- refile --

type refiledMsg struct {
	note inbox.Note
	err  error
	snap snapshot
}

func (m *Model) openRefile(n inbox.Note, from screen) tea.Cmd {
	m.refileNote, m.refileFrom = n, from
	m.ti.Reset()
	m.ti.Placeholder = "task (- or empty clears)"
	m.ti.SetValue(n.Task)
	m.scr, m.flash = scRefile, ""
	return m.ti.Focus()
}

func (m *Model) keyRefile(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		ib, client, w, sessions := m.ib, m.am, m.watched(), m.snap.sessions
		id, task := m.refileNote.ID, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m.ti.Value()), "#"))
		return m, func() tea.Msg {
			n, err := ib.Refile(id, task)
			return refiledMsg{n, err, load(ib, client, w, sessions, false)}
		}
	case "esc":
		return m, m.leaveRefile("", false)
	}
	var cmd tea.Cmd
	m.ti, cmd = m.ti.Update(msg)
	return m, cmd
}

func (m *Model) onRefiled(msg refiledMsg) tea.Cmd {
	m.snap = msg.snap
	m.refreshThread()
	if msg.err != nil {
		m.flash, m.flashOK = msg.err.Error(), false
		return nil
	}
	to := "no task"
	if msg.note.Task != "" {
		to = "#" + msg.note.Task
	}
	if m.note.ID == msg.note.ID {
		m.note.Task = msg.note.Task
	}
	return m.leaveRefile(msg.note.Label()+" → "+to, true)
}

// leaveRefile returns to the screen T was pressed on; the list follows the note.
func (m *Model) leaveRefile(flash string, ok bool) tea.Cmd {
	switch m.refileFrom {
	case scThread:
		cmd := m.backToThread(flash)
		m.flashOK = ok
		return cmd
	case scNote:
		m.scr, m.flash, m.flashOK, m.flashAt = scNote, flash, ok, m.now()
		return nil
	}
	m.scr, m.flash, m.flashOK, m.flashAt = scList, flash, ok, m.now()
	m.refocus("n:" + noteValue(m.refileNote))
	return nil
}

func (m *Model) viewRefile() frame {
	head := pyfmt.Clip("refile "+m.refileNote.Label()+"  "+m.refileNote.Title, m.w)
	top := []section{{lines: []string{head}}}
	if m.flash != "" {
		top = append(top, section{lines: view.WrapRest("» "+m.flash, m.w, "", "  "), style: &bannerStyle})
	}
	return frame{s: scRefile, top: top, body: []section{{lines: []string{m.ti.View()}}}, cur: -1, help: true}
}
