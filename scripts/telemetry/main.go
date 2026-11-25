// zen-telemetry logs every agent-manager agent tool call into zen-flow.db and
// reports the biggest time sinks.
//
//	zen-telemetry hook                       PostToolUse/PostToolUseFailure hook (stdin)
//	zen-telemetry import [--days 31] [--dry-run]
//	zen-telemetry report [--days 7] [--slow 30s] [--json] [--due]
//	zen-telemetry report --by-model [--days 7] [--json]
//	zen-telemetry filed <week> <sink-key> <task-id>
//	zen-telemetry mark-done <week>
//	zen-telemetry sink-task [<task-id> [--fold <task-id>]...]
//	zen-telemetry entry add|close <sink-key>
//
// Storage: $ZEN_FLOW_DB, else <vault>/config/zen-flow.db (vault from
// ~/.claude/second-brain.json). Exit codes: 0 ok, 1 error, 2 usage,
// 10 report --due and this ISO week is already done.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, strings.TrimPrefix(v, "#"))
	return nil
}

var weekRe = regexp.MustCompile(`^\d{4}-W\d{2}$`)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, time.Now()))
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage: zen-telemetry <hook | import | report | filed | mark-done | sink-task | entry> [flags]
  hook                                   record one hook payload from stdin; always exits 0
  import [--days 31] [--dry-run]         one-time import from ~/.claude/projects transcripts
  report [--days 7] [--slow 30s] [--json] [--due]
                                         time-sink report; --due exits 10 if this week is done
  report --by-model [--days 7] [--json]  tokens by role, agent and model, this window and the last
  filed <week> <sink-key> <task-id>      record a task filed for a sink
  mark-done <week>                       close the week's review
  sink-task [<task-id> [--fold <id>]...] print or set the rolling sink task; --fold
                                         opens entries for the sinks filed as <id>
  entry add|close <sink-key>             mark a sink's checklist item open or ticked`)
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	home, _ := os.UserHomeDir()
	fail := func(err error) int {
		fmt.Fprintln(stderr, "zen-telemetry:", err)
		return 1
	}
	switch args[0] {
	case "hook":
		runHook(stdin, home, now)
		return 0
	case "import":
		fs := flag.NewFlagSet("import", flag.ContinueOnError)
		fs.SetOutput(stderr)
		days := fs.Int("days", 31, "import transcripts modified in the last N days")
		dry := fs.Bool("dry-run", false, "parse and count, write nothing")
		if fs.Parse(args[1:]) != nil || fs.NArg() != 0 || *days < 1 {
			return 2
		}
		if err := runImport(home, *days, *dry, stdout, now); err != nil {
			return fail(err)
		}
		return 0
	case "report":
		fs := flag.NewFlagSet("report", flag.ContinueOnError)
		fs.SetOutput(stderr)
		o := reportOpts{}
		fs.IntVar(&o.Days, "days", 7, "window in days before now")
		fs.DurationVar(&o.Slow, "slow", 30*time.Second, "slow-call threshold")
		fs.BoolVar(&o.JSON, "json", false, "print JSON")
		fs.BoolVar(&o.Due, "due", false, "exit 10, printing nothing, if this ISO week is marked done")
		byModel := fs.Bool("by-model", false, "tokens by role, agent type and model instead of the time-sink report")
		if fs.Parse(args[1:]) != nil || fs.NArg() != 0 || o.Days < 1 {
			return 2
		}
		if *byModel {
			if err := runByModel(home, o.Days, o.JSON, stdout, now); err != nil {
				return fail(err)
			}
			return 0
		}
		code, err := runReport(home, o, stdout, now)
		if err != nil {
			return fail(err)
		}
		return code
	case "filed":
		if len(args) != 4 || !weekRe.MatchString(args[1]) || args[2] == "" || args[3] == "" {
			usage(stderr)
			return 2
		}
		if err := runFiled(home, args[1], args[2], args[3], now); err != nil {
			return fail(err)
		}
		return 0
	case "mark-done":
		if len(args) != 2 || !weekRe.MatchString(args[1]) {
			usage(stderr)
			return 2
		}
		if err := runMarkDone(home, args[1], now); err != nil {
			return fail(err)
		}
		return 0
	case "sink-task":
		fs := flag.NewFlagSet("sink-task", flag.ContinueOnError)
		fs.SetOutput(stderr)
		var fold multiFlag
		fs.Var(&fold, "fold", "fold the sinks filed as this task id (repeatable)")
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			if fs.Parse(args[2:]) != nil || fs.NArg() != 0 {
				return 2
			}
			if err := runSinkTask(home, strings.TrimPrefix(args[1], "#"), fold, stdout, now); err != nil {
				return fail(err)
			}
			return 0
		}
		if len(args) != 1 {
			usage(stderr)
			return 2
		}
		if err := runSinkTask(home, "", nil, stdout, now); err != nil {
			return fail(err)
		}
		return 0
	case "entry":
		if len(args) != 3 || (args[1] != "add" && args[1] != "close") || args[2] == "" {
			usage(stderr)
			return 2
		}
		if err := runEntry(home, args[1], args[2], stdout, now); err != nil {
			return fail(err)
		}
		return 0
	case "-h", "--help", "help":
		usage(stdout)
		return 0
	}
	usage(stderr)
	return 2
}
