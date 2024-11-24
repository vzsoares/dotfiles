# minimalist mocha theme

# Define color and format codes
BOLD="%B"
FG_WHITE="%{%F{#e0def4}%}"
RESET="%{$reset_color%}${FG_WHITE}" #FG_WHITE sets terminal color
FG_CYAN="%{%F{#9ccfd8}%}"
FG_BLUE="%{%F{#31748f}%}"
FG_RED="%{%F{#eb6f92}%}"
FG_GOLD="%{%F{#f6c177}%}"

# Prompt configuration
PROMPT="${BOLD}${FG_CYAN}%c${RESET}"
PROMPT+=' $(git_prompt_info)'

# Git prompt configuration
ZSH_THEME_GIT_PROMPT_PREFIX="${BOLD}${FG_BLUE}git:${BOLD}${FG_RED}"
ZSH_THEME_GIT_PROMPT_SUFFIX="${RESET} "
ZSH_THEME_GIT_PROMPT_DIRTY="${BOLD}${FG_BLUE} ${BOLD}${FG_GOLD}%1{✗%}"
ZSH_THEME_GIT_PROMPT_CLEAN="${BOLD}${FG_BLUE}"

