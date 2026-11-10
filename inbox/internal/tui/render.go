package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
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

// rows is the height left for a screen: the last row is the status line.
func (m *Model) rows() int {
	if m.h < 2 {
		return m.h
	}
	return m.h - 1
}

func (m *Model) footerRows(s screen) int { return len(m.bottom(s, false)) }

// bottom is the key footer, plus the help text when toggled with ?.
func (m *Model) bottom(s screen, scroll bool) []string {
	out := footerLines(s, m.w, scroll)
	if m.showHelp {
		out = append(helpLines(s, m.w), out...)
	}
	return out
}

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

func (m *Model) View() string {
	if m.w <= 0 || m.h <= 0 {
		return ""
	}
	var secs []section
	switch {
	case m.busy != "":
		secs = m.viewBusy()
	default:
		secs = m.viewScreen()
	}
	var out []string
	for _, s := range secs {
		for _, line := range s.lines {
			if lipgloss.Width(line) > m.w {
				line = pyfmt.Clip(line, m.w)
			}
			if s.style != nil {
				line = s.style.Render(line)
			}
			out = append(out, line)
		}
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

func (m *Model) viewScreen() []section {
	var secs []section
	switch m.scr {
	case scList:
		secs = m.viewList()
	case scSession:
		secs = m.viewSession()
	case scWalk:
		secs = m.viewWalk()
	case scFree:
		secs = m.viewFree()
	case scFull:
		secs = []section{{lines: strings.Split(m.vp.View(), "\n")},
			{lines: m.bottom(scFull, false), style: &footerStyle}}
	case scReview:
		secs = m.viewReview()
	case scChange:
		secs = m.viewChange()
	case scTalkPick:
		secs = m.viewTalkPick()
	case scTalkNote:
		secs = []section{{lines: []string{"note (optional)", pyfmt.Clip(m.ti.View(), m.w)}},
			{lines: m.bottom(scTalkNote, false), style: &footerStyle}}
	}
	return secs
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
	case len(m.snap.undelivered) > 0:
		var names []string
		for _, b := range m.snap.undelivered {
			names = append(names, b.Decisions[0].SessionName)
		}
		return pyfmt.Clip("✕ delivery failed · "+strings.Join(names, ", ")+" · r retry", m.w), failStyle
	case !m.loaded:
		return "○ loading…", footerStyle
	case len(m.snap.open) == 0:
		return "○ inbox empty", footerStyle
	}
	var names []string
	for _, r := range view.Rows(m.snap.open, m.snap.sessions) {
		names = append(names, r.Name)
	}
	return pyfmt.Clip("◆ "+strconv.Itoa(len(m.snap.open))+" open · "+strings.Join(names, ", "), m.w), footerStyle
}

func (m *Model) viewList() []section {
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
	foot := m.bottom(scList, false)
	items := m.listItems()
	rows := m.rows() - len(foot)
	if len(items) == 0 {
		empty := "inbox empty"
		if !m.loaded {
			empty = "loading…"
		}
		return []section{{lines: flash, style: flashStyle}, {lines: top, style: &bannerStyle},
			{lines: []string{empty}}, {lines: foot, style: &footerStyle}}
	}
	if len(flash)+len(top) > rows-1 {
		top = top[:max(min(len(top), rows-1-len(flash)), 0)]
		flash = flash[:max(min(len(flash), rows-1), 0)]
	}
	secs := []section{{lines: flash, style: flashStyle}, {lines: top, style: &bannerStyle}}
	secs = append(secs, menuSection(items, m.cursor, max(min(len(items), rows-len(flash)-len(top)), 1), m.w)...)
	return append(secs, section{lines: foot, style: &footerStyle})
}

func (m *Model) markerLines() []string {
	out := make([]string, len(m.markers))
	for i, mk := range m.markers {
		out[i] = pyfmt.Clip("» "+mk, m.w)
	}
	return out
}

// scrolled fits text above a menu, scrolling it when even the last variant
// overflows; it returns the text rows to show and the footer to use.
func (m *Model) scrolled(s screen, variants [][]string, items, extra int) ([]string, int, []string) {
	foot := m.bottom(s, false)
	text, menuH, scroll := fitText(variants, items, m.rows()-len(foot)-extra)
	if scroll {
		foot = m.bottom(s, true)
		text, _ = window(text, m.scroll, max(m.rows()-len(foot)-extra-menuH, 1))
	}
	return text, menuH, foot
}

func (m *Model) viewSession() []section {
	open := m.sessionOpen()
	at := m.now()
	markers := m.markerLines()
	full := view.SessionLines(open, m.snap.sessions, m.w, at, false)
	compact := view.SessionLines(open, m.snap.sessions, m.w, at, true)
	text, menuH, foot := m.scrolled(scSession, [][]string{full, compact}, len(sessionMenu), len(markers))
	secs := []section{{lines: markers, style: &bannerStyle}, {lines: text}}
	secs = append(secs, menuSection(sessionMenu, m.cursor, menuH, m.w)...)
	return append(secs, section{lines: foot, style: &footerStyle})
}

func (m *Model) walkFit() (walkFit, inbox.Decision, []string) {
	d, _ := m.decision(m.p.cur)
	foot := footerLines(scWalk, m.w, false)
	help := 0
	if m.showHelp {
		help = len(helpLines(scWalk, m.w))
	}
	f := fitWalk(d, m.brief(d), m.banner(), m.w, m.rows(), len(questionMenu(d)), len(foot), help)
	if f.scroll {
		foot = footerLines(scWalk, m.w, true)
		f.rows = max(m.rows()-len(foot)-f.menuH, 1)
	}
	return f, d, foot
}

func (m *Model) viewWalk() []section {
	f, d, foot := m.walkFit()
	body := f.body
	if f.scroll {
		body, _ = window(body, m.scroll, f.rows)
	}
	top := f.top
	nb := len(m.banner())
	if nb > len(top) {
		nb = len(top)
	}
	secs := []section{{lines: top[:nb], style: &bannerStyle}, {lines: top[nb:]}, {lines: body}}
	secs = append(secs, menuSection(questionMenu(d), m.cursor, f.menuH, m.w)...)
	if f.help && m.showHelp {
		secs = append(secs, section{lines: helpLines(scWalk, m.w), style: &footerStyle})
	}
	return append(secs, section{lines: foot, style: &footerStyle})
}

func (m *Model) viewFree() []section {
	d, _ := m.decision(m.p.cur)
	head := pyfmt.Clip(d.ID+"  "+d.Question, m.w)
	return []section{{lines: []string{head}}, {lines: strings.Split(m.ta.View(), "\n")},
		{lines: m.bottom(scFree, false), style: &footerStyle}}
}

func (m *Model) viewReview() []section {
	entries := m.entries()
	banner := m.banner()
	full := reviewLines(entries, m.w, -1)
	compact := reviewLines(entries, m.w, m.w/2)
	text, menuH, foot := m.scrolled(scReview, [][]string{full, compact}, len(reviewMenu), len(banner))
	secs := []section{{lines: banner, style: &bannerStyle}, {lines: text}}
	secs = append(secs, menuSection(reviewMenu, m.cursor, menuH, m.w)...)
	return append(secs, section{lines: foot, style: &footerStyle})
}

func (m *Model) viewChange() []section {
	var items []item
	for _, e := range m.entries() {
		items = append(items, item{e.d.ID + "  " + e.d.Question, e.d.ID})
	}
	items = append(items, item{"Back", ""})
	foot := m.bottom(scChange, false)
	n := max(min(len(items), m.rows()-len(foot)-1), 1)
	secs := []section{{lines: []string{"Change which answer?"}}}
	secs = append(secs, menuSection(items, m.cursor, n, m.w)...)
	return append(secs, section{lines: foot, style: &footerStyle})
}

func (m *Model) viewTalkPick() []section {
	head := view.Wrap("Which did the talk settle? (space marks, none leaves all open)", m.w, "")
	var items []item
	for _, d := range m.talkOpen {
		box := "[ ] "
		if m.marked[d.ID] {
			box = "[x] "
		}
		items = append(items, item{box + d.ID + "  " + d.Question, d.ID})
	}
	foot := m.bottom(scTalkPick, false)
	n := max(min(len(items), m.rows()-len(foot)-len(head)), 1)
	secs := []section{{lines: head}}
	secs = append(secs, menuSection(items, m.cursor, n, m.w)...)
	return append(secs, section{lines: foot, style: &footerStyle})
}
