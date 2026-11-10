// Command zen-inbox is the decision inbox: agents ask, the user answers from
// a Bubble Tea watcher, answers go back to each session as one message.
package main

import (
	"os"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/cli"
	"github.com/vzsoares/dotfiles/inbox/internal/tui"
)

func main() {
	client := am.Exec{TmuxEnv: am.TmuxEnv()}
	app := &cli.App{Stdout: os.Stdout, Stderr: os.Stderr, AM: client, Watch: tui.Watch(client)}
	os.Exit(app.Run(os.Args[1:]))
}
