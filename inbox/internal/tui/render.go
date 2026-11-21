package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/md"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

// ANSI 16-colour indexes only, so the panel follows the terminal theme.
var (
	cursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	bannerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	footerStyle = lipgloss.NewStyle().Faint(true)
	busyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	failStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	headerStyle = lipgloss.NewStyle().Bold(true)
	keyStyle    = lipgloss.NewStyle().Bold(true)
	sepStyle    = lipgloss.NewStyle().Faint(true)
)

type section struct {
	lines []string
	style *lipgloss.Style
}

// frame is one screen: top stays pinned, body scrolls, and the menu and key
// footer stay pinned below it.
type frame struct {
	s    screen
	top  []section
	body []section
	menu []section
	// cur is the body row to keep in view after the cursor moves, or -1.
	cur int
	// help puts the ? text at the end of the body.
	help bool
}

// rows is the height left for a screen: the first row is the header.
func (m *Model) rows() int {
	if m.h < 2 {
		return m.h
	}
	return m.h - 1
}

func (m *Model) paint(k md.Key, text string) []string { return m.md.Cached(k, text) }

func (m *Model) banner() []string {
	if m.copied != "" {
		return []string{"» " + m.copied}
	}
	if m.p == nil || len(m.p.notes) == 0 {
		return nil
	}
	return []string{pyfmt.Clip("» "+m.p.notes[len(m.p.notes)-1], max(m.w, view.MinWidth))}
}

func menuSection(items []item, cursor, n, width int) []section {
	rows := menuWindow(items, cursor, n, width)
	out := make([]section, len(rows))
	for i, r := range rows {
		out[i] = section{lines: []string{r}}
		if strings.HasPrefix(r, "› ") {
			out[i].style = &cursorStyle
		}
	}
	return out
}

func count(secs []section) int {
	n := 0
	for _, s := range secs {
		n += len(s.lines)
	}
	return n
}

// menuRows is the menu height: every item when that leaves the body
// min(body, 3) rows, else what is left, at least one.
func menuRows(items, body, room int) int {
	return max(min(items, room-min(body, 3)), 1)
}

// clip cuts a line, painted or not, to width cells and closes its colour.
func clip(line string, width int) string {
	if lipgloss.Width(line) <= width {
		return line
	}
	out := ansi.Truncate(line, width, "…")
	if strings.Contains(out, "\x1b") {
		out += "\x1b[0m"
	}
	return out
}

func (m *Model) lines(secs []section) []string {
	var out []string
	for _, s := range secs {
		for _, line := range s.lines {
			line = clip(line, m.w)
			if s.style != nil {
				line = s.style.Render(line)
			}
			out = append(out, line)
		}
	}
	return out
}

// span is the body rows [start, end) shown in room rows from off; a marker
// row is taken at each clipped edge. cur, when >= 0, is kept in view.
func span(n, room, off, cur int) (int, int) {
	// A footer wider than a tiny pane leaves room negative.
	room = max(room, 0)
	if n <= room {
		return 0, n
	}
	if room < 3 {
		if cur >= 0 {
			off = max(min(off, cur), cur-room+1)
		}
		off = max(min(off, n-room), 0)
		return off, off + room
	}
	bounds := func(off int) (int, int) {
		k := room
		if off > 0 {
			k--
		}
		if off+k < n {
			k--
		}
		return off, off + k
	}
	off = max(min(off, n-room+1), 0)
	if cur >= 0 {
		off = min(off, cur)
		for {
			if _, end := bounds(off); cur < end || end >= n {
				break
			}
			off++
		}
	}
	return bounds(off)
}

// layout is the screen's rows with the body cut to the pane, the body offset
// it settled on and the body rows shown.
func (m *Model) layout(follow bool) ([]string, int, int) {
	f := m.frame()
	top := m.lines(f.top)
	body := m.lines(f.body)
	menu := m.lines(f.menu)
	m.blocks = nil
	if f.s == scConfirmManual {
		// y confirms here, so a "[n] copy" label would invite a wrong y.
		body = m.md.Unlabel(body)
	}
	top, m.blocks = m.md.Number(top, m.blocks)
	body, m.blocks = m.md.Number(body, m.blocks)
	menu, m.blocks = m.md.Number(menu, m.blocks)
	if f.help && m.showHelp {
		body = append(body, m.lines([]section{{lines: helpLines(f.s, m.w), style: &footerStyle}})...)
	}
	foot := m.lines([]section{{lines: m.footer(f.s, false)}})
	room := m.rows() - len(top) - len(menu) - len(foot)
	if len(body) > room {
		foot = m.lines([]section{{lines: m.footer(f.s, true)}})
		room = m.rows() - len(top) - len(menu) - len(foot)
	}
	cur := -1
	if follow {
		cur = f.cur
	}
	start, end := span(len(body), room, m.scroll, cur)
	out := append([]string(nil), top...)
	if start > 0 {
		out = append(out, footerStyle.Render(clip("↑ "+strconv.Itoa(start)+" more", m.w)))
	}
	out = append(out, body[start:end]...)
	if end < len(body) {
		out = append(out, footerStyle.Render(clip("↓ "+strconv.Itoa(len(body)-end)+" more", m.w)))
	}
	out = append(out, menu...)
	// The footer stays on the last rows.
	for len(out)+len(foot) < m.rows() {
		out = append(out, "")
	}
	return append(out, foot...), start, end - start
}

func (m *Model) View() string {
	if m.w <= 0 || m.h <= 0 {
		return ""
	}
	var out []string
	if m.busy != "" {
		out = m.lines(m.viewBusy())
	} else {
		out, _, _ = m.layout(false)
	}
	if len(out) > m.rows() {
		out = out[:m.rows()]
	}
	if m.h >= 2 {
		out = append([]string{m.headerLine()}, out...)
	}
	return strings.Join(out, "\n")
}

func (m *Model) frame() frame {
	switch m.scr {
	case scList:
		return m.viewList()
	case scSession:
		return m.viewSession()
	case scWalk:
		return m.viewWalk()
	case scFree:
		return m.viewFree()
	case scFull:
		d, _ := m.decision(m.p.cur)
		return frame{s: scFull, top: []section{{lines: m.copiedLines(), style: &bannerStyle}}, body: []section{{lines: m.fullLines(d)}}, cur: -1, help: true}
	case scReview:
		return m.viewReview()
	case scJump:
		return m.viewJump()
	case scAll:
		return m.viewAll()
	case scTalkWho:
		return m.viewTalkWho()
	case scTalkPick:
		return m.viewTalkPick()
	case scConfirm:
		return m.viewConfirm()
	case scNote:
		return m.viewNote()
	case scReply:
		return m.viewReply()
	case scChief:
		return m.viewChief()
	case scThread:
		return m.viewThread()
	case scRefile:
		return m.viewRefile()
	case scReviewNote:
		return frame{s: scReviewNote, top: []section{{lines: []string{"note for all " + strconv.Itoa(m.p.answers()) + " answers (optional)"}}},
			body: []section{{lines: strings.Split(m.ti.View(), "\n")}}, cur: -1, help: true}
	case scTalkNote:
		return frame{s: scTalkNote, top: []section{{lines: []string{"note (optional)"}}},
			body: []section{{lines: strings.Split(m.ti.View(), "\n")}}, cur: -1, help: true}
	}
	return frame{s: m.scr, cur: -1}
}

// viewBusy is the whole screen while a send, retry or talk is in flight: no
// menu, cursor or key hints, since keys are ignored until it finishes.
func (m *Model) viewBusy() []section {
	return []section{{lines: view.WrapRest(m.sp.View()+" "+m.busy, m.w, "", "  "), style: &busyStyle}}
}

// headerLine is the header with the Claude quota on its right when both fit.
func (m *Model) headerLine() string {
	line, style := m.header()
	left := style.Render(line)
	gap := 2
	q := m.quota.text(m.now(), m.loc, m.w-lipgloss.Width(line)-gap)
	if q == "" {
		return left
	}
	return left + strings.Repeat(" ", m.w-lipgloss.Width(line)-lipgloss.Width(q)) + q
}

// header is the top row: what needs attention now, else the counts.
func (m *Model) header() (string, lipgloss.Style) {
	const name = "cockpit · "
	switch {
	case m.busy != "":
		text := name + "» " + strings.ToLower(m.busy[:1]) + m.busy[1:]
		return pyfmt.Clip(text, max(m.w-2, 0)) + " " + m.sp.View(), busyStyle
	case len(m.snap.undelivered) > 0 || len(m.snap.failedNotes) > 0:
		var who string
		if len(m.snap.undelivered) > 0 {
			who = m.snap.undelivered[0].Decisions[0].SessionName
		} else {
			who = m.snap.failedNotes[0].SessionName
		}
		if n := len(m.snap.undelivered) + len(m.snap.failedNotes) - 1; n > 0 {
			who += " +" + strconv.Itoa(n)
		}
		return pyfmt.Clip(name+"✕ delivery failed · "+who+" · r retry", m.w), failStyle
	case len(m.waitRows()) > 0:
		return m.waitingHeader(name)
	case m.chief.id != "":
		return pyfmt.Clip(name+m.chiefStatusLine(), m.w), headerStyle
	case !m.loaded:
		return pyfmt.Clip(name+"loading…", m.w), headerStyle
	}
	parts := []string{"cockpit"}
	prod := 0
	for _, d := range m.snap.open {
		if d.Prod != nil {
			prod++
		}
	}
	if prod > 0 {
		parts = append(parts, strconv.Itoa(prod)+" prod")
	}
	if n := len(m.snap.held); n > 0 {
		parts = append(parts, strconv.Itoa(n)+" held")
	}
	if d, n := m.inboxCount(); len(parts) == 1 && d+n == 0 {
		parts = append(parts, "all clear")
	}
	return pyfmt.Clip(strings.Join(parts, " · "), m.w), headerStyle
}

// loudAfter is how long a target may be down before the panel says so in
// red and with a banner.
const loudAfter = 15 * time.Minute

// waitedLong is whether stamp is loudAfter old or more.
func (m *Model) waitedLong(stamp string) bool {
	t, ok := inbox.ParseStamp(stamp)
	return ok && m.now().Sub(t) >= loudAfter
}

// waitingHeader names the first target that is down, how many more there
// are and how much is queued; red once the oldest item waited loudAfter.
func (m *Model) waitingHeader(name string) (string, lipgloss.Style) {
	rows := m.waitRows()
	if !slices.ContainsFunc(rows, func(r waitRow) bool { return !r.behind }) {
		return pyfmt.Clip(name+"◷ "+rows[0].reason+" · "+strconv.Itoa(rows[0].n)+" held", m.w), headerStyle
	}
	var names []string
	queued, oldest := 0, rows[0].oldest
	for _, r := range rows {
		if !slices.Contains(names, r.name) {
			names = append(names, r.name)
		}
		queued += r.n
		oldest = min(oldest, r.oldest)
	}
	who := names[0]
	if len(names) > 1 {
		who += " +" + strconv.Itoa(len(names)-1)
	}
	n := strconv.Itoa(queued) + " queued"
	if m.waitedLong(oldest) {
		return pyfmt.Clip(name+"✕ "+who+" down "+view.Age(oldest, m.now())+" · "+n+" · r retry", m.w), failStyle
	}
	return pyfmt.Clip(name+"◷ "+who+" is down · "+n+" · sends when back", m.w), bannerStyle
}

// nag posts one banner per target per outage once its oldest waiting item
// is loudAfter old; an SSH panel posts none.
func (m *Model) nag() []tea.Cmd {
	type down struct {
		n      int
		oldest string
	}
	byName := map[string]down{}
	var order []string
	for _, r := range m.waitRows() {
		if r.behind {
			continue
		}
		d, ok := byName[r.name]
		if !ok {
			order = append(order, r.name)
			d.oldest = r.oldest
		}
		byName[r.name] = down{d.n + r.n, min(d.oldest, r.oldest)}
	}
	for name := range m.nagged {
		if _, ok := byName[name]; !ok {
			delete(m.nagged, name)
		}
	}
	var cmds []tea.Cmd
	for _, name := range order {
		d := byName[name]
		if m.nagged[name] || !m.waitedLong(d.oldest) {
			continue
		}
		m.nagged[name] = true
		if am.Remote() {
			continue
		}
		client, title := m.am, "zen-inbox · "+name+" still down"
		body := strconv.Itoa(d.n) + " queued · oldest " + view.Age(d.oldest, m.now())
		cmds = append(cmds, func() tea.Msg { client.Notify(title, body); return nil })
	}
	return cmds
}

// group appends a labelled block to body, a blank row apart from the last.
func group(body []section, head string, style *lipgloss.Style) []section {
	if count(body) > 0 {
		body = append(body, section{lines: []string{""}})
	}
	return append(body, section{lines: []string{head}, style: style})
}

// viewList is the cockpit: failed deliveries, threads, notes without a task
// and held answers,
// in that order, each under its label; the rows follow listItems' order.
func (m *Model) viewList() frame {
	var body []section
	if m.flash != "" {
		style := &bannerStyle
		if m.flashOK {
			style = &okStyle
		}
		body = append(body, section{lines: view.WrapRest("» "+m.flash, m.w, "", "  "), style: style})
	}
	items := m.listItems()
	held := view.HeldLines(m.snap.held, m.snap.open)
	if len(items) == 0 && len(held) == 0 {
		return frame{s: scList, body: append(body, m.emptySections(count(body))...), cur: -1, help: true}
	}
	rows := m.listRows()
	heads := map[int]string{
		gFailed:  "Failed delivery · " + strconv.Itoa(len(m.snap.undelivered)+len(m.snap.failedNotes)),
		gWaiting: "Waiting · sent when the session is back",
		gThreads: "Threads · " + strconv.Itoa(len(m.threads())),
		gNoTask:  m.notesHead(),
	}
	headStyle := map[int]*lipgloss.Style{gFailed: &failStyle, gWaiting: &bannerStyle, gThreads: &bannerStyle, gNoTask: &bannerStyle}
	cur, last := -1, -1
	for i, r := range rows {
		prefix, st := "  ", r.style
		if i == m.cursor {
			prefix, st = "› ", &cursorStyle
		}
		// Folded, the No task header is the row itself.
		if r.value == "nh:" {
			if st == nil {
				st = &readStyle
				if inbox.Unread(m.looseNotes()) > 0 {
					st = &bannerStyle
				}
			}
			body = group(body, prefix+r.label, st)
		} else {
			if r.group != last && r.group != gInbox {
				body = group(body, heads[r.group], headStyle[r.group])
			}
			body = append(body, section{lines: []string{prefix + r.label}, style: st})
		}
		last = r.group
		if i == m.cursor {
			cur = count(body) - 1
		}
	}
	if len(held) > 0 {
		body = group(body, "Held · sent when the task's asks close", &bannerStyle)
		for _, h := range held {
			body = append(body, section{lines: view.WrapRest("  "+h, m.w, "", "    "), style: &readStyle})
		}
	}
	return frame{s: scList, body: body, cur: cur, help: true}
}

// emptySections centres the nothing-waiting message in the rows left above
// the footer.
func (m *Model) emptySections(used int) []section {
	msg := []string{"All clear · nothing to act on"}
	if !m.loaded {
		msg = []string{"loading…"}
	} else if m.snap.lastClosed != "" {
		msg = append(msg, "last item closed "+view.Age(m.snap.lastClosed, m.now())+" ago")
	}
	room := m.rows() - used - m.footRows(scList)
	if m.showHelp {
		room -= len(helpLines(scList, m.w))
	}
	var out []section
	if pad := (room - len(msg)) / 2; pad > 0 {
		out = append(out, section{lines: make([]string, pad)})
	}
	for i, line := range msg {
		style := &readStyle
		if i == 0 {
			style = &headerStyle
		}
		indent := strings.Repeat(" ", max((m.w-lipgloss.Width(line))/2, 0))
		out = append(out, section{lines: []string{indent + line}, style: style})
	}
	return out
}

func (m *Model) markerLines() []string {
	out := make([]string, len(m.markers))
	for i, mk := range m.markers {
		out[i] = pyfmt.Clip("» "+mk, m.w)
	}
	return out
}

func (m *Model) footRows(s screen) int { return len(m.footer(s, false)) }

// footer is the screen's key rows; the list's follow the cursor.
func (m *Model) footer(s screen, scroll bool) []string {
	if s != scList {
		return footerLines(s, m.w, scroll)
	}
	keys := m.listKeys()
	if scroll {
		keys = append(keys, ranked{scrollKey(s), 8})
	}
	return []string{paintKeys(fitKeys(keys, m.w))}
}

func (m *Model) viewSession() frame {
	copied := m.copiedLines()
	markers := m.markerLines()
	text := view.PanelSessionLines(m.sessionOpen(), m.snap.sessions, m.w, m.now(), m.paint)
	help := 0
	if m.showHelp {
		help = len(helpLines(scSession, m.w))
	}
	menuH := menuRows(len(sessionMenu), len(markers)+len(text)+help, m.rows()-m.footRows(scSession)-len(copied))
	return frame{s: scSession, top: []section{{lines: copied, style: &bannerStyle}},
		body: []section{{lines: markers, style: &bannerStyle}, {lines: text}},
		menu: menuSection(sessionMenu, m.cursor, menuH, m.w), cur: -1, help: true}
}

// walkKeys is the screen whose footer and help the walk shows.
func (m *Model) walkKeys(d inbox.Decision) screen {
	if m.locked(d) {
		return scLocked
	}
	return scWalk
}

func (m *Model) walkFit() (walkFit, inbox.Decision) {
	d, _ := m.decision(m.p.cur)
	s := m.walkKeys(d)
	help := 0
	if m.showHelp {
		help = len(helpLines(s, m.w))
	}
	f := fitWalk(d, m.brief(d), m.banner(), m.notice(d), m.w, m.rows(), len(questionMenu(d, m.locked(d))), m.footRows(s),
		help, m.paint)
	return f, d
}

func (m *Model) viewWalk() frame {
	f, d := m.walkFit()
	top := f.top
	nb := min(len(m.banner()), len(top))
	return frame{s: m.walkKeys(d), top: []section{{lines: top[:nb], style: &bannerStyle}, {lines: top[nb:]}},
		body: []section{{lines: f.body}}, menu: menuSection(questionMenu(d, m.locked(d)), m.cursor, f.menuH, m.w),
		cur: -1, help: f.help}
}

func (m *Model) viewFree() frame {
	d, _ := m.decision(m.p.cur)
	head := pyfmt.Clip(d.ID+"  "+md.Plain(d.Question), m.w)
	s := scFree
	if pyfmt.Blank(m.ta.Value()) {
		s = scFreeBlank
	}
	var flash []string
	if m.flash != "" {
		flash = view.WrapRest("» "+m.flash, m.w, "", "  ")
	}
	return frame{s: s, top: []section{{lines: []string{head}}, {lines: flash, style: &bannerStyle}},
		body: []section{{lines: strings.Split(m.ta.View(), "\n")}}, cur: -1, help: true}
}

func (m *Model) viewReview() frame {
	entries := m.entries()
	banner := m.banner()
	full := reviewLines(entries, m.w, m.paint)
	compact := reviewLines(entries, m.w, nil)
	help := 0
	if m.showHelp {
		help = len(helpLines(scReview, m.w))
	}
	text, menuH := fitText([][]string{full, compact}, len(reviewMenu),
		m.rows()-m.footRows(scReview)-len(banner)-help)
	return frame{s: scReview, body: []section{{lines: banner, style: &bannerStyle}, {lines: text}},
		menu: menuSection(reviewMenu, m.cursor, menuH, m.w), cur: -1, help: true}
}

// viewConfirm shows what the typed y binds. A manual ask: the step again,
// scrolling over the pinned prompt. A prod ask: the exact action; the sha and
// a run's command get lines of their own so all of them fit a narrow pane.
func (m *Model) viewConfirm() frame {
	d, _ := m.decision(m.p.cur)
	if d.Kind == inbox.KindManual {
		return frame{s: scConfirmManual, top: []section{{lines: []string{pyfmt.Clip(d.ID+"  manual step", m.w)}, style: &bannerStyle}},
			body: []section{{lines: view.PanelAskLines(d, m.w, m.paint)}},
			menu: []section{{lines: []string{"", "Did you run it? type y"}, style: &bannerStyle}}, cur: -1, help: true}
	}
	var p inbox.ProdAction
	if d.Prod != nil {
		p = *d.Prod
	}
	body := append(prodLines(p, m.w, true), "", "Approve? y/N")
	return frame{s: scConfirm, top: []section{{lines: []string{pyfmt.Clip(d.ID+"  prod approval", m.w)}, style: &bannerStyle}},
		body: []section{{lines: body}}, cur: -1, help: true}
}

// cmdSeps are where a token wider than the pane may break: after the character.
const cmdSeps = "/,=:;|&"

// wrapCmd breaks cmd at spaces, keeping runs of them inside a line. A token
// wider than width breaks after its last separator that fits, else at width.
func wrapCmd(cmd string, width int) []string {
	var out []string
	for _, para := range strings.Split(cmd, "\n") {
		line := []rune{}
		for _, tok := range strings.Split(para, " ") {
			t := []rune(tok)
			if len(line) > 0 && len(line)+1+len(t) <= width {
				line = append(append(line, ' '), t...)
				continue
			}
			if len(line) > 0 {
				out = append(out, string(line))
				line = line[:0]
			}
			for len(t) > width {
				cut := width
				if i := strings.LastIndexAny(string(t[:width]), cmdSeps); i >= 0 {
					cut = len([]rune(string(t[:width])[:i+1]))
				}
				out = append(out, string(t[:cut]))
				t = t[cut:]
			}
			line = append(line, t...)
		}
		out = append(out, string(line))
	}
	return out
}

// viewJump is the session's decision list: open ones in a menu with their
// draft markers, then those closed meanwhile, dimmed.
func (m *Model) viewJump() frame {
	items := m.jumpItems()
	k, n := m.progress(m.sid)
	head := pyfmt.Clip(m.sname+" · decisions · "+strconv.Itoa(k)+"/"+strconv.Itoa(n)+" drafted", m.w)
	body := menuSection(items, m.cursor, len(items), m.w)
	if closed := m.closedItems(); len(closed) > 0 {
		body = append(body, section{lines: append([]string{""}, closed...), style: &footerStyle})
	}
	return frame{s: scJump, top: []section{{lines: []string{head}, style: &headerStyle}, {lines: m.banner(), style: &bannerStyle}},
		body: body, cur: m.cursor, help: true}
}

func (m *Model) viewTalkPick() frame {
	head := view.Wrap("Which did the talk settle? (space marks, none leaves all open)", m.w, "")
	return frame{s: scTalkPick, top: []section{{lines: head}},
		body: m.markMenu(m.talkOpen), cur: m.cursor}
}

// markMenu is a [ ]/[x] menu of decisions, checked from m.marked.
func (m *Model) markMenu(ds []inbox.Decision) []section {
	var items []item
	for _, d := range ds {
		box := "[ ] "
		if m.marked[d.ID] {
			box = "[x] "
		}
		items = append(items, item{box + d.ID + "  " + md.Plain(d.Question), d.ID})
	}
	return menuSection(items, m.cursor, len(items), m.w)
}

// prodLines is a prod ask's action; the sha and a run's command get lines of
// their own so all of them fit a narrow pane. Without tail a run's command
// text is left to the caller.
func prodLines(p inbox.ProdAction, width int, tail bool) []string {
	run := p.Action == inbox.RunAction
	fields := [][2]string{{"action", p.Action}, {"repo", p.Repo}}
	if !run {
		fields = append(fields, [2]string{"branch", p.Branch})
	}
	var out []string
	for _, kv := range fields {
		out = append(out, view.WrapRest(fmt.Sprintf("%-7s%s", kv[0], kv[1]), width, "", "       ")...)
	}
	if run {
		out = append(out, "command")
		if !tail {
			return out
		}
		return append(out, wrapCmd(p.Cmd, max(width, view.MinWidth))...)
	}
	return append(out, "sha", p.Sha)
}

// viewAll is every open decision of the session on one scrolling screen, the
// highlighted one marked with ›.
func (m *Model) viewAll() frame {
	open := m.sessionOpen()
	w := max(m.w-2, view.MinWidth)
	var body []string
	cur := -1
	for i, d := range open {
		if i > 0 {
			body = append(body, "")
		}
		lines := view.PanelQuestionLines(d, w, m.paint)
		if d.Prod != nil {
			run := d.Prod.Action == inbox.RunAction
			for _, l := range prodLines(*d.Prod, w-2, !run) {
				lines = append(lines, "  "+l)
			}
			lines = append(lines, view.PanelCmdLines(d, w, m.paint)...)
		}
		lines = append(lines, view.PanelBodyLines(d, w, m.paint)...)
		lines = append(lines, view.PanelContextLines(d, w, m.paint)...)
		lines = append(lines, view.PanelOptionLines(d, w, m.paint)...)
		if mark, text := m.draftText(d); text != "" {
			lines = append(lines, view.Painted(m.paint, md.Key{ID: d.ID, Rev: d.Rev, Field: "draft"}, text, w, mark+" draft: ", "  ")...)
		}
		if i == m.cursor {
			cur = len(body)
		}
		for j, l := range lines {
			lead := "  "
			if i == m.cursor && j == 0 {
				lead = cursorStyle.Render("›") + " "
			}
			body = append(body, lead+l)
		}
	}
	head := pyfmt.Clip("Review all · "+m.sname+" · "+strconv.Itoa(len(open))+" open", m.w)
	return frame{s: scAll, top: []section{{lines: []string{head}, style: &headerStyle}, {lines: m.copiedLines(), style: &bannerStyle}},
		body: []section{{lines: body}}, cur: cur, help: true}
}

func (m *Model) viewTalkWho() frame {
	head := view.Wrap("Talk about which? (space marks, none means all)", m.w, "")
	return frame{s: scTalkWho, top: []section{{lines: head}},
		body: m.markMenu(m.sessionOpen()), cur: m.cursor}
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
