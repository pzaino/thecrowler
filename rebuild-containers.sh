#!/bin/bash

# Stop and remove containers, networks, and volumes
docker-compose down -v

# Remove all unused images
docker image prune -a -f

# Rebuild and start containers
docker-compose up --build
