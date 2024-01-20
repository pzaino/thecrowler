#!/bin/bash

# This script is used to build the project automatically.

rm -rf ./bin
mkdir ./bin

go build
rval=$?
if [ "${rval}" == "0" ]; then
    echo "TheCrow built successfully!"
else
    echo "TheCrow build failed!"
    exit $rval
fi

mv TheCrow ./bin

go build ./cmd/addSite
rval=$?
if [ "${rval}" == "0" ]; then
    echo "addSite command line tool built successfully!"
else
    echo "addASite command line tool build failed!"
    exit $rval
fi

mv addSite ./bin

go build ./cmd/removeSite
rval=$?
if [ "${rval}" == "0" ]; then
    echo "removeSite command line tool built successfully!"
else
    echo "removeSite command line tool build failed!"
    exit $rval
fi

mv removeSite ./bin

exit $rval

# Path: autobuild.sh
