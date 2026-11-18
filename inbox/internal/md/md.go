// Package md renders agent text as markdown for the panel: glamour with an
// ANSI-16 style, hard-wrapped so no line is wider than asked.
package md

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/charmbracelet/glamour"
	gansi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// Key names one rendered field: a decision's question, context or option at
// a revision, or a brief.
type Key struct {
	ID    string
	Rev   int64
	Field string
	Width int
}

type entry struct {
	text  string
	lines []string
}

// maxEntries bounds the cache; it is cleared when full.
const maxEntries = 512

// Renderer keeps one glamour renderer per width for the current colour
// profile, and the rendered lines per Key. Not safe for concurrent use.
type Renderer struct {
	lr      *lipgloss.Renderer
	profile termenv.Profile
	trs     map[int]*glamour.TermRenderer
	cache   map[Key]entry
	fail    error // set by tests to force the raw-text fallback
}

func New(lr *lipgloss.Renderer) *Renderer {
	return &Renderer{lr: lr, profile: lr.ColorProfile(), trs: map[int]*glamour.TermRenderer{}, cache: map[Key]entry{}}
}

func (r *Renderer) sync() {
	if p := r.lr.ColorProfile(); p != r.profile {
		r.profile, r.trs, r.cache = p, map[int]*glamour.TermRenderer{}, map[Key]entry{}
	}
}

// Cached is Lines for k, rendered again only when k or its text changes.
func (r *Renderer) Cached(k Key, text string) []string {
	r.sync()
	if e, ok := r.cache[k]; ok && e.text == text {
		return e.lines
	}
	lines := r.Lines(text, k.Width)
	if len(r.cache) >= maxEntries {
		clear(r.cache)
	}
	r.cache[k] = entry{text, lines}
	return lines
}

// Lines renders text at width: blank edges dropped, every line at most width
// cells and ending reset, and no escape bytes at all under the Ascii profile.
// Text that fails to render comes back raw, still wrapped.
func (r *Renderer) Lines(text string, width int) []string {
	r.sync()
	width = max(width, 1)
	// Cells are counted per rune, so a tab would overrun the width; the fence
	// sentinels (Nerd Font glyphs too) would open a box mid-text.
	text = inputFix.Replace(text)
	out, err := r.render(text, width)
	if err != nil {
		out = text
	}
	ascii := r.profile == termenv.Ascii
	var lines []string
	raw := strings.Split(out, "\n")
	for i := 0; i < len(raw); i++ {
		if lead, _, ok := strings.Cut(raw[i], fenceOpen); ok {
			var code []string
			for i++; i < len(raw); i++ {
				body, _, closed := strings.Cut(raw[i], fenceClose)
				if !closed || strings.TrimSpace(ansi.Strip(body)) != "" {
					code = append(code, body)
				}
				if closed {
					break
				}
			}
			lines = append(lines, r.box(lead, code, width, ascii)...)
			continue
		}
		lines = append(lines, wrap(raw[i], width, ascii)...)
	}
	lines = slices.CompactFunc(lines, func(a, b string) bool { return a == "" && b == "" })
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// render is glamour's output; a panic inside it is an error too.
func (r *Renderer) render(text string, width int) (out string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("md: render panicked: %v", p)
		}
	}()
	if r.fail != nil {
		return "", r.fail
	}
	return r.term(width).Render(text)
}

// wrap hard-wraps one rendered line to width, each piece keeping the colour
// open at its start.
func wrap(raw string, width int, ascii bool) []string {
	var out []string
	active := ""
	for _, piece := range strings.Split(ansi.Hardwrap(raw, width, true), "\n") {
		line := active + piece
		active = carry(active, piece)
		out = append(out, finish(line, ascii))
	}
	return out
}

// Fenced code blocks reach Lines between these private-use runes, so box can
// frame them after glamour has laid out the rest.
const (
	fenceOpen  = "\ue000"
	fenceClose = "\ue001"
)

var inputFix = strings.NewReplacer("\t", "    ", fenceOpen, "\ufffd", fenceClose, "\ufffd")

// box frames code lines in a rounded border at lead's indent, sized to the
// widest line; too narrow a pane gets the lines unframed.
func (r *Renderer) box(lead string, code []string, width int, ascii bool) []string {
	leadW := lipgloss.Width(lead)
	inner := width - leadW - 4
	var body []string
	for _, l := range code {
		l = finish(ansi.TruncateLeft(l, leadW, ""), ascii)
		if inner < minInner {
			body = append(body, wrap(lead+l, width, ascii)...)
			continue
		}
		body = append(body, wrap(l, inner, ascii)...)
	}
	for len(body) > 0 && strings.TrimSpace(ansi.Strip(body[len(body)-1])) == "" {
		body = body[:len(body)-1]
	}
	if inner < minInner {
		return body
	}
	cw := 0
	for _, l := range body {
		cw = max(cw, lipgloss.Width(l))
	}
	pad := finish(lead, ascii)
	pad += strings.Repeat(" ", leadW-lipgloss.Width(pad))
	edge := func(s string) string {
		if ascii {
			return s
		}
		return r.lr.NewStyle().Foreground(lipgloss.Color("8")).Render(s)
	}
	out := []string{pad + edge("╭"+strings.Repeat("─", cw+2)+"╮")}
	for _, l := range body {
		out = append(out, pad+edge("│")+" "+l+strings.Repeat(" ", cw-lipgloss.Width(l))+" "+edge("│"))
	}
	return append(out, pad+edge("╰"+strings.Repeat("─", cw+2)+"╯"))
}

// minInner is the narrowest code column worth a frame.
const minInner = 8

const reset = "\x1b[0m"

// finish strips the right padding glamour adds up to the wrap width and
// closes any colour, or drops every escape under Ascii.
func finish(line string, ascii bool) string {
	visible := strings.TrimRight(ansi.Strip(line), " ")
	if ascii || !strings.Contains(line, "\x1b") {
		return visible
	}
	if visible == "" {
		return ""
	}
	return trailingSGR.ReplaceAllString(ansi.Truncate(line, lipgloss.Width(visible), ""), "") + reset
}

var (
	sgr         = regexp.MustCompile(`\x1b\[([0-9;]*)m`)
	trailingSGR = regexp.MustCompile(`(\x1b\[[0-9;]*m)+$`)
)

// carry is the SGR state still open after s, starting from active, so a line
// split from a styled one keeps its colour.
func carry(active, s string) string {
	for _, m := range sgr.FindAllStringSubmatch(s, -1) {
		if strings.Trim(m[1], "0;") == "" {
			active = ""
		} else {
			active += m[0]
		}
	}
	return active
}

func (r *Renderer) term(width int) *glamour.TermRenderer {
	if tr, ok := r.trs[width]; ok {
		return tr
	}
	tr, err := glamour.NewTermRenderer(
		glamour.WithStyles(Style()),
		glamour.WithColorProfile(r.profile),
		glamour.WithWordWrap(width),
		glamour.WithPreservedNewLines(),
		glamour.WithChromaFormatter("terminal16"),
	)
	if err != nil {
		// Only invalid options fail, and these are fixed.
		panic(err)
	}
	r.trs[width] = tr
	return tr
}

func ptr[T any](v T) *T { return &v }

// Chroma entries are hex values the terminal16 formatter maps exactly to
// SGR 30-37/90-97.
const (
	hexGreen   = "#007f00"
	hexYellow  = "#7f7fe0"
	hexMagenta = "#7f007f"
	hexCyan    = "#007f7f"
	hexGrey    = "#555555"
)

// Style is a crush-like dark style cut down to ANSI 16 indexes: magenta
// headings and bullets, tinted inline code, no margins that pad lines.
func Style() gansi.StyleConfig {
	return gansi.StyleConfig{
		Document: gansi.StyleBlock{Margin: ptr(uint(0))},
		BlockQuote: gansi.StyleBlock{
			StylePrimitive: gansi.StylePrimitive{Italic: ptr(true)},
			Indent:         ptr(uint(1)), IndentToken: ptr("│ "),
		},
		List: gansi.StyleList{LevelIndent: 2},
		Heading: gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{
			Color: ptr("5"), Bold: ptr(true),
		}},
		H1: gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{
			Prefix: " ", Suffix: " ", Color: ptr("0"), BackgroundColor: ptr("5"),
		}},
		H2:             gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: "## "}},
		H3:             gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: "### "}},
		H4:             gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: "#### "}},
		H5:             gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: "##### "}},
		H6:             gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: "###### "}},
		Strikethrough:  gansi.StylePrimitive{CrossedOut: ptr(true)},
		Emph:           gansi.StylePrimitive{Italic: ptr(true)},
		Strong:         gansi.StylePrimitive{Bold: ptr(true)},
		HorizontalRule: gansi.StylePrimitive{Color: ptr("8"), Format: "\n────────\n"},
		Item:           gansi.StylePrimitive{Prefix: "• ", Color: ptr("5")},
		Enumeration:    gansi.StylePrimitive{BlockPrefix: ". "},
		Task:           gansi.StyleTask{Ticked: "[✓] ", Unticked: "[ ] "},
		Link:           gansi.StylePrimitive{Color: ptr("8"), Underline: ptr(true)},
		LinkText:       gansi.StylePrimitive{Color: ptr("4"), Bold: ptr(true), Underline: ptr(true)},
		Image:          gansi.StylePrimitive{Color: ptr("8"), Underline: ptr(true)},
		ImageText:      gansi.StylePrimitive{Color: ptr("4"), Format: "Image: {{.text}} →"},
		Code: gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{
			Color: ptr("6"), BackgroundColor: ptr("8"),
		}},
		CodeBlock: gansi.StyleCodeBlock{
			StyleBlock: gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{
				Color: ptr("6"), BlockPrefix: fenceOpen + "\n", BlockSuffix: fenceClose + "\n",
			}, Margin: ptr(uint(0))},
			Chroma: &gansi.Chroma{
				Text:                gansi.StylePrimitive{Color: ptr(hexCyan)},
				Comment:             gansi.StylePrimitive{Color: ptr(hexGrey)},
				CommentPreproc:      gansi.StylePrimitive{Color: ptr(hexMagenta)},
				Keyword:             gansi.StylePrimitive{Color: ptr(hexMagenta)},
				KeywordReserved:     gansi.StylePrimitive{Color: ptr(hexMagenta)},
				KeywordNamespace:    gansi.StylePrimitive{Color: ptr(hexMagenta)},
				KeywordType:         gansi.StylePrimitive{Color: ptr(hexMagenta)},
				Name:                gansi.StylePrimitive{Color: ptr(hexCyan)},
				NameBuiltin:         gansi.StylePrimitive{Color: ptr(hexCyan)},
				NameFunction:        gansi.StylePrimitive{Color: ptr(hexCyan)},
				LiteralNumber:       gansi.StylePrimitive{Color: ptr(hexYellow)},
				LiteralString:       gansi.StylePrimitive{Color: ptr(hexGreen)},
				LiteralStringEscape: gansi.StylePrimitive{Color: ptr(hexGreen)},
				GenericDeleted:      gansi.StylePrimitive{Color: ptr("#7f0000")},
				GenericInserted:     gansi.StylePrimitive{Color: ptr(hexGreen)},
				GenericEmph:         gansi.StylePrimitive{Italic: ptr(true)},
				GenericStrong:       gansi.StylePrimitive{Bold: ptr(true)},
			},
		},
		Table: gansi.StyleTable{
			CenterSeparator: ptr("┼"), ColumnSeparator: ptr("│"), RowSeparator: ptr("─"),
		},
	}
}

var plainMarks = []*regexp.Regexp{
	regexp.MustCompile("`+([^`]+)`+"),
	regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`),
	regexp.MustCompile(`\*\*([^*\s](?:[^*]*[^*\s])?)\*\*`),
	regexp.MustCompile(`~~([^~\s](?:[^~]*[^~\s])?)~~`),
	regexp.MustCompile(`(^|[^*\w])\*([^*\s](?:[^*]*[^*\s])?)\*`),
}

// Plain drops inline markdown markers (code ticks, bold, italic, strike,
// link targets) for one-line rows; the text inside stays.
func Plain(s string) string {
	for i, re := range plainMarks {
		repl := "$1"
		if i == len(plainMarks)-1 {
			repl = "$1$2"
		}
		s = re.ReplaceAllString(s, repl)
	}
	return s
}
