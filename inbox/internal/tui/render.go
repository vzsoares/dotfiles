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

// rows is the height left for a screen: the last row is the status line.
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
	foot := m.lines([]section{{lines: footerLines(f.s, m.w, false), style: &footerStyle}})
	room := m.rows() - len(top) - len(menu) - len(foot)
	if len(body) > room {
		foot = m.lines([]section{{lines: footerLines(f.s, m.w, true), style: &footerStyle}})
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
		for len(out) < m.rows() {
			out = append(out, "")
		}
		line, style := m.status()
		out = append(out, style.Render(line))
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

// status is the one-line bottom row agent-manager shows as the terminal's
// last output.
func (m *Model) status() (string, lipgloss.Style) {
	switch {
	case m.busy != "":
		text := "» " + strings.ToLower(m.busy[:1]) + m.busy[1:]
		return pyfmt.Clip(text, max(m.w-2, 0)) + " " + m.sp.View(), busyStyle
	case len(m.snap.undelivered) > 0 || len(m.snap.failedNotes) > 0:
		var name string
		if len(m.snap.undelivered) > 0 {
			name = m.snap.undelivered[0].Decisions[0].SessionName
		} else {
			name = m.snap.failedNotes[0].SessionName
		}
		if n := len(m.snap.undelivered) + len(m.snap.failedNotes) - 1; n > 0 {
			name += " +" + strconv.Itoa(n)
		}
		return pyfmt.Clip("✕ delivery failed · "+name+" · r retry", m.w), failStyle
	case !m.loaded:
		return "○ loading…", footerStyle
	}
	unread := inbox.Unread(m.snap.notes)
	mail := "✉ " + strconv.Itoa(unread) + " unread"
	switch {
	case len(m.snap.open) == 0 && unread == 0:
		return "○ inbox empty", footerStyle
	case len(m.snap.open) == 0:
		return pyfmt.Clip(mail, m.w), footerStyle
	}
	var names []string
	for _, r := range view.Rows(m.snap.open, m.snap.sessions) {
		names = append(names, r.Name)
	}
	line := "◆ " + strconv.Itoa(len(m.snap.open)) + " open · " + strings.Join(names, ", ")
	if unread > 0 {
		line += " · " + mail
	}
	return pyfmt.Clip(line, m.w), footerStyle
}

func (m *Model) viewList() frame {
	var flash, top []string
	if m.flash != "" {
		flash = append(view.WrapRest("» "+m.flash, m.w, "", "  "), "")
	}
	for _, s := range view.StatusLines(m.snap.held, m.snap.open, m.snap.undelivered) {
		top = append(top, view.WrapRest(s, m.w, "", "    ")...)
	}
	flashStyle := &bannerStyle
	if m.flashOK {
		flashStyle = &okStyle
	}
	body := []section{{lines: flash, style: flashStyle}, {lines: top, style: &bannerStyle}}
	items := m.listItems()
	if len(items) == 0 {
		empty := "inbox empty"
		if !m.loaded {
			empty = "loading…"
		}
		return frame{s: scList, body: append(body, section{lines: []string{empty}}), cur: -1, help: true}
	}
	cur := len(flash) + len(top) + m.cursor
	first := len(items) - len(m.snap.notes)
	if first > 0 {
		c := m.cursor
		if c >= first {
			c = -1
		}
		body = append(body, menuSection(items[:first], c, first, m.w)...)
	}
	if first == len(items) {
		return frame{s: scList, body: body, cur: cur, help: true}
	}
	gap := first > 0
	if m.cursor >= first {
		cur++
		if gap {
			cur++
		}
	}
	body = append(body, m.noteSections(items[first:], m.cursor-first, gap)...)
	return frame{s: scListNotes, body: body, cur: cur, help: true}
}

func (m *Model) markerLines() []string {
	out := make([]string, len(m.markers))
	for i, mk := range m.markers {
		out[i] = pyfmt.Clip("» "+mk, m.w)
	}
	return out
}

func (m *Model) footRows(s screen) int { return len(footerLines(s, m.w, false)) }

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
	fields := [][2]string{{"action", p.Action}, {"repo", p.Repo}}
	if p.Action != inbox.RunAction {
		fields = append(fields, [2]string{"branch", p.Branch})
	}
	var body []string
	for _, kv := range fields {
		body = append(body, view.WrapRest(fmt.Sprintf("%-7s%s", kv[0], kv[1]), m.w, "", "       ")...)
	}
	if p.Action == inbox.RunAction {
		body = append(body, "command")
		body = append(body, view.Wrap(p.Cmd, m.w, "")...)
	} else {
		body = append(body, "sha", p.Sha)
	}
	body = append(body, "", "Approve? y/N")
	return frame{s: scConfirm, top: []section{{lines: []string{pyfmt.Clip(d.ID+"  prod approval", m.w)}, style: &bannerStyle}},
		body: []section{{lines: body}}, cur: -1, help: true}
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
