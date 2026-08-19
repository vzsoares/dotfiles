# ---------------------------------------------------------------------------
# Audio feedback: chime when a long command finishes (different tone on error)
# ---------------------------------------------------------------------------
# The sound + "am I looking at this window?" logic lives in `zen-bell`
# (scripts/bell.sh), shared with the Claude Code hooks in .claude/settings.json.
#
# Tunables (override in ~/.zshrc.local):
#   ZEN_BELL_THRESHOLD  seconds a command must run before it chimes
#   ZEN_BELL_VOLUME     0.0 - 1.0            (read by zen-bell)
#   ZEN_BELL_IGNORE     commands that never chime (interactive / long-lived)
#   ZEN_BELL_WHEN_FOCUSED  1 = chime even while you are looking at the terminal

: ${ZEN_BELL_THRESHOLD:=10}
typeset -ga ZEN_BELL_IGNORE
(( $#ZEN_BELL_IGNORE )) || ZEN_BELL_IGNORE=(
  nvim vim vi less man ssh tmux btop htop watch watchexec viddy
  claude c lazygit git-commit fzf top journalctl
)

if (( $+commands[zen-bell] )); then
  autoload -Uz add-zsh-hook

  _zen_bell_preexec() {
    _zen_bell_start=$SECONDS
    # $1 is the raw command line; match on the first word, skipping env
    # assignments and sudo so `sudo nvim` is still ignored.
    local -a words
    words=(${(z)1})
    local w
    for w in $words; do
      case $w in
        *=*|sudo|command|doas) continue ;;
        *) _zen_bell_cmd=${w:t}; break ;;
      esac
    done
  }

  _zen_bell_precmd() {
    local code=$?
    [[ -n $_zen_bell_start ]] || return
    local elapsed=$(( SECONDS - _zen_bell_start ))
    local cmd=$_zen_bell_cmd
    unset _zen_bell_start _zen_bell_cmd

    (( elapsed >= ZEN_BELL_THRESHOLD )) || return
    (( ${ZEN_BELL_IGNORE[(Ie)$cmd]} )) && return

    local sound=complete
    (( code != 0 )) && sound=dialog-error
    zen-bell $sound >/dev/null 2>&1 &!
  }

  add-zsh-hook preexec _zen_bell_preexec
  add-zsh-hook precmd _zen_bell_precmd

  # Must run FIRST: every preceding precmd hook clobbers $?, so appended
  # ordering would make the hook see 0 for every command and never play the
  # error tone. Other plugins only ever append, so index 1 stays ours.
  precmd_functions=(_zen_bell_precmd ${precmd_functions:#_zen_bell_precmd})
fi
