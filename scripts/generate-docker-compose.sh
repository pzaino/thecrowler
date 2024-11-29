#!/bin/bash

engine_count="$1"
vdi_count="$2"
prometheus="$3"

# If no arguments are provided, ask the user for the number of instances
if [ -z "$engine_count" ] || [ -z "$vdi_count" ]; then
    # Ask the user for the number of instances
    # shellcheck disable=SC2162
    read -p "Enter the number of crowler_engine instances: " engine_count
    # shellcheck disable=SC2162
    read -p "Enter the number of crowler_vdi instances: " vdi_count
    # Ask the user if they want to include the Prometheus PushGateway
    # shellcheck disable=SC2162
    read -p "Do you want to include the Prometheus PushGateway? (yes/no): " prometheus
fi

# trim trail spaces in prometheus input and transform to lowercase
prometheus=$(echo "$prometheus" | tr '[:upper:]' '[:lower:]' | xargs)

# If no value is provided for prometheus, set it to "no"
if [ -z "$prometheus" ]; then
    prometheus="no"
fi
if [ "$prometheus" != "yes" ] && [ "$prometheus" != "no" ]; then
    echo "Invalid input. Please provide 'yes' or 'no' for the Prometheus PushGateway."
    exit 1
fi

# Check if engine_count and vdi_count are provided
if [ -z "$engine_count" ] || [ -z "$vdi_count" ]; then
    echo "Invalid input. Please provide the number of instances for both crowler_engine and crowler_vdi."
    exit 1
fi

# Check if engine_count and vdi_count are integers and their value is bigger than 0
if ! [[ "$engine_count" =~ ^[0-9]+$ ]] || ! [[ "$vdi_count" =~ ^[0-9]+$ ]] || [ "$engine_count" -le 0 ] || [ "$vdi_count" -le 0 ]; then
    echo "Invalid input. Please provide a positive integer value for both crowler_engine and crowler_vdi."
    exit 1
fi

# Start writing the docker-compose.generated.yml file
cat << EOF > docker-compose.yml
---
version: '3.8'
services:
  crowler_db:
    image: postgres:alpine3.20
    ports:
      - "5432:5432"
    environment:
      - COMPOSE_PROJECT_NAME=crowler_
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
    restart: always

  crowler_api:
    environment:
      - COMPOSE_PROJECT_NAME=crowler_
      - INSTANCE_ID=\${INSTANCE_ID:-1}
      - POSTGRES_DB=\${DOCKER_POSTGRES_DB_NAME:-SitesIndex}
      - CROWLER_DB_USER=\${DOCKER_CROWLER_DB_USER:-crowler}
      - CROWLER_DB_PASSWORD=\${DOCKER_CROWLER_DB_PASSWORD}
      - POSTGRES_DB_HOST=\${DOCKER_DB_HOST:-crowler_db}
      - POSTGRES_DB_PORT=\${DOCKER_DB_PORT:-5432}
      - POSTGRES_SSL_MODE=\${DOCKER_POSTGRES_SSL_MODE:-disable}
    platform: \${DOCKER_DEFAULT_PLATFORM:-linux/amd64}
    depends_on:
      - crowler_db
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

  crowler_events:
    environment:
      - COMPOSE_PROJECT_NAME=crowler_
      - INSTANCE_ID=\${INSTANCE_ID:-1}
      - POSTGRES_DB=\${DOCKER_POSTGRES_DB_NAME:-SitesIndex}
      - CROWLER_DB_USER=\${DOCKER_CROWLER_DB_USER:-crowler}
      - CROWLER_DB_PASSWORD=\${DOCKER_CROWLER_DB_PASSWORD}
      - POSTGRES_DB_HOST=\${DOCKER_DB_HOST:-crowler_db}
      - POSTGRES_DB_PORT=\${DOCKER_DB_PORT:-5432}
      - POSTGRES_SSL_MODE=\${DOCKER_POSTGRES_SSL_MODE:-disable}
    platform: \${DOCKER_DEFAULT_PLATFORM:-linux/amd64}
    depends_on:
      - crowler_db
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

# Add the crowler_engine instances
# shellcheck disable=SC2086
for i in $(seq 1 $engine_count); do
cat << EOF >> docker-compose.yml

  crowler_engine_$i:
    environment:
      - COMPOSE_PROJECT_NAME=crowler_
      - INSTANCE_ID=$i
      - SELENIUM_HOST=\${DOCKER_SELENIUM_HOST:-crowler_vdi_$i}
      - POSTGRES_DB=\${DOCKER_POSTGRES_DB_NAME:-SitesIndex}
      - CROWLER_DB_USER=\${DOCKER_CROWLER_DB_USER:-crowler}
      - CROWLER_DB_PASSWORD=\${DOCKER_CROWLER_DB_PASSWORD}
      - POSTGRES_DB_HOST=\${DOCKER_DB_HOST:-crowler_db}
      - POSTGRES_DB_PORT=\${DOCKER_DB_PORT:-5432}
      - POSTGRES_SSL_MODE=\${DOCKER_POSTGRES_SSL_MODE:-disable}
    platform: \${DOCKER_DEFAULT_PLATFORM:-linux/amd64}
    depends_on:
      - crowler_db
    build:
      context: .
      dockerfile: Dockerfile.thecrowler
    networks:
      - crowler-net
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
    restart: always
EOF
done

# Add the crowler_vdi instances
# shellcheck disable=SC2086
for i in $(seq 1 $vdi_count); do
  # Calculate unique host port ranges for each instance to avoid conflicts
  HOST_PORT_START=$((4442 + (i - 1) * 3))
  HOST_PORT_END=$((4444 + (i - 1) * 3))

cat << EOF >> docker-compose.yml

  crowler_vdi_$i:
    container_name: "crowler_vdi_$i"
    environment:
      - COMPOSE_PROJECT_NAME=crowler_
      - INSTANCE_ID=$i
    image: \${DOCKER_SELENIUM_IMAGE:-selenium/standalone-chrome:4.18.1-20240224}
    platform: \${DOCKER_DEFAULT_PLATFORM:-linux/amd64}
    ports:
      - "$HOST_PORT_START-$HOST_PORT_END:4442-4444"
    networks:
      - crowler-net
    restart: always
EOF
done

# Add the Prometheus PushGateway (if requested by the user)
# shellcheck disable=SC2086
if [ $prometheus == "yes" ];
then
  cat << EOF >> docker-compose.yml

    pushgateway:
      image: prom/pushgateway
      container_name: pushgateway
      ports:
        - "9091:9091"
      networks:
        - crowler-net
      restart: always
      logging:
        driver: "json-file"
        options:
          max-size: "10m"
          max-file: "3"
EOF
fi

# Add the networks and volumes
cat << EOF >> docker-compose.yml

networks:
  crowler-net:
    driver: bridge

volumes:
  api_data:
  events_data:
  db_data:
    driver: local
  engine_data:
    driver: local
EOF

echo "docker-compose.generated.yml has been generated with $engine_count crowler_engine instance(s) and $vdi_count crowler_vdi instance(s)."
