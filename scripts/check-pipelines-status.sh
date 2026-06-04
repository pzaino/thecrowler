#!/bin/bash

# This simple script extracts pipelines status from the whole logs on docker for a given engine. It looks for lines between "=====" and "=====" which are the markers for pipelines status in the logs.

engine="$1"
if [ "$engine" == "" ];
then
        engine="1"
fi

docker logs crowler-engine-${engine} 2>&1 | sed -n '/^=====/,/^=====/p'
