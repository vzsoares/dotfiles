# AWS
# `aws login` leaves a login_session only the AWS CLI reads, and the keys it
# vends last 15 min. Profiles used by SDKs (Terraform) wrap the -login profile:
#   credential_process = aws configure export-credentials \
#     --profile <name>-login --format process
# so every tool refreshes keys for the whole login session (~12h).
# Exported keys would override the profile and freeze, so aws-use only sets it.
aws-use() {
  unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_CREDENTIAL_EXPIRATION
  export AWS_PROFILE="$1"
  if ! aws configure export-credentials --profile "$1" >/dev/null 2>&1; then
    echo "aws-use: no session for $1 — run its -go alias (aws login) first" >&2
    return 1
  fi
  echo "aws-use: $1 (keys auto-refresh until the aws login session ends)"
}

alias aws-approva-dev="aws-use approva-dev"
alias aws-approva-prod="aws-use approva-prod"
alias aws-zenhalab-prod="aws-use zenhalab-prod-go"

alias aws-approva-dev-go="aws login --profile approva-dev-login && aws-use approva-dev"
alias aws-approva-prod-go="aws login --profile approva-prod-login && aws-use approva-prod"
alias aws-zenhalab-prod-go="aws login --profile zenhalab-prod-login && aws-use zenhalab-prod"

# Tools
alias ts="tsx"
alias n="nvim"
alias tf="terraform"
alias yayu="yay --sudoloop --save --noconfirm -Syu"
# New Alacritty window that skips the tmux auto-start in .zshrc
alias notmux="NO_TMUX=1 alacritty >/dev/null 2>&1 &!"

# Python
alias sourcepy="source .venv/bin/activate"

# Scripts
alias run="$HOME/code/personal/dotfiles/scripts/run.sh"

# Claude
alias claude-yolo='ENABLE_BACKGROUND_TASKS=1 claude --dangerously-skip-permissions --allowedTools "*"'
alias c='claude'
alias cy='claude-yolo'

# agent-manager keeps config in SQLite; export it to tracked JSON on quit
am() {
  agent-manager "$@"
  local rc=$?
  if [ $# -eq 0 ]; then
    (cd "$HOME/code/personal/dotfiles/agent-manager" && ./link export >/dev/null)
    git -C "$HOME/code/personal/dotfiles" diff --quiet -- agent-manager ||
      echo "am: settings changed, review with git diff in dotfiles"
  fi
  return $rc
}
