package cli

import (
	"cmp"

	"github.com/spf13/cobra"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

func usageErr(msg string) error { return &inbox.Error{Msg: msg, Code: 2} }

// prodFlags keeps `ask --prod` and the plain ask apart: a prod-action ask
// writes its own question, options and rec.
func prodFlags(cmd *cobra.Command) error {
	f := cmd.Flags()
	if f.Changed("prod") {
		for _, name := range []string{"question", "option", "rec", "gate"} {
			if f.Changed(name) {
				return usageErr("--prod writes its own question and options; drop -q, -o, --rec and --gate")
			}
		}
		return nil
	}
	for _, name := range []string{"repo", "branch", "sha", "cmd"} {
		if f.Changed(name) {
			return usageErr("--repo, --branch, --sha and --cmd need --prod")
		}
	}
	if !f.Changed("question") {
		return usageErr(`required flag(s) "question" not set`)
	}
	return nil
}

func (a *App) approval() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approval (use | check | pending | push)",
		Short: "Check or consume an approved prod-action ask, or a used release's push.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = cmd.Help()
			return exitCode(2)
		},
	}
	cmd.AddCommand(a.approvalCmd("use", "Consume the approval once; exit 1 with the reason if it does not match."),
		a.approvalCmd("check", "Validate the approval without consuming it."),
		a.approvalPending(), a.approvalPush())
	return cmd
}

func (a *App) approvalCmd(name, short string) *cobra.Command {
	var want inbox.ProdAction
	var by string
	var match bool
	cmd := &cobra.Command{
		Use:   name + " (ID | --match) --action A --repo R (--branch B --sha S | --cmd C)",
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if match == (len(args) == 1) {
				return usageErr("give an approval ID or --match, not both")
			}
			if err := inbox.CheckProd(want); err != nil {
				return err
			}
			if name == "use" {
				by = cmp.Or(by, caller(), "user")
			}
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			var id string
			var p inbox.ProdAction
			if match {
				id, p, err = ib.ApprovalMatch(want, by)
			} else {
				id = args[0]
				p, err = ib.Approval(id, want, by)
			}
			if err != nil {
				return err
			}
			if name == "use" {
				a.out("used " + id + " by " + p.UsedBy)
			} else {
				a.out("approved " + id)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&match, "match", false, "use the oldest approved unused ask bound to these fields")
	f.StringVar(&want.Action, "action", "", "the prod action (e.g. release)")
	f.StringVar(&want.Repo, "repo", "", "repo name")
	f.StringVar(&want.Branch, "branch", "", "target branch")
	f.StringVar(&want.Sha, "sha", "", "full 40-char commit sha")
	f.StringVar(&want.Cmd, "cmd", "", "with --action run: the exact command, byte for byte")
	if name == "use" {
		f.StringVar(&by, "by", "", "who uses it (default $AGENT_MANAGER_SESSION_ID, else user)")
	}
	return cmd
}

func (a *App) approvalPending() *cobra.Command {
	var repo, branch string
	cmd := &cobra.Command{
		Use:   "pending --repo R --branch B",
		Short: "Print \"<id> <approved sha>\" per used release approval not yet pushed, oldest first; exit 1 when none. Read-only.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := inbox.CheckTarget(inbox.ProdAction{Action: inbox.ReleaseAction, Repo: repo, Branch: branch}); err != nil {
				return err
			}
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			found, err := ib.ReleasesPending(repo, branch)
			if err != nil {
				return err
			}
			if len(found) == 0 {
				return &inbox.Error{Msg: "no used release approval awaiting its push for " + repo + " " + branch, Code: 1}
			}
			for _, p := range found {
				a.out(p.ID + " " + p.Sha)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&repo, "repo", "", "repo name")
	f.StringVar(&branch, "branch", "", "target branch")
	return cmd
}

func (a *App) approvalPush() *cobra.Command {
	var repo, branch, sha, by string
	cmd := &cobra.Command{
		Use:   "push ID --repo R --branch B --sha S",
		Short: "Record S as the one push a used release approval lets through; exit 1 if it was pushed already.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := inbox.CheckProd(inbox.ProdAction{Action: inbox.ReleaseAction, Repo: repo, Branch: branch, Sha: sha}); err != nil {
				return err
			}
			by = cmp.Or(by, caller(), "user")
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			p, err := ib.ReleasePush(args[0], repo, branch, sha, by)
			if err != nil {
				return err
			}
			a.out("pushed " + args[0] + " at " + p.PushedSha[:12] + " by " + p.PushedBy)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&repo, "repo", "", "repo name")
	f.StringVar(&branch, "branch", "", "target branch")
	f.StringVar(&sha, "sha", "", "full 40-char sha being pushed")
	f.StringVar(&by, "by", "", "who pushes (default $AGENT_MANAGER_SESSION_ID, else user)")
	return cmd
}
