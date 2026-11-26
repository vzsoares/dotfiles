package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

// Tag fills: black on yellow and on cyan, white on red. Black and white text
// on a filled ANSI colour reads on light and dark terminal themes alike.
var (
	tagYouStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("3")).Bold(true)
	tagAgentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6")).Bold(true)
	tagProdStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("1")).Bold(true)
)

func tagStyle(label string) *lipgloss.Style {
	switch label {
	case view.TagYou:
		return &tagYouStyle
	case view.TagAgent:
		return &tagAgentStyle
	}
	return &tagProdStyle
}

// tagOf is the run tag for an ask and its one-line copy, "" when it has none.
func tagOf(d inbox.Decision) (label, hint string) {
	switch {
	case d.Prod != nil:
		return view.TagProd, "Approve and the agent runs it. Nothing runs until you do."
	case d.Kind == inbox.KindManual:
		return view.TagYou, "Run it yourself, then pick Done and type y."
	case d.UserRan():
		return view.TagAgent, "You ran it; the agent skips its run."
	case d.Kind == inbox.KindAgentRuns:
		return view.TagAgent, "The agent runs this after you answer. Don't run it yourself."
	}
	return "", ""
}

// tagHead is a one-line screen head led by the ask's filled tag.
func tagHead(d inbox.Decision, text string, width int) string {
	label, _ := tagOf(d)
	if label == "" {
		return pyfmt.Clip(text, width)
	}
	return tagStyle(label).Render(" "+label+" ") + " " + pyfmt.Clip(text, max(width-len(label)-3, 1))
}

// tagLines is the pinned tag over an ask: the filled label, then its copy.
func tagLines(d inbox.Decision, width int) []string {
	label, hint := tagOf(d)
	if label == "" {
		return nil
	}
	head := tagStyle(label).Render(" " + label + " ")
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

var tagWord = regexp.MustCompile(`(YOU RUN THIS|AGENT RUNS THIS|PROD)( · |$)`)

// paintTags fills the run tags in a list row and styles the rest with st.
func paintTags(line string, st *lipgloss.Style) string {
	plain := func(s string) string {
		if st == nil || s == "" {
			return s
		}
		return st.Render(s)
	}
	var out strings.Builder
	last := 0
	for _, m := range tagWord.FindAllStringSubmatchIndex(line, -1) {
		if m[0] > 0 && line[m[0]-1] != ' ' {
			continue
		}
		out.WriteString(plain(line[last:m[0]]))
		out.WriteString(tagStyle(line[m[2]:m[3]]).Render(line[m[2]:m[3]]))
		last = m[3]
	}
	out.WriteString(plain(line[last:]))
	return out.String()
}
