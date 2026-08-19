# ---------------------------------------------------------------------------
# Audio feedback: chime when a long command finishes (different tone on error)
# ---------------------------------------------------------------------------
# Tunables (override in ~/.zshrc.local):
#   ZEN_BELL_THRESHOLD  seconds a command must run before it chimes
#   ZEN_BELL_VOLUME     0.0 - 1.0
#   ZEN_BELL_IGNORE     commands that never chime (interactive / long-lived)
#   ZEN_BELL_WHEN_FOCUSED  1 = chime even while you are looking at the terminal

: ${ZEN_BELL_THRESHOLD:=10}
: ${ZEN_BELL_VOLUME:=0.4}
: ${ZEN_BELL_WHEN_FOCUSED:=0}
typeset -ga ZEN_BELL_IGNORE
(( $#ZEN_BELL_IGNORE )) || ZEN_BELL_IGNORE=(
  nvim vim vi less man ssh tmux btop htop watch watchexec viddy
  claude c lazygit git-commit fzf top journalctl
)

_ZEN_BELL_SOUNDS=/usr/share/sounds/freedesktop/stereo
_zen_bell_player=${commands[pw-play]:-${commands[paplay]}}
# pw-play takes a 0.0-1.0 float; paplay wants an integer 0-65536.
if [[ ${_zen_bell_player:t} == paplay ]]; then
  _zen_bell_volarg() { print -- "--volume=$(( int(ZEN_BELL_VOLUME * 65536) ))" }
else
  _zen_bell_volarg() { print -- "--volume=$ZEN_BELL_VOLUME" }
fi

if [[ -n $_zen_bell_player ]]; then
  autoload -Uz add-zsh-hook

  # Walk up the process tree from $1 looking for pid $2 (bounded, no forks).
  _zen_bell_is_ancestor() {
    local pid=$1 target=$2 stat n=0
    local -a f
    while [[ -n $pid && $pid != 0 ]] && (( n++ < 12 )); do
      [[ $pid == $target ]] && return 0
      stat=$(</proc/$pid/stat) 2>/dev/null || return 1
      # "pid (comm) state ppid ..." - comm can contain spaces and parens,
      # so split after the LAST ')' and take the second field.
      f=(${=stat##*\) })
      pid=$f[2]
    done
    return 1
  }

  # True when this shell is the thing the user is actually looking at.
  # Fails open (returns false -> chime) on Wayland, no xdotool, or any error.
  _zen_bell_focused() {
    [[ -n $DISPLAY ]] || return 1
    (( $+commands[xdotool] )) || return 1

    local fpid
    fpid=$(xdotool getactivewindow getwindowpid 2>/dev/null) || return 1
    [[ $fpid == <-> ]] || return 1

    # Outside tmux the terminal emulator is an ancestor of this shell.
    # Inside tmux it is not (our parent is the tmux server), so anchor on the
    # tmux *client* instead - that one IS a child of the terminal emulator.
    local anchor=$$
    if [[ -n $TMUX ]]; then
      local vis
      vis=$(tmux display-message -p -t "$TMUX_PANE" \
              '#{&&:#{pane_active},#{window_active}}' 2>/dev/null) || return 1
      [[ $vis == 1 ]] || return 1   # hidden pane -> not focused
      anchor=$(tmux display-message -p -t "$TMUX_PANE" \
                 '#{client_pid}' 2>/dev/null) || return 1
      [[ $anchor == <-> ]] || return 1
    fi

    _zen_bell_is_ancestor $anchor $fpid
  }

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
    # Last, because it is the only check that costs a fork.
    (( ZEN_BELL_WHEN_FOCUSED )) || ! _zen_bell_focused || return

    local sound=complete.oga
    (( code != 0 )) && sound=dialog-error.oga
    $_zen_bell_player $(_zen_bell_volarg) \
      $_ZEN_BELL_SOUNDS/$sound >/dev/null 2>&1 &!
  }

  add-zsh-hook preexec _zen_bell_preexec
  add-zsh-hook precmd _zen_bell_precmd

  # Must run FIRST: every preceding precmd hook clobbers $?, so appended
  # ordering would make the hook see 0 for every command and never play the
  # error tone. Other plugins only ever append, so index 1 stays ours.
  precmd_functions=(_zen_bell_precmd ${precmd_functions:#_zen_bell_precmd})
fi
