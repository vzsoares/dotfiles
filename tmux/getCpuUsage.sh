#!/bin/bash

# top -bn1 | grep "Cpu(s)" | \
#            sed "s/.*, *\([0-9.]*\)%* id.*/\1/" | \
#            awk '{printf "%02d%\n", 100 - $1}'

i3status --run-once -c "~/.config/tmux/.i3status.conf" 
