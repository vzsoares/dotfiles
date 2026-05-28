#!/usr/bin/env bash

while [ : ]; do
    screenkey -s small -p fixed -g 20%x10%+79%-50% --opacity 0.4
    pid=$!
    sleep 10
    kill $pid
done

