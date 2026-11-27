package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
)

func (a *App) delete() *cobra.Command {
	var task string
	var purge, dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "delete [ID]... [--task N] [--purge] [--dry-run] [--json]",
		Short: "Hide notes, closed decisions and chats; --purge removes them.",
		Long: "IDs: nN (note), cN (chat), TASK.N or TASK.gN (decision). --task N takes\n" +
			"every closed decision, note and chat of that task and skips what a guard\n" +
			"keeps; an explicit ID that fails a guard writes nothing and exits 1.",
		RunE: func(_ *cobra.Command, args []string) error {
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			res, err := ib.Delete(args, task, purge, dryRun)
			if err != nil {
				return err
			}
			if asJSON {
				a.out(pyfmt.Dumps(deleteJSON(res, dryRun)))
			} else {
				a.printDelete(res, dryRun)
			}
			if res.Refused {
				return exitCode(1)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&task, "task", "", "every closed decision, note and chat of this task")
	f.BoolVar(&purge, "purge", false, "remove the rows and their children instead of hiding them")
	f.BoolVar(&dryRun, "dry-run", false, "run the checks and print what would change")
	f.BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}

func deleteJSON(res inbox.DeleteResult, dryRun bool) pyfmt.Object {
	deleted := make([]any, len(res.Deleted))
	for i, d := range res.Deleted {
		deleted[i] = pyfmt.Object{{Key: "id", Value: d.ID}, {Key: "kind", Value: d.Kind}, {Key: "mode", Value: d.Mode}}
	}
	skipped := make([]any, len(res.Skipped))
	for i, s := range res.Skipped {
		skipped[i] = pyfmt.Object{{Key: "id", Value: s.ID}, {Key: "reason", Value: s.Reason}}
	}
	return pyfmt.Object{{Key: "dry_run", Value: dryRun}, {Key: "deleted", Value: deleted}, {Key: "skipped", Value: skipped}}
}

func (a *App) printDelete(res inbox.DeleteResult, dryRun bool) {
	for _, d := range res.Deleted {
		verb := map[string]string{inbox.ModeHide: "hid", inbox.ModePurge: "purged"}[d.Mode]
		if dryRun {
			verb = "would " + d.Mode
		}
		a.out(verb + " " + d.ID)
	}
	for _, s := range res.Skipped {
		if res.Refused {
			fmt.Fprintln(a.Stderr, s.Msg)
		} else {
			fmt.Fprintf(a.Stderr, "skipped %s: %s\n", s.ID, s.Reason)
		}
	}
}

func (a *App) chats() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "chats [TASK] [--json]",
		Short: "Chats to chief the panel shows, newest first.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			task := ""
			if len(args) == 1 {
				task = args[0]
			}
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			cs, err := ib.Chats(task)
			if err != nil {
				return err
			}
			if asJSON {
				objs := make([]pyfmt.Object, len(cs))
				for i, c := range cs {
					objs[i] = c.JSON()
				}
				a.out(pyfmt.Dumps(objs))
				return nil
			}
			now := inbox.Now()
			for _, c := range cs {
				scope := "#" + c.Task
				if c.Task == "" {
					scope = "-"
				}
				a.out(fmt.Sprintf("%s · %s · %s · %s · %s", c.Label(), scope, inbox.Age(c.SentAt, now), c.Status, firstLine(c.Text, 60)))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print chats as JSON")
	return cmd
}

// firstLine is s up to its first newline, cut to n runes with an ellipsis.
func firstLine(s string, n int) string {
	line, _, _ := strings.Cut(s, "\n")
	if utf8.RuneCountInString(line) <= n {
		return line
	}
	return string([]rune(line)[:n-1]) + "…"
}
