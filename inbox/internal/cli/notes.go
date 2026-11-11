package cli

import (
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
)

func (a *App) note() *cobra.Command {
	var in inbox.NoteInput
	var session string
	cmd := &cobra.Command{
		Use:   `note [TASK] --title "<one line>" [--body TEXT | --body -]`,
		Short: "Leave chief's note for the user in the panel; print its ID.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				in.Task = args[0]
			}
			in.SessionID = session
			if in.SessionID == "" {
				in.SessionID = caller()
			}
			if in.SessionID == "" {
				return noSession()
			}
			if in.Body == "-" {
				raw, err := io.ReadAll(io.LimitReader(os.Stdin, inbox.BodyMax+1))
				if err != nil {
					return usageErr("--body -: " + err.Error())
				}
				in.Body = string(raw)
			}
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			in.Name = in.SessionID
			if s := am.Find(a.AM.Sessions(), in.SessionID); s != nil && pyfmt.Truthy(s["name"]) {
				in.Name = pyfmt.Str(s["name"])
			}
			n, err := ib.AddNote(in)
			if err != nil {
				return err
			}
			a.out(n.Label())
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&in.Title, "title", "", "one line, at most 120 characters")
	f.StringVar(&in.Body, "body", "", "markdown body, or - to read stdin")
	f.StringVar(&session, "session", "", "sending session id")
	return cmd
}

func (a *App) notes() *cobra.Command {
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:   "notes [--all] [--json]",
		Short: "Notes the panel shows, newest first.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			ns, err := ib.Notes(all)
			if err != nil {
				return err
			}
			if asJSON {
				objs := make([]pyfmt.Object, len(ns))
				for i, n := range ns {
					objs[i] = n.JSON()
				}
				a.out(pyfmt.Dumps(objs))
				return nil
			}
			for _, n := range ns {
				a.out(n.String())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include hidden read notes")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print notes as JSON")
	return cmd
}

func (a *App) read() *cobra.Command {
	var all, unread bool
	cmd := &cobra.Command{
		Use:   "read (ID... | --all) [--unread]",
		Short: "Mark notes read, or unread with --unread.",
		RunE: func(_ *cobra.Command, args []string) error {
			if all == (len(args) > 0) {
				return usageErr("give note IDs or --all")
			}
			var ids []int64
			for _, s := range args {
				id, err := inbox.ParseNoteID(s)
				if err != nil {
					return err
				}
				ids = append(ids, id)
			}
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			if all {
				return ib.MarkAllNotes(!unread)
			}
			return ib.MarkNotes(ids, !unread)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "every visible note")
	cmd.Flags().BoolVar(&unread, "unread", false, "mark unread instead")
	return cmd
}
