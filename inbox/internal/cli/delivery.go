package cli

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

func (a *App) delivery() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delivery (scan | list | check | dismiss | resend)",
		Short: "Log of agent-manager delivery problems: lost, false drop, refused, held, unverified.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = cmd.Help()
			return exitCode(2)
		},
	}
	cmd.AddCommand(a.deliveryScan(), a.deliveryList(), a.deliveryCheck(), a.deliveryDismiss(), a.deliveryResend())
	return cmd
}

// withInbox opens the db for one delivery command.
func (a *App) withInbox(fn func(ib *inbox.Inbox) error) error {
	ib, err := a.open()
	if err != nil {
		return err
	}
	defer ib.Close()
	return fn(ib)
}

func entryID(arg string) (int64, error) {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || id <= 0 {
		return 0, usageErr("not an entry id: " + arg)
	}
	return id, nil
}

func (a *App) deliveryScan() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "scan [--json]",
		Short: "Copy agent-manager's delivery problems into the log; prune closed entries past 30 days.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.withInbox(func(ib *inbox.Inbox) error {
				rep, err := ib.ScanDelivery()
				if err != nil {
					return err
				}
				if asJSON {
					a.out(pyfmt.Dumps(rep.JSON()))
				} else {
					a.out(rep.Text())
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the report as JSON")
	return cmd
}

func (a *App) deliveryList() *cobra.Command {
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:   "list [--all] [--json]",
		Short: "Open entries, newest first; --all adds the closed ones.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.withInbox(func(ib *inbox.Inbox) error {
				es, err := ib.Entries(all)
				if err != nil {
					return err
				}
				if asJSON {
					objs := make([]pyfmt.Object, len(es))
					for i, e := range es {
						objs[i] = e.JSON()
					}
					a.out(pyfmt.Dumps(objs))
					return nil
				}
				if len(es) == 0 {
					a.out("no delivery problems")
				}
				at := inbox.Now()
				for _, e := range es {
					a.out(strconv.FormatInt(e.ID, 10) + "\t" + view.DeliveryLine(e, at))
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "closed entries too")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print entries as JSON")
	return cmd
}

func (a *App) deliveryCheck() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "check MESSAGE_ID [--json]",
		Short: "What became of an agent-manager message, whoever sent it: delivered, false drop, lost, held: <why> or unknown.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return a.withInbox(func(ib *inbox.Inbox) error {
				c, err := ib.CheckMessage(args[0])
				if err != nil {
					return err
				}
				if asJSON {
					obj := pyfmt.Object{{Key: "message_id", Value: args[0]}, {Key: "status", Value: c.Status}, {Key: "why", Value: c.Why}}
					if c.Entry != nil {
						obj = append(obj, pyfmt.KV{Key: "entry", Value: c.Entry.JSON()})
					}
					a.out(pyfmt.Dumps(obj))
				} else if c.Why != "" {
					a.out(c.Status + " (unavailable: " + c.Why + ")")
				} else {
					a.out(c.Status)
				}
				if c.Status == "unknown" {
					return exitCode(1)
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the verdict as JSON")
	return cmd
}

func (a *App) deliveryDismiss() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "dismiss ENTRY_ID [--json]",
		Short: "Close an open entry without acting on it.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id, err := entryID(args[0])
			if err != nil {
				return err
			}
			return a.withInbox(func(ib *inbox.Inbox) error {
				e, err := ib.Dismiss(id)
				if err != nil {
					return err
				}
				if asJSON {
					a.out(pyfmt.Dumps(e.JSON()))
				} else {
					a.out("dismissed entry " + args[0])
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the entry as JSON")
	return cmd
}

func (a *App) deliveryResend() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "resend ENTRY_ID [--json]",
		Short: "Send an open entry's message again: to its recipient, else its one live namesake.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id, err := entryID(args[0])
			if err != nil {
				return err
			}
			return a.withInbox(func(ib *inbox.Inbox) error {
				e, text, err := ib.Resend(id)
				if err != nil {
					return err
				}
				if asJSON {
					a.out(pyfmt.Dumps(e.JSON()))
				} else {
					a.out(text)
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the entry as JSON")
	return cmd
}
