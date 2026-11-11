package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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
	if f.help && m.showHelp {
		body = append(body, m.lines([]section{{lines: helpLines(f.s, m.w), style: &footerStyle}})...)
	}
	menu := m.lines(f.menu)
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
		return frame{s: scFull, body: []section{{lines: m.fullLines(d)}}, cur: -1, help: true}
	case scReview:
		return m.viewReview()
	case scJump:
		return m.viewJump()
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
	case scTalkNote:
		return frame{s: scTalkNote, top: []section{{lines: []string{"note (optional)"}}},
			body: []section{{lines: []string{m.ti.View()}}}, cur: -1, help: true}
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
	case m.chief.id != "":
		return pyfmt.Clip(name+m.chiefStatusLine(), m.w), headerStyle
	case !m.loaded:
		return pyfmt.Clip(name+"loading…", m.w), headerStyle
	}
	parts := []string{"cockpit"}
	if n := len(m.snap.open); n > 0 {
		parts = append(parts, strconv.Itoa(n)+" open")
	}
	prod := 0
	for _, d := range m.snap.open {
		if d.Prod != nil {
			prod++
		}
	}
	if prod > 0 {
		parts = append(parts, strconv.Itoa(prod)+" prod")
	}
	if n := inbox.Unread(m.snap.notes); n > 0 {
		parts = append(parts, "✉ "+strconv.Itoa(n)+" unread")
	}
	if n := len(m.snap.held); n > 0 {
		parts = append(parts, strconv.Itoa(n)+" held")
	}
	if len(parts) == 1 {
		parts = append(parts, "all clear")
	}
	return pyfmt.Clip(strings.Join(parts, " · "), m.w), headerStyle
}

// group appends a labelled block to body, a blank row apart from the last.
func group(body []section, head string, style *lipgloss.Style) []section {
	if count(body) > 0 {
		body = append(body, section{lines: []string{""}})
	}
	return append(body, section{lines: []string{head}, style: style})
}

// viewList is the cockpit: failed deliveries, asks, notes and held answers,
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
	rows, prod := m.askRows()
	nFail := len(m.snap.undelivered) + len(m.snap.failedNotes)
	cur, i := -1, 0
	add := func(n int, style func(j int) *lipgloss.Style) {
		for j := 0; j < n; j, i = j+1, i+1 {
			prefix, st := "  ", style(j)
			if i == m.cursor {
				prefix, st, cur = "› ", &cursorStyle, count(body)
			}
			body = append(body, section{lines: []string{prefix + items[i].label}, style: st})
		}
	}
	if nFail > 0 {
		body = group(body, "Failed delivery · "+strconv.Itoa(nFail), &failStyle)
		add(nFail, func(int) *lipgloss.Style { return &failStyle })
	}
	if len(rows) > 0 {
		head := "Asks · " + strconv.Itoa(len(m.snap.open)) + " open"
		body = group(body, head, &bannerStyle)
		add(len(rows), func(j int) *lipgloss.Style {
			if prod[rows[j].SessionID] {
				return &failStyle
			}
			return nil
		})
	}
	if len(m.snap.notes) > 0 && m.notesFolded {
		prefix, st := "  ", &readStyle
		if inbox.Unread(m.snap.notes) > 0 {
			st = &bannerStyle
		}
		if i == m.cursor {
			prefix, st = "› ", &cursorStyle
		}
		body = group(body, prefix+m.notesHead(), st)
		if i == m.cursor {
			cur = count(body) - 1
		}
		i++
	} else if len(m.snap.notes) > 0 {
		body = group(body, m.notesHead(), &bannerStyle)
		add(len(m.snap.notes), func(j int) *lipgloss.Style {
			if m.snap.notes[j].Unread() {
				return &unreadStyle
			}
			return &readStyle
		})
	}
	if len(held) > 0 {
		body = group(body, "Held · sent when the session's asks close", &bannerStyle)
		for _, h := range held {
			body = append(body, section{lines: view.WrapRest("  "+h, m.w, "", "    "), style: &readStyle})
		}
	}
	return frame{s: scList, body: body, cur: cur, help: true}
}

// emptySections centres the nothing-waiting message in the rows left above
// the footer.
func (m *Model) emptySections(used int) []section {
	msg := []string{"Nothing waiting for you"}
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
	markers := m.markerLines()
	text := view.PanelSessionLines(m.sessionOpen(), m.snap.sessions, m.w, m.now(), m.paint)
	help := 0
	if m.showHelp {
		help = len(helpLines(scSession, m.w))
	}
	menuH := menuRows(len(sessionMenu), len(markers)+len(text)+help, m.rows()-m.footRows(scSession))
	return frame{s: scSession, body: []section{{lines: markers, style: &bannerStyle}, {lines: text}},
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
	head := pyfmt.Clip(d.ID+"  "+d.Question, m.w)
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
	full := reviewLines(entries, m.w, -1)
	compact := reviewLines(entries, m.w, m.w/2)
	help := 0
	if m.showHelp {
		help = len(helpLines(scReview, m.w))
	}
	text, menuH := fitText([][]string{full, compact}, len(reviewMenu),
		m.rows()-m.footRows(scReview)-len(banner)-help)
	return frame{s: scReview, body: []section{{lines: banner, style: &bannerStyle}, {lines: text}},
		menu: menuSection(reviewMenu, m.cursor, menuH, m.w), cur: -1, help: true}
}

// viewConfirm shows the exact prod action an Approve binds; the sha and a
// run's command get lines of their own so all of them fit a narrow pane.
func (m *Model) viewConfirm() frame {
	d, _ := m.decision(m.p.cur)
	var p inbox.ProdAction
	if d.Prod != nil {
		p = *d.Prod
	}
	run := p.Action == inbox.RunAction
	fields := [][2]string{{"action", p.Action}, {"repo", p.Repo}}
	if !run {
		fields = append(fields, [2]string{"branch", p.Branch})
	}
	var body []string
	for _, kv := range fields {
		body = append(body, view.WrapRest(fmt.Sprintf("%-7s%s", kv[0], kv[1]), m.w, "", "       ")...)
	}
	if run {
		body = append(body, "command")
		body = append(body, wrapCmd(p.Cmd, max(m.w, view.MinWidth))...)
	} else {
		body = append(body, "sha", p.Sha)
	}
	body = append(body, "", "Approve? y/N")
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

func (m *Model) viewJump() frame {
	items := m.jumpItems()
	return frame{s: scJump, top: []section{{lines: []string{"Jump to which question?"}}},
		body: menuSection(items, m.cursor, len(items), m.w), cur: m.cursor}
}

func (m *Model) viewTalkPick() frame {
	head := view.Wrap("Which did the talk settle? (space marks, none leaves all open)", m.w, "")
	var items []item
	for _, d := range m.talkOpen {
		box := "[ ] "
		if m.marked[d.ID] {
			box = "[x] "
		}
		items = append(items, item{box + d.ID + "  " + d.Question, d.ID})
	}
	return frame{s: scTalkPick, top: []section{{lines: head}},
		body: menuSection(items, m.cursor, len(items), m.w), cur: m.cursor}
}
