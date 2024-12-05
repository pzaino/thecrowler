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
    docker-compose down --remove-orphans
    # remove crowler network
    docker network rm crowler-net
    docker network rm thecrowler_crowler-net
    # Prune dangling images matching the naming pattern "crowler-*"
    docker images --filter "dangling=true" | grep "crowler-" | awk '{print $3}' | xargs docker rmi
else
    echo "Removing volumes"
    # Stop and remove containers, networks, and volumes
    docker-compose down -v --remove-orphans
    # remove crowler network
    docker network rm crowler-net
    docker network rm thecrowler_crowler-net
    # Remove all unused images
    #docker image prune -a -f
    docker images --filter "dangling=true" | grep "crowler-" | awk '{print $3}' | xargs docker rmi
fi


# Rebuild and start containers
./docker-build.sh "${pars}"
