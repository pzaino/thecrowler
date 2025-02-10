#!/bin/bash

# shellcheck disable=SC2124
pars="$@"

# default settings
preserve_volumes=0

# Process the arguments in pars
for arg in ${pars}; do
    case ${arg} in
        --volumes)
            preserve_volumes=1
            # remove "--volumes" from pars
            #pars=$(echo "${pars}" | sed 's/--volumes//')
            pars=${pars/--volumes/}
            ;;
    esac
done

# start cleaning up
echo "Cleaning up..."

if [ "${preserve_volumes}" -eq 1 ]; then
    echo "Preserving volumes"
    # Stop and remove containers, networks, and volumes
    docker compose down --remove-orphans
    echo "Stopping all crowler-* containers..."
    docker ps -a --format '{{.ID}} {{.Names}}' | awk '$2 ~ /^crowler-/ {print $1}' | xargs -r docker stop

    echo "Removing all crowler-* containers..."
    docker ps -a --format '{{.ID}} {{.Names}}' | awk '$2 ~ /^crowler-/ {print $1}' | xargs -r docker rm -f

    echo "Removing all crowler-* networks..."
    docker network ls --format '{{.ID}} {{.Name}}' | awk '$2 ~ /^crowler-/ {print $1}' | xargs -r docker network rm

    echo "Removing all crowler-* volumes..."
    docker volume ls --format '{{.Name}}' | awk '$1 ~ /^crowler-/ {print $1}' | xargs -r docker volume rm

    echo "Removing all crowler-* images..."
    docker images --format '{{.ID}} {{.Repository}}' | awk '$2 ~ /^crowler-/ {print $1}' | xargs -r docker rmi -f

    echo "Cleanup complete! All crowler-* resources have been removed."
    # Prune dangling images matching the naming pattern "crowler-*"
    docker images | grep -E "crowler-|selenium/" | awk '{print $3}' | xargs docker rmi
else
    echo "Removing volumes"
    # Stop and remove containers, networks, and volumes
    docker compose down -v --remove-orphans
    # remove crowler network
    echo "Stopping all crowler-* containers..."
    docker ps -a --format '{{.ID}} {{.Names}}' | awk '$2 ~ /^crowler-/ {print $1}' | xargs -r docker stop

    echo "Removing all crowler-* containers..."
    docker ps -a --format '{{.ID}} {{.Names}}' | awk '$2 ~ /^crowler-/ {print $1}' | xargs -r docker rm -f

    echo "Removing all crowler-* networks..."
    docker network ls --format '{{.ID}} {{.Name}}' | awk '$2 ~ /^crowler-/ {print $1}' | xargs -r docker network rm

    echo "Removing all crowler-* images..."
    docker images --format '{{.ID}} {{.Repository}}' | awk '$2 ~ /^crowler-/ {print $1}' | xargs -r docker rmi -f

    echo "Cleanup complete! All crowler-* resources have been removed."
fi


# Rebuild and start containers
./docker-build.sh "${pars}"
