// Package cli is zen-inbox's command line; its output and exit codes are
// pinned by the golden files in testdata/golden.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

const usage = `Decision inbox: agents ask, the user answers from a live watcher.

Exit codes: 0 ok · 1 not found or wrong state · 2 usage ·
3 answer recorded, delivery failed · 4 vault or db unavailable.`

// exitCode ends the process with a code after the command printed its output.
type exitCode int

func (e exitCode) Error() string { return "exit " + strconv.Itoa(int(e)) }

// App holds what the commands reach outside the process.
type App struct {
	Stdout, Stderr io.Writer
	AM             am.Client
	// Watch runs the panel; set by main so this package stays TUI-free.
	Watch func(ib *inbox.Inbox) error
}

// Run executes argv and returns the process exit code.
func (a *App) Run(argv []string) int {
	root := a.root()
	root.SetArgs(argv)
	err := root.Execute()
	var ie *inbox.Error
	var code exitCode
	switch {
	case err == nil:
		return 0
	case errors.As(err, &code):
		return int(code)
	case errors.As(err, &ie):
		fmt.Fprintf(a.Stderr, "zen-inbox: %s\n", ie.Msg)
		return ie.Code
	}
	fmt.Fprintf(a.Stderr, "zen-inbox: %s\nTry 'zen-inbox --help' for help.\n", err)
	return 2
}

func (a *App) root() *cobra.Command {
	root := &cobra.Command{
		Use:           "zen-inbox",
		Short:         "Decision inbox",
		Long:          usage,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = cmd.Help()
			return exitCode(2)
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return err })
	root.AddCommand(a.ask(), a.brief(), a.amend(), a.withdraw(), a.list(), a.show(),
		a.answer(), a.resolve(), a.retry(), a.watch(), a.approval(), a.note(), a.notes(), a.read())
	return root
}

func (a *App) out(s string) { fmt.Fprintln(a.Stdout, s) }

func caller() string { return os.Getenv("AGENT_MANAGER_SESSION_ID") }

func (a *App) open() (*inbox.Inbox, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, &inbox.Error{Msg: "no home directory: " + err.Error(), Code: 4}
	}
	path, err := inbox.DBPath(home)
	if err != nil {
		return nil, err
	}
	ib, err := inbox.Open(path, a.AM)
	if ib != nil {
		ib.Self = caller()
	}
	return ib, err
}

// report prints a close's outcome and returns its exit code.
func (a *App) report(ib *inbox.Inbox, res *inbox.Delivery, sid string) error {
	msg, err := ib.Describe(res, sid, "")
	if err != nil {
		return err
	}
	if res != nil && !res.OK {
		fmt.Fprintln(a.Stderr, msg)
		return exitCode(3)
	}
	a.out(msg)
	return nil
}

// sessionName is sid's agent-manager name, or sid when it has none.
func (a *App) sessionName(sid string) string {
	if s := am.Find(a.AM.Sessions(), sid); s != nil && pyfmt.Truthy(s["name"]) {
		return pyfmt.Str(s["name"])
	}
	return sid
}

func optional(cmd *cobra.Command, name string) *string {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	v, _ := cmd.Flags().GetString(name)
	return &v
}

func noSession() error {
	return &inbox.Error{Msg: "no session: set AGENT_MANAGER_SESSION_ID or --session", Code: 2}
}

func (a *App) ask() *cobra.Command {
	var in inbox.AskInput
	var prod inbox.ProdAction
	var session string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ask TASK (-q QUESTION [-o OPTION]... | --prod ACTION --repo R --branch B --sha S)",
		Short: "Record a decision; print its ID.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := prodFlags(cmd); err != nil {
				return err
			}
			in.Task = args[0]
			in.SessionID = session
			if in.SessionID == "" {
				in.SessionID = caller()
			}
			if in.SessionID == "" {
				return noSession()
			}
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			in.Name = a.sessionName(in.SessionID)
			ask := ib.Ask
			if cmd.Flags().Changed("prod") {
				ask = func(in inbox.AskInput) (inbox.Decision, error) { return ib.AskProd(in, prod) }
			}
			d, err := ask(in)
			if err != nil {
				return err
			}
			if asJSON {
				a.out(pyfmt.Dumps(d.JSON()))
			} else {
				a.out(d.ID)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&in.Question, "question", "q", "", "the question")
	f.StringArrayVarP(&in.Options, "option", "o", nil, "up to 4, in order")
	f.StringVar(&in.Rec, "rec", "", "recommended option letter")
	f.StringVar(&in.Context, "context", "", "context for this question")
	f.BoolVar(&in.Gate, "gate", false, "gate ID <task>.g")
	f.StringVar(&in.Title, "title", "", "board task title")
	f.StringVar(&in.Brief, "brief", "", "1-3 lines of task context")
	f.StringVar(&session, "session", "", "asking session id")
	f.BoolVar(&asJSON, "json", false, "print the decision as JSON")
	f.StringVar(&prod.Action, "prod", "", "prod-action ask for ACTION (e.g. release)")
	f.StringVar(&prod.Repo, "repo", "", "with --prod: repo name")
	f.StringVar(&prod.Branch, "branch", "", "with --prod: target branch")
	f.StringVar(&prod.Sha, "sha", "", "with --prod: full 40-char commit sha")
	return cmd
}

func (a *App) brief() *cobra.Command {
	var session string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "brief TASK [--title TEXT] [--text TEXT]",
		Short: "Set or replace a task's title and brief for the asking session.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sid := session
			if sid == "" {
				sid = caller()
			}
			if sid == "" {
				return noSession()
			}
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			row, err := ib.SetBrief(sid, args[0], optional(cmd, "title"), optional(cmd, "text"))
			if err != nil {
				return err
			}
			if asJSON {
				a.out(pyfmt.Dumps(row))
			} else {
				a.out(fmt.Sprintf("brief %s for %s", args[0], sid))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.String("title", "", "board task title")
	f.String("text", "", "1-3 lines of context")
	f.StringVar(&session, "session", "", "asking session id")
	f.BoolVar(&asJSON, "json", false, "print the row as JSON")
	return cmd
}

func (a *App) amend() *cobra.Command {
	var options []string
	var force bool
	cmd := &cobra.Command{
		Use:   "amend ID [-q ...] [-o ...]... [--rec ...] [--context ...]",
		Short: "Change an open decision, keeping its ID.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			in := inbox.AmendInput{
				Question: optional(cmd, "question"),
				Rec:      optional(cmd, "rec"),
				Context:  optional(cmd, "context"),
			}
			if len(options) > 0 {
				in.Options = options
			}
			d, err := ib.Amend(args[0], caller(), force, in)
			if err != nil {
				return err
			}
			a.out("amended " + d.ID)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringP("question", "q", "", "new question")
	f.StringArrayVarP(&options, "option", "o", nil, "new options, replacing all")
	f.String("rec", "", "recommended option letter")
	f.String("context", "", "new context")
	f.BoolVar(&force, "force", false, "amend another session's")
	return cmd
}

func (a *App) withdraw() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "withdraw ID",
		Short: "Close an open decision unanswered.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			res, err := ib.Withdraw(args[0], caller(), force)
			if err != nil {
				return err
			}
			d, err := ib.Get(args[0])
			if err != nil {
				return err
			}
			a.out("withdrew " + args[0])
			switch {
			case res == nil:
				return nil
			case res.Message != "":
				a.out(res.Message)
				return nil
			}
			return a.report(ib, res, d.SessionID)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "withdraw another session's")
	return cmd
}

func termWidth() int {
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return w
	}
	return 80
}

func (a *App) list() *cobra.Command {
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:   "list [--all] [--json]",
		Short: "Open decisions, held answers and failed deliveries.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			where := "status = 'open' OR delivery IN ('held', 'sending', 'failed')"
			if all {
				where = "1"
			}
			ds, err := ib.Select(where)
			if err != nil {
				return err
			}
			switch {
			case asJSON:
				objs := make([]pyfmt.Object, len(ds))
				for i, d := range ds {
					objs[i] = d.JSON()
				}
				a.out(pyfmt.Dumps(objs))
			case all:
				for _, d := range ds {
					state := d.Status
					if d.Delivery != "none" {
						state += "/" + d.Delivery
					}
					a.out(strings.Join([]string{d.ID, state, d.SessionName, d.Question}, "\t"))
				}
			default:
				return a.summary(ib)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "every decision ever asked")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print decisions as JSON")
	return cmd
}

func (a *App) summary(ib *inbox.Inbox) error {
	sessions := a.AM.Sessions()
	width := termWidth()
	at := inbox.Now()
	open, err := ib.Select("status = 'open'")
	if err != nil {
		return err
	}
	held, err := ib.Select("delivery = 'held'")
	if err != nil {
		return err
	}
	undelivered, err := ib.Undelivered()
	if err != nil {
		return err
	}
	a.out(view.Render(open, held, undelivered, sessions, width, at))
	for _, row := range view.Rows(open, sessions) {
		opened, err := ib.SessionOpen(row.SessionID)
		if err != nil {
			return err
		}
		a.out("\n" + strings.Join(view.SessionLines(opened, sessions, width, at, false), "\n"))
	}
	return nil
}

func (a *App) show() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show ID [--json]",
		Short: "One decision in full.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			d, err := ib.Get(args[0])
			if err != nil {
				return err
			}
			if asJSON {
				a.out(pyfmt.Dumps(d.JSON()))
				return nil
			}
			a.out(fmt.Sprintf("%s  [%s]  %s (%s)  %s", d.ID, d.Status, d.SessionName, d.SessionID, d.CreatedAt))
			a.out(pyfmt.RStrip("#" + d.Task + " " + d.Title))
			if d.Brief != "" {
				a.out(d.Brief)
			}
			a.out(d.Question)
			if d.Context != "" {
				a.out(d.Context)
			}
			for _, o := range d.Options {
				mark := ""
				if o.Letter == d.Rec {
					mark = "  ← recommended"
				}
				a.out(fmt.Sprintf("  %s) %s%s", o.Letter, o.Text, mark))
			}
			if d.Status == "answered" {
				if d.AnswerKind == "talk" {
					a.out("resolved in talk " + d.Note)
				} else {
					a.out(inbox.AnswerLine(d, -1))
				}
				a.out(pyfmt.RStrip(fmt.Sprintf("delivery: %s %s %s", d.Delivery, d.MessageID, d.DeliveryError)))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the decision as JSON")
	return cmd
}

func (a *App) answer() *cobra.Command {
	var note string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "answer ID (LETTER | --text TEXT) [--note TEXT]",
		Short: "Record an answer; it is sent once the session has no open decision.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var letter *string
			if len(args) == 2 {
				letter = &args[1]
			}
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			res, err := ib.Answer(args[0], letter, optional(cmd, "text"), note, nil)
			if err != nil {
				return err
			}
			d, err := ib.Get(args[0])
			if err != nil {
				return err
			}
			if asJSON {
				a.out(pyfmt.Dumps(d.JSON()))
				if res != nil && !res.OK {
					return exitCode(3)
				}
				return nil
			}
			return a.report(ib, res, d.SessionID)
		},
	}
	cmd.Flags().String("text", "", "free-text answer")
	cmd.Flags().StringVar(&note, "note", "", "note sent with the answer")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the decision as JSON")
	return cmd
}

func (a *App) resolve() *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "resolve ID [--note TEXT]",
		Short: "Close a decision settled in talk; nothing is sent for it.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			res, err := ib.Resolve(args[0], note)
			if err != nil {
				return err
			}
			d, err := ib.Get(args[0])
			if err != nil {
				return err
			}
			a.out("resolved " + args[0])
			if res == nil {
				return nil
			}
			return a.report(ib, res, d.SessionID)
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "what the talk settled")
	return cmd
}

func (a *App) retry() *cobra.Command {
	return &cobra.Command{
		Use:   "retry SESSION",
		Short: "Resend a session's failed delivery as one message, and its failed note replies.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			res, err := ib.Retry(args[0])
			var ie *inbox.Error
			noAnswers := errors.As(err, &ie) && ie.Code == 1
			if err != nil && !noAnswers {
				return err
			}
			var rep error
			if !noAnswers {
				rep = a.report(ib, res, res.SessionID)
			}
			replies, rerr := ib.RetryNotes(args[0])
			failed := false
			for _, r := range replies {
				if r.OK {
					a.out(r.Describe())
				} else {
					fmt.Fprintln(a.Stderr, r.Describe())
					failed = true
				}
			}
			switch {
			case rerr != nil:
				return rerr
			case noAnswers && len(replies) == 0:
				return err
			case rep != nil:
				return rep
			case failed:
				return exitCode(3)
			}
			return nil
		},
	}
}

func (a *App) watch() *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Live view, one row per asking session; walk, review, then Send.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			return a.Watch(ib)
		},
	}
}
