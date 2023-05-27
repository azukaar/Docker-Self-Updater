#!/bin/bash

rm -rf build

go build -o build/docker-self-updater src/*.go
if [ $? -ne 0 ]; then
    exit 1
fi
