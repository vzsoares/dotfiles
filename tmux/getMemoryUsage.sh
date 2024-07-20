#!/bin/bash

free -m | awk 'NR==2{printf "%.1fGB\n", $3/1024 }'
