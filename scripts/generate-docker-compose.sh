#!/bin/bash

# Get arguments passed to the script
# shellcheck disable=SC2124
pars="$@"

# Initialize variables
engine_count=""
vdi_count=""
prometheus=""
postgres=""

# Function to display usage
cmd_usage() {
    echo "Usage: $0 [OPTIONS]"
    echo "Options:"
    echo "  --engine_count=<number>  Number of crowler-engine instances"
    echo "  -e=<number>              Number of crowler-engine instances"
    echo "  --vdi_count=<number>     Number of crowler-vdi instances"
    echo "  -v=<number>              Number of crowler-vdi instances"
    echo "  --prometheus=<yes/no>    Include Prometheus PushGateway"
    echo "  --prom=<yes/no>          Include Prometheus PushGateway"
    echo "  --postgres=<yes/no>      Include PostgreSQL database"
    echo "  --pg=<yes/no>            Include PostgreSQL database"
}

# Function to read and validate integer input
read_integer_input() {
    local prompt="$1"
    local varname="$2"
    local value
    while :; do
        # shellcheck disable=SC2162
        read -p "$prompt" value
        if [[ "$value" =~ ^[0-9]+$ ]] && [ "$value" -gt 0 ]; then
            eval "$varname=$value"
            break
        else
            echo "Invalid input. Please provide a positive integer."
        fi
    done
}

# Function to read and validate yes/no input
read_yes_no_input() {
    local prompt="$1"
    local varname="$2"
    local value
    while :; do
        # shellcheck disable=SC2162
        read -p "$prompt (yes/no): " value
        value=$(echo "$value" | tr '[:upper:]' '[:lower:]' | xargs)
        if [[ "$value" == "yes" || "$value" == "no" ]]; then
            eval "$varname=$value"
            break
        else
            echo "Invalid input. Please provide 'yes' or 'no'."
        fi
    done
}

# process the arguments in pars
# shellcheck disable=SC2068
for arg in ${pars}; do
    case ${arg} in
        --help)
            cmd_usage
            exit 0
            ;;
        -h)
            cmd_usage
            exit 0
            ;;
        --engine=*)
            engine_count=${arg#--engine_count=}
            ;;
        -e=*)
            engine_count=${arg#-e=}
            ;;
        --vdi=*)
            vdi_count=${arg#--vdi_count=}
            ;;
        -v=*)
            vdi_count=${arg#-v=}
            ;;
        --prometheus=*)
            prometheus=${arg#--prometheus=}
            ;;
        --prom=*)
            prometheus=${arg#--prom=}
            ;;
        --postgres=*)
            postgres=${arg#--postgres=}
            ;;
        --pg=*)
            postgres=${arg#--pg=}
            ;;
    esac
done

# Prompt for missing arguments
if [ -z "$engine_count" ]; then
    read_integer_input "Enter the number of crowler-engine instances: " engine_count
fi
if [ -z "$vdi_count" ]; then
    read_integer_input "Enter the number of crowler-vdi instances: " vdi_count
fi
if [ -z "$prometheus" ]; then
    read_yes_no_input "Do you want to include the Prometheus PushGateway?" prometheus
fi
if [ -z "$postgres" ]; then
    read_yes_no_input "Do you want to include the PostgreSQL database?" postgres
fi

# Generate docker-compose.yml
cat << EOF > docker-compose.yml
---
version: '3.8'
services:

  crowler-api:
    container_name: "crowler-api"
    env_file:
      - .env
    environment:
      - COMPOSE_PROJECT_NAME=crowler
      - INSTANCE_ID=\${INSTANCE_ID:-1}
      - POSTGRES_DB=\${DOCKER_POSTGRES_DB_NAME:-SitesIndex}
      - CROWLER_DB_USER=\${DOCKER_CROWLER_DB_USER:-crowler}
      - CROWLER_DB_PASSWORD=\${DOCKER_CROWLER_DB_PASSWORD}
      - POSTGRES_DB_HOST=\${DOCKER_DB_HOST:-crowler-db}
      - POSTGRES_DB_PORT=\${DOCKER_DB_PORT:-5432}
      - POSTGRES_SSL_MODE=\${DOCKER_POSTGRES_SSL_MODE:-disable}
    platform: \${DOCKER_DEFAULT_PLATFORM:-linux/amd64}
    build:
      context: .
      dockerfile: Dockerfile.searchapi
    ports:
      - "8080:8080"
    networks:
      - crowler-net
    volumes:
      - api_data:/app/data
    user: apiuser
    read_only: true
    healthcheck:
      test: ["CMD-SHELL", "healthCheck"]
      interval: 10s
      timeout: 5s
      retries: 5
    restart: unless-stopped

  crowler-events:
    container_name: "crowler-events"
    env_file:
      - .env
    environment:
      - COMPOSE_PROJECT_NAME=crowler
      - INSTANCE_ID=\${INSTANCE_ID:-1}
      - POSTGRES_DB=\${DOCKER_POSTGRES_DB_NAME:-SitesIndex}
      - CROWLER_DB_USER=\${DOCKER_CROWLER_DB_USER:-crowler}
      - CROWLER_DB_PASSWORD=\${DOCKER_CROWLER_DB_PASSWORD}
      - POSTGRES_DB_HOST=\${DOCKER_DB_HOST:-crowler-db}
      - POSTGRES_DB_PORT=\${DOCKER_DB_PORT:-5432}
      - POSTGRES_SSL_MODE=\${DOCKER_POSTGRES_SSL_MODE:-disable}
    platform: \${DOCKER_DEFAULT_PLATFORM:-linux/amd64}
    build:
      context: .
      dockerfile: Dockerfile.events
    ports:
      - "8082:8082"
    networks:
      - crowler-net
    volumes:
      - events_data:/app/data
    user: eventsuser
    read_only: true
    healthcheck:
      test: ["CMD-SHELL", "healthCheck"]
      interval: 10s
      timeout: 5s
      retries: 5
    restart: unless-stopped
EOF

# Add crowler-db
# shellcheck disable=SC2086
if [ "$postgres" == "yes" ]; then
    cat << EOF >> docker-compose.yml

  crowler-db:
    image: postgres:15.10-bookworm
    container_name: "crowler-db"
    ports:
      - "5432:5432"
    env_file:
      - .env
    environment:
      - COMPOSE_PROJECT_NAME=crowler
      - POSTGRES_DB=\${DOCKER_POSTGRES_DB_NAME:-SitesIndex}
      - POSTGRES_USER=\${DOCKER_POSTGRES_USER:-postgres}
      - POSTGRES_PASSWORD=\${DOCKER_POSTGRES_PASSWORD}
      - CROWLER_DB_USER=\${DOCKER_CROWLER_DB_USER:-crowler}
      - CROWLER_DB_PASSWORD=\${DOCKER_CROWLER_DB_PASSWORD}
    volumes:
      - db_data:/var/lib/postgresql/data
      - ./pkg/database/postgresql-setup.sh:/docker-entrypoint-initdb.d/init.sh
      - ./pkg/database/postgresql-setup-v1.5.pgsql:/docker-entrypoint-initdb.d/postgresql-setup-v1.5.pgsql
    networks:
      - crowler-net
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U \$\${POSTGRES_USER}"]
      interval: 10s
      timeout: 5s
      retries: 5
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"
    restart: unless-stopped
EOF
fi

# Add crowler-engine instances
# shellcheck disable=SC2086
for i in $(seq 1 "$engine_count"); do
    ENGINE_NETWORKS=""
    for j in $(seq 1 "$vdi_count"); do
      if [ -z "$ENGINE_NETWORKS" ]; then
        ENGINE_NETWORKS="      - crowler-vdi-$j"
      else
        ENGINE_NETWORKS="$ENGINE_NETWORKS\n      - crowler-vdi-$j"
      fi
    done
    # Ensure proper YAML formatting
    ENGINE_NETWORKS=$(echo -e "$ENGINE_NETWORKS")

    cat << EOF >> docker-compose.yml

  crowler-engine-$i:
    container_name: "crowler-engine-$i"
    env_file:
      - .env
    environment:
      - COMPOSE_PROJECT_NAME=crowler
      - INSTANCE_ID=$i
      - SELENIUM_HOST=\${DOCKER_SELENIUM_HOST:-crowler-vdi-$i}
      - POSTGRES_DB=\${DOCKER_POSTGRES_DB_NAME:-SitesIndex}
      - CROWLER_DB_USER=\${DOCKER_CROWLER_DB_USER:-crowler}
      - CROWLER_DB_PASSWORD=\${DOCKER_CROWLER_DB_PASSWORD}
      - POSTGRES_DB_HOST=\${DOCKER_DB_HOST:-crowler-db}
      - POSTGRES_DB_PORT=\${DOCKER_DB_PORT:-5432}
      - POSTGRES_SSL_MODE=\${DOCKER_POSTGRES_SSL_MODE:-disable}
    platform: \${DOCKER_DEFAULT_PLATFORM:-linux/amd64}
    build:
      context: .
      dockerfile: Dockerfile.thecrowler
    networks:
      - crowler-net
$ENGINE_NETWORKS
    cap_add:
      - NET_ADMIN
      - NET_RAW
    stdin_open: true # For interactive terminal access (optional)
    tty: true        # For interactive terminal access (optional)
    volumes:
      - engine_data:/app/data
    user: crowler
    healthcheck:
      test: ["CMD-SHELL", "healthCheck"]
      interval: 10s
      timeout: 5s
      retries: 5
    restart: unless-stopped
EOF
done

# Add crowler-vdi instances
# shellcheck disable=SC2086
for i in $(seq 1 "$vdi_count"); do
    # Calculate unique host port ranges for each instance to avoid conflicts
    HOST_PORT_START1=$((4444 + (i - 1) * 2)) # VDI Selenium Hub
    HOST_PORT_END1=$((4445 + (i - 1) * 2))   # VDI SysMng Port
    HOST_PORT_START2=$((5900 + (i - 1) * 1)) # VDI VNC Port
    HOST_PORT_START3=$((7900 + (i - 1) * 1)) # VDI noVNC Port
    HOST_PORT_START4=$((9222 + (i - 1) * 1)) # VDI ChromeDP Port
    NETWORK_NAME="crowler-vdi-$i"
    cat << EOF >> docker-compose.yml

  crowler-vdi-$i:
    container_name: "crowler-vdi-$i"
    env_file:
      - .env
    environment:
      - COMPOSE_PROJECT_NAME=crowler
      - INSTANCE_ID=$i
    image: \${DOCKER_SELENIUM_IMAGE:-selenium/standalone-chrome:4.18.1-20240224}
    platform: \${DOCKER_DEFAULT_PLATFORM:-linux/amd64}
    ports:
      - "$HOST_PORT_START1-$HOST_PORT_END1:4444-4445"
      - "$HOST_PORT_START2:5900"
      - "$HOST_PORT_START3:7900"
      - "$HOST_PORT_START4:9222"
    networks:
      - $NETWORK_NAME
    restart: unless-stopped
EOF
done

# Add Prometheus PushGateway
if [ "$prometheus" == "yes" ]; then
    cat << EOF >> docker-compose.yml

  crowler-push-gateway:
    image: prom/pushgateway
    container_name: "crowler-push-gateway"
    ports:
      - "9091:9091"
    env_file:
      - .env
    environment:
      - COMPOSE_PROJECT_NAME=crowler
    networks:
      - crowler-net
    restart: unless-stopped
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"
EOF
fi

# Add networks and volumes
cat << EOF >> docker-compose.yml

networks:
  crowler-net:
    driver: bridge
EOF

# Add all dynamically created networks for VDIs
for i in $(seq 1 "$vdi_count"); do
    cat << EOF >> docker-compose.yml
  crowler-vdi-$i:
    driver: bridge
EOF
done

# Add Static Volumes
cat << EOF >> docker-compose.yml

volumes:
  api_data:
  events_data:
EOF

if [ "$postgres" == "yes" ]; then
    cat << EOF >> docker-compose.yml
  db_data:
    driver: local
EOF
fi

cat << EOF >> docker-compose.yml
  engine_data:
    driver: local
EOF

echo "docker-compose.yml has been successfully generated."
