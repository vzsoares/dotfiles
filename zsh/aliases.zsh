# AWS
alias aws-approva-dev="export AWS_PROFILE=approva-dev"
alias aws-approva-prod="export AWS_PROFILE=approva-prod"
alias aws-myself="export AWS_PROFILE=myself"

# Tools
alias ts="tsx"
alias n="nvim"
alias tf="terraform"

# Python
alias sourcepy="source .venv/bin/activate"

# Claude
alias claude-yolo='ENABLE_BACKGROUND_TASKS=1 claude --dangerously-skip-permissions --allowedTools "*"'
alias claude-commit='git diff --cached | claude -p "Write a concise Conventional Commit message for these changes. Return only the message."'
