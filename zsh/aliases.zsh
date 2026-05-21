# AWS
alias aws-approva-dev="export AWS_PROFILE=approva-dev"
alias aws-approva-prod="export AWS_PROFILE=approva-prod"
alias aws-myself="export AWS_PROFILE=myself"

alias aws-approva-dev-auth="aws login -- profile approva-dev"
alias aws-approva-prod-auth="aws login -- profile approva-prod"

alias aws-approva-dev-go="export AWS_PROFILE=approva-dev && aws login -- profile approva-dev"
alias aws-approva-prod-go="export AWS_PROFILE=approva-prod && aws login -- profile approva-prod"

# Tools
alias ts="tsx"
alias n="nvim"
alias tf="terraform"

# Python
alias sourcepy="source .venv/bin/activate"

# Scripts
alias run="$HOME/code/personal/dotfiles/scripts/run.sh"

# Claude
alias claude-yolo='ENABLE_BACKGROUND_TASKS=1 claude --dangerously-skip-permissions --allowedTools "*"'
alias claude-commit="$HOME/code/personal/dotfiles/scripts/commit.sh"
