#!/bin/bash

# free -m | awk 'NR==2{printf "%.1f/%.1fGB\n", $3/1024, $2/1024 }'
free -m | awk 'NR==2{printf "%.1fGB\n", $3/1024 }'
