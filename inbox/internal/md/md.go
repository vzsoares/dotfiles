// Package md renders agent text as markdown for the panel: glamour with an
// ANSI-16 style, hard-wrapped so no line is wider than asked.
package md

import (
	"regexp"
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
func (r *Renderer) Lines(text string, width int) []string {
	r.sync()
	width = max(width, 1)
	// Cells are counted per rune, so a tab would overrun the width.
	text = strings.ReplaceAll(text, "\t", "    ")
	out, err := r.term(width).Render(text)
	if err != nil {
		out = text
	}
	var lines []string
	active := ""
	for _, raw := range strings.Split(out, "\n") {
		for _, piece := range strings.Split(ansi.Hardwrap(raw, width, true), "\n") {
			line := active + piece
			active = carry(active, piece)
			lines = append(lines, finish(line, r.profile == termenv.Ascii))
		}
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

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
	return ansi.Truncate(line, lipgloss.Width(visible), "") + reset
}

var sgr = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

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

// Style is glamour's dark style cut down to ANSI 16 indexes, with no margins
// and no decoration that pads lines.
func Style() gansi.StyleConfig {
	code := gansi.StylePrimitive{Color: ptr("6")}
	return gansi.StyleConfig{
		Document:   gansi.StyleBlock{Margin: ptr(uint(0))},
		BlockQuote: gansi.StyleBlock{Indent: ptr(uint(1)), IndentToken: ptr("│ ")},
		List:       gansi.StyleList{LevelIndent: 2},
		Heading: gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{
			Color: ptr("4"), Bold: ptr(true),
		}},
		Strikethrough:  gansi.StylePrimitive{CrossedOut: ptr(true)},
		Emph:           gansi.StylePrimitive{Italic: ptr(true)},
		Strong:         gansi.StylePrimitive{Bold: ptr(true)},
		HorizontalRule: gansi.StylePrimitive{Color: ptr("8"), Format: "\n--------\n"},
		Item:           gansi.StylePrimitive{BlockPrefix: "• "},
		Enumeration:    gansi.StylePrimitive{BlockPrefix: ". "},
		Task:           gansi.StyleTask{Ticked: "[✓] ", Unticked: "[ ] "},
		Link:           gansi.StylePrimitive{Color: ptr("4"), Underline: ptr(true)},
		LinkText:       gansi.StylePrimitive{Bold: ptr(true)},
		Image:          gansi.StylePrimitive{Color: ptr("4"), Underline: ptr(true)},
		ImageText:      gansi.StylePrimitive{Color: ptr("8"), Format: "Image: {{.text}} →"},
		Code:           gansi.StyleBlock{StylePrimitive: code},
		CodeBlock: gansi.StyleCodeBlock{
			StyleBlock: gansi.StyleBlock{StylePrimitive: code, Margin: ptr(uint(0))},
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
		Table: gansi.StyleTable{},
	}
}
