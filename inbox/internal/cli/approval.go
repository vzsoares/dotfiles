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
	for _, name := range []string{"repo", "branch", "sha"} {
		if f.Changed(name) {
			return usageErr("--repo, --branch and --sha need --prod")
		}
	}
	if !f.Changed("question") {
		return usageErr(`required flag(s) "question" not set`)
	}
	return nil
}

func (a *App) approval() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approval (use | check) ID --action A --repo R --branch B --sha S",
		Short: "Check or consume an approved prod-action ask.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = cmd.Help()
			return exitCode(2)
		},
	}
	cmd.AddCommand(a.approvalCmd("use", "Consume the approval once; exit 1 with the reason if it does not match."),
		a.approvalCmd("check", "Validate the approval without consuming it."))
	return cmd
}

func (a *App) approvalCmd(name, short string) *cobra.Command {
	var want inbox.ProdAction
	var by string
	cmd := &cobra.Command{
		Use:   name + " ID --action A --repo R --branch B --sha S",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
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
			p, err := ib.Approval(args[0], want, by)
			if err != nil {
				return err
			}
			if name == "use" {
				a.out("used " + args[0] + " by " + p.UsedBy)
			} else {
				a.out("approved " + args[0])
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&want.Action, "action", "", "the prod action (e.g. release)")
	f.StringVar(&want.Repo, "repo", "", "repo name")
	f.StringVar(&want.Branch, "branch", "", "target branch")
	f.StringVar(&want.Sha, "sha", "", "full 40-char commit sha")
	if name == "use" {
		f.StringVar(&by, "by", "", "who uses it (default $AGENT_MANAGER_SESSION_ID, else user)")
	}
	return cmd
}
