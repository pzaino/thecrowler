#!/bin/bash

# This script is used to build the project automatically.
build_objs="$1"
rval=0

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

if  [ "${build_objs}" == "all" ] ||
    [ "${build_objs}" == "engine" ] ||
    [ "${build_objs}" == "cr" ] ||
    [ "${build_objs}" == "" ];
then
    CGO_ENABLED=0 go build
    rval=$?
    if [ "${rval}" == "0" ]; then
        echo "TheCrowler built successfully!"
        moveFile TheCrowler ./bin
    else
        echo "TheCrowler build failed!"
        exit $rval
    fi
fi

if  [ "${build_objs}" == "all" ] ||
    [ "${build_objs}" == "addSource" ] ||
    [ "${build_objs}" == "as" ] ||
    [ "${build_objs}" == "" ];
then
    cmd_name="addSource"
    CGO_ENABLED=0 go build ./cmd/${cmd_name}
    rval=$?
    if [ "${rval}" == "0" ]; then
        echo "${cmd_name} command line tool built successfully!"
        moveFile ${cmd_name} ./bin
    else
        echo "${cmd_name} command line tool build failed!"
        exit $rval
    fi
fi

if  [ "${build_objs}" == "all" ] ||
    [ "${build_objs}" == "addCategory" ] ||
    [ "${build_objs}" == "ac" ] ||
    [ "${build_objs}" == "" ];
then
    cmd_name="addCategory"
    CGO_ENABLED=0 go build ./cmd/${cmd_name}
    rval=$?
    if [ "${rval}" == "0" ]; then
        echo "${cmd_name} command line tool built successfully!"
        moveFile ${cmd_name} ./bin
    else
        echo "${cmd_name} command line tool build failed!"
        exit $rval
    fi
fi

if  [ "${build_objs}" == "all" ] ||
    [ "${build_objs}" == "removeSource" ] ||
    [ "${build_objs}" == "rs" ] ||
    [ "${build_objs}" == "" ];
then
    cmd_name="removeSource"
    CGO_ENABLED=0 go build ./cmd/${cmd_name}
    rval=$?
    if [ "${rval}" == "0" ]; then
        echo "${cmd_name} command line tool built successfully!"
        moveFile ${cmd_name} ./bin
    else
        echo "${cmd_name} command line tool build failed!"
        exit $rval
    fi
fi

if  [ "${build_objs}" == "all" ] ||
    [ "${build_objs}" == "api" ] ||
    [ "${build_objs}" == "" ];
then
    cmd_name="api"
    CGO_ENABLED=0 go build ./services/${cmd_name}
    rval=$?
    if [ "${rval}" == "0" ]; then
        echo "${cmd_name} command line tool built successfully!"
        moveFile ${cmd_name} ./bin
    else
        echo "${cmd_name} command line tool build failed!"
        exit $rval
    fi
fi

if  [ "${build_objs}" == "all" ] ||
    [ "${build_objs}" == "events" ] ||
    [ "${build_objs}" == "" ];
then
    cmd_name="events"
    CGO_ENABLED=0 go build ./services/${cmd_name}
    rval=$?
    if [ "${rval}" == "0" ]; then
        echo "${cmd_name} command line tool built successfully!"
        moveFile ${cmd_name} ./bin
    else
        echo "${cmd_name} command line tool build failed!"
        exit $rval
    fi
fi

if  [ "${build_objs}" == "all" ] ||
    [ "${build_objs}" == "healthCheck" ] ||
    [ "${build_objs}" == "ec" ] ||
    [ "${build_objs}" == "" ];
then
    cmd_name="healthCheck"
    CGO_ENABLED=0 go build ./cmd/${cmd_name}
    rval=$?
    if [ "${rval}" == "0" ]; then
        echo "${cmd_name} command line tool built successfully!"
        moveFile ${cmd_name} ./bin
    else
        echo "${cmd_name} command line tool build failed!"
        exit $rval
    fi
fi

if  [ "${build_objs}" == "all" ] ||
    [ "${build_objs}" == "runPlgTests" ] ||
    [ "${build_objs}" == "rpt" ] ||
    [ "${build_objs}" == "" ];
then
    cmd_name="runPluginTests"
    CGO_ENABLED=0 go build ./cmd/${cmd_name}
    rval=$?
    if [ "${rval}" == "0" ]; then
        echo "${cmd_name} command line tool built successfully!"
        moveFile ${cmd_name} ./bin
    else
        echo "${cmd_name} command line tool build failed!"
        exit $rval
    fi
fi

exit "$rval"

# Path: autobuild.sh
