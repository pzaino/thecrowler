#!/bin/bash

state_up="$1"

# Stop and remove containers, networks, and volumes
docker-compose down -v

# Remove all unused images
docker image prune -a -f

# Rebuild and start containers
./docker-build.sh "${state_up}"

