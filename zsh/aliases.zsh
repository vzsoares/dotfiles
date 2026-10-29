# AWS
# `aws login` leaves a login_session only the AWS CLI reads; Terraform's AWS
# provider (v5) fails with "No valid credential sources". aws-use also exports
# the session as short-lived keys, so every tool in the shell uses it.
aws-use() {
  unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_CREDENTIAL_EXPIRATION
  export AWS_PROFILE="$1"
  local creds
  if ! creds=$(aws configure export-credentials --profile "$1" --format env 2>/dev/null); then
    echo "aws-use: no session for $1 — run its -go alias (aws login) first" >&2
    return 1
  fi
  eval "$creds"
  echo "aws-use: $1, keys valid until ${AWS_CREDENTIAL_EXPIRATION:-unknown}"
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

# Python
alias sourcepy="source .venv/bin/activate"

# Scripts
alias run="$HOME/code/personal/dotfiles/scripts/run.sh"

# Claude
alias claude-yolo='ENABLE_BACKGROUND_TASKS=1 claude --dangerously-skip-permissions --allowedTools "*"'
alias c='claude'
