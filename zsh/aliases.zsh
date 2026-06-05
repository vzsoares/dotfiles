# AWS
alias aws-approva-dev="export AWS_PROFILE=approva-dev"
alias aws-approva-prod="export AWS_PROFILE=approva-prod"
alias aws-myself="export AWS_PROFILE=myself"

alias aws-approva-dev-go="export AWS_PROFILE=approva-dev && aws login --profile approva-dev && eval \"\$(aws configure export-credentials --profile approva-dev --format env)\""
alias aws-approva-prod-go="export AWS_PROFILE=approva-prod && aws login --profile approva-prod && eval \"\$(aws configure export-credentials --profile approva-prod --format env)\""

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
