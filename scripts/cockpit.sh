#!/usr/bin/env bash
# zen-cockpit: a second zen-inbox panel for an SSH session (the phone).
# SSH_CONNECTION keeps banners, bell and ntfy off; TERM stays as the client
# set it. Never attaches to the desk cockpit pane.
set -e

if [ -z "${SSH_CONNECTION:-}" ]; then
    echo "zen-cockpit: run it over SSH; the desk cockpit is the tmux pane" >&2
    exit 1
fi
exec zen-inbox watch
