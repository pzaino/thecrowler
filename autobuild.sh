#!/bin/bash

# This script is used to build the project automatically.

# Utility functions
function checkError() {
    if [ "$?" != "0" ]; then
        echo "Error: $1"
        exit 1
    fi
}

function moveFile() {
    # Copy a file from one directory to another in a case-insensitive way
    # Usage: copyFile <source> <destination>
    local src_dir
    src_dir="$(dirname "$1")"
    local src_file
    src_file="$(basename "$1")"
    local dest
    dest="$2"

    # Find the file in a case-insensitive way and copy it
    find "$src_dir" -maxdepth 1 -iname "$src_file" -exec mv {} "$dest" \;
}

# Check if the go command is installed
which go
checkError "Go is not installed!"

# Clean up the bin directory
rm -rf ./bin
mkdir ./bin

go build
rval=$?
if [ "${rval}" == "0" ]; then
    echo "TheCrowler built successfully!"
    moveFile TheCrowler ./bin
else
    echo "TheCrowler build failed!"
    exit $rval
fi

go build ./cmd/addSite
rval=$?
if [ "${rval}" == "0" ]; then
    echo "addSite command line tool built successfully!"
    moveFile addSite ./bin
else
    echo "addASite command line tool build failed!"
    exit $rval
fi

go build ./cmd/removeSite
rval=$?
if [ "${rval}" == "0" ]; then
    echo "removeSite command line tool built successfully!"
    moveFile removeSite ./bin
else
    echo "removeSite command line tool build failed!"
    exit $rval
fi

go build ./services/api
rval=$?
if [ "${rval}" == "0" ]; then
    echo "api command line tool built successfully!"
    moveFile api ./bin
else
    echo "api command line tool build failed!"
    exit $rval
fi

exit $rval

# Path: autobuild.sh
