#!/bin/bash

engine_count="$1"
vdi_count="$2"
prometheus="$3"
postgres="$4"

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
    environment:
      - COMPOSE_PROJECT_NAME=crowler-
      - INSTANCE_ID=\${INSTANCE_ID:-1}
      - POSTGRES_DB=\${DOCKER_POSTGRES_DB_NAME:-SitesIndex}
      - CROWLER_DB_USER=\${DOCKER_CROWLER_DB_USER:-crowler}
      - CROWLER_DB_PASSWORD=\${DOCKER_CROWLER_DB_PASSWORD}
      - POSTGRES_DB_HOST=\${DOCKER_DB_HOST:-crowler-db}
      - POSTGRES_DB_PORT=\${DOCKER_DB_PORT:-5432}
      - POSTGRES_SSL_MODE=\${DOCKER_POSTGRES_SSL_MODE:-disable}
    platform: \${DOCKER_DEFAULT_PLATFORM:-linux/amd64}
    depends_on:
      - crowler-db
    build:
      context: .
      dockerfile: Dockerfile.searchapi
    ports:
      - "8080:8080"
    volumes:
      - api_data:/app/data
    user: apiuser
    read_only: true
    healthcheck:
      test: ["CMD-SHELL", "healthCheck"]
      interval: 10s
      timeout: 5s
      retries: 5
    restart: always

  crowler-events:
    container_name: "crowler-events"
    environment:
      - COMPOSE_PROJECT_NAME=crowler-
      - INSTANCE_ID=\${INSTANCE_ID:-1}
      - POSTGRES_DB=\${DOCKER_POSTGRES_DB_NAME:-SitesIndex}
      - CROWLER_DB_USER=\${DOCKER_CROWLER_DB_USER:-crowler}
      - CROWLER_DB_PASSWORD=\${DOCKER_CROWLER_DB_PASSWORD}
      - POSTGRES_DB_HOST=\${DOCKER_DB_HOST:-crowler-db}
      - POSTGRES_DB_PORT=\${DOCKER_DB_PORT:-5432}
      - POSTGRES_SSL_MODE=\${DOCKER_POSTGRES_SSL_MODE:-disable}
    platform: \${DOCKER_DEFAULT_PLATFORM:-linux/amd64}
    depends_on:
      - crowler-db
    build:
      context: .
      dockerfile: Dockerfile.events
    ports:
      - "8082:8082"
    volumes:
      - events_data:/app/data
    user: eventsuser
    read_only: true
    healthcheck:
      test: ["CMD-SHELL", "healthCheck"]
      interval: 10s
      timeout: 5s
      retries: 5
    restart: always
EOF

# Add crowler-db
if [ "$postgres" == "yes" ]; then
    cat << EOF >> docker-compose.yml

  crowler-db:
    image: postgres:15.10-bookworm
    container_name: "crowler-db"
    ports:
      - "5432:5432"
    environment:
      - COMPOSE_PROJECT_NAME=crowler_
      - POSTGRES_DB=\${DOCKER_POSTGRES_DB_NAME:-SitesIndex}
      - POSTGRES_USER=\${DOCKER_POSTGRES_USER:-postgres}
      - POSTGRES_PASSWORD=\${DOCKER_POSTGRES_PASSWORD}
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
    restart: always
EOF
fi

# Add crowler-engine instances
for i in $(seq 1 "$engine_count"); do
    cat << EOF >> docker-compose.yml

  crowler-engine-$i:
    environment:
      - COMPOSE_PROJECT_NAME=crowler-
      - INSTANCE_ID=$i
      - SELENIUM_HOST=\${DOCKER_SELENIUM_HOST:-crowler-vdi-$i}
    build:
      context: .
      dockerfile: Dockerfile.thecrowler
    networks:
      - crowler-net
    restart: always
EOF
done

# Add crowler-vdi instances
for i in $(seq 1 "$vdi_count"); do
    HOST_PORT_START=$((4444 + (i - 1) * 10))
    cat << EOF >> docker-compose.yml

  crowler-vdi-$i:
    container_name: "crowler-vdi-$i"
    ports:
      - "${HOST_PORT_START}:4444"
    networks:
      - crowler-net
    restart: always
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
    networks:
      - crowler-net
    restart: always
EOF
fi

# Add networks and volumes
cat << EOF >> docker-compose.yml

networks:
  crowler-net:
    driver: bridge

volumes:
  api_data:
  events_data:
  db_data:
EOF

echo "docker-compose.yml has been successfully generated."
