package tui

import (
	"hash/fnv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

// Item types, most urgent first; every list row and header leads with one.
const (
	typeApproval = "approval"
	typeHuman    = "human action"
	typeGate     = "gate"
	typeDecision = "decision"
	typeNote     = "note"
)

// colour is one adaptive foreground: a light shade on dark terminals, a deep
// one on light terminals, so text reads on either.
func colour(dark, light string) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Dark: dark, Light: light})
}

// typeStyles give each type one fixed colour.
var typeStyles = map[string]lipgloss.Style{
	typeApproval: colour("203", "124"),
	typeHuman:    colour("220", "130"),
	typeGate:     colour("213", "90"),
	typeDecision: colour("120", "22"),
	typeNote:     colour("75", "26"),
}

// projectPalette holds the hues a project name hashes into.
var projectPalette = []lipgloss.Style{
	colour("209", "166"), colour("114", "28"), colour("111", "25"), colour("176", "91"),
	colour("179", "94"), colour("80", "30"), colour("204", "125"), colour("146", "61"),
}

// projectNameMax is the widest project name a lead shows.
const projectNameMax = 16

// lead is "{project} ● {type}": the name in its stable colour, then the type in
// its fixed one. With no project it is the type alone, with no dot.
func lead(project, typ string) string {
	out := typeStyles[typ].Render(typ)
	if project == "" {
		return out
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(project)))
	name := projectPalette[int(h.Sum32())%len(projectPalette)].Render(pyfmt.Clip(project, projectNameMax))
	return name + " ● " + out
}

// projectOf is a task's board project tag, "" while unknown.
func (m *Model) projectOf(task string) string { return m.snap.projects[task] }

// typeOf is an ask's type.
func typeOf(d inbox.Decision) string {
	switch {
	case d.Prod != nil:
		return typeApproval
	case d.Kind == inbox.KindManual:
		return typeHuman
	case d.Gate:
		return typeGate
	}
	return typeDecision
}

// rowType is a session row's most urgent type.
func rowType(r view.Row, prod bool) string {
	switch {
	case prod:
		return typeApproval
	case r.Manual:
		return typeHuman
	case r.Gate:
		return typeGate
	}
	return typeDecision
}

var urgency = map[string]int{typeNote: 1, typeDecision: 2, typeGate: 3, typeHuman: 4, typeApproval: 5}

// moreUrgent is whichever of two types ranks higher; "" ranks lowest.
func moreUrgent(a, b string) string {
	if urgency[b] > urgency[a] {
		return b
	}
	return a
}

// leadType is a thread's most urgent type; a thread of notes alone is a note.
func (t thread) leadType() string { return moreUrgent(typeNote, t.typ) }

// hintOf is the one-line explanation under a header, "" for a plain ask.
func hintOf(d inbox.Decision) string {
	switch {
	case d.Prod != nil:
		return "Approve and the agent runs it. Nothing runs until you do."
	case d.Kind == inbox.KindManual:
		return "Run it yourself, then pick Done and type y."
	case d.UserRan():
		return "You ran it; the agent skips its run."
	case d.Kind == inbox.KindAgentRuns:
		return "The agent runs this after you answer. Don't run it yourself."
	}
	return ""
}

// tagHead is a one-line screen head led by the ask's lead.
func tagHead(d inbox.Decision, project, text string, width int) string {
	l := lead(project, typeOf(d)) + " "
	return l + pyfmt.Clip(text, max(width-lipgloss.Width(l), 1))
}

// tagLines is the pinned lead over an ask, with its explanation after it.
func tagLines(d inbox.Decision, project string, width int) []string {
	head := lead(project, typeOf(d))
	hint := hintOf(d)
	if hint == "" {
		return []string{head}
	}
	pad := strings.Repeat(" ", lipgloss.Width(head)+1)
	lines := view.WrapRest(hint, max(width-len(pad), view.MinWidth), "", "")
	for i, l := range lines {
		if i == 0 {
			lines[i] = head + " " + l
		} else {
			lines[i] = pad + l
		}
	}
	return lines
}

// paintRow is a list row led by its lead after the cursor prefix; st styles
// the rest.
func paintRow(prefix, project, typ, label string, st *lipgloss.Style) string {
	plain := func(s string) string {
		if st == nil || s == "" {
			return s
		}
		return st.Render(s)
	}
	rest := strings.TrimLeft(label, " ")
	indent := label[:len(label)-len(rest)]
	return plain(prefix+indent) + lead(project, typ) + plain(" "+rest)
}
