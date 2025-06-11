#!/bin/bash

# Get arguments passed to the script
# shellcheck disable=SC2124
pars="$@"

# Initialize variables
engine_count=""
vdi_count=""
prometheus=""
postgres=""
cpu_limit=""
cpu_limit_engine=""
cpu_limit_vdi=""
cpu_limit_mng=""
no_api=0
no_events=0
no_jaeger=0
mem_limit_vdi_pct=""
mem_limit_eng_pct=""
mem_limit_mng_pct=""
mem_limit_tlm_pct=""
use_swarm="no"

# Function to display usage
cmd_usage() {
    echo "Usage: $0 [OPTIONS]"
    echo "Options:"
    echo "  --engine_count=<number>     Number of crowler-engine instances"
    echo "  -e=<number>                 Number of crowler-engine instances"
    echo "  --vdi_count=<number>        Number of crowler-vdi instances"
    echo "  -v=<number>                 Number of crowler-vdi instances"
    echo "  --prometheus=<yes/no>       Include Prometheus PushGateway"
    echo "  --prom=<yes/no>             Include Prometheus PushGateway"
    echo "  --postgres=<yes/no>         Include PostgreSQL database"
    echo "  --pg=<yes/no>               Include PostgreSQL database"
    echo "  --cpu_limit=<number>        CPU limit for all services"
    echo "  --cpu_limit_engine=<number> CPU limit for crowler-engine instances"
    echo "  --cpu_limit_vdi=<number>    CPU limit for crowler-vdi instances"
    echo "  --cpu_limit_mng=<number>    CPU limit for crowler-api and crowler-events"
    echo "  --no_api                    Do not include crowler-api"
    echo "  --no_events                 Do not include crowler-events"
    echo "  --no_jaeger                 Do not include jaeger"
    echo "  --mem_limit_vdi=<number>    Memory limit for crowler-vdi instances in %"
    echo "  --mem_limit_engine=<number> Memory limit for crowler-engine instances in %"
    echo "  --mem_limit_mng=<number>    Memory limit for crowler-api and crowler-events in %"
    echo "  --mem_limit_tlm=<number>    Memory limit for jaeger and prometheus gateway instances in %"
    echo "  --swarm=<yes/no>            Use Docker Swarm mode"
}

# Function to read and validate integer input
read_integer_input() {
    local prompt="$1"
    local varname="$2"
    local value
    while :; do
        # shellcheck disable=SC2162
        read -p "$prompt" value
        if [[ "$value" =~ ^[0-9]+$ ]] && [ "$value" -ge 0 ]; then
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

# Function that returns today's date as yyyymmdd number
get_date() {
    date +"%Y%m%d"
}

# Detect number of logical CPUs in a portable way
detect_cpu_count() {
  if command -v nproc >/dev/null 2>&1; then
    nproc --all
  elif [[ "$OSTYPE" == "darwin"* ]]; then
    sysctl -n hw.logicalcpu
  elif command -v getconf >/dev/null 2>&1; then
    getconf _NPROCESSORS_ONLN
  else
    echo "1"  # Fallback to 1 if detection fails
  fi
}

# Detect the total memory in MB in a portable way
detect_total_memory_mb() {
  if command -v free >/dev/null 2>&1; then
    free -m | awk '/^Mem:/ { print $2 }'
  elif [[ "$OSTYPE" == "darwin"* ]]; then
    sysctl -n hw.memsize | awk '{print int($1 / 1024 / 1024)}'
  else
    echo "2048" # Fallback to 2GB
  fi
}

to_mem_unit() {
  local mb=$1
  if [ "$mb" -ge 1024 ]; then
    echo "$((mb / 1024))g"
  else
    echo "${mb}m"
  fi
}

emit_limits() {
  local indent="$1"
  local cpus="$2"
  local memory="$3"

  if [ "$use_swarm" == "yes" ]; then
    echo "$indent""deploy:"
    echo "$indent  resources:"
    echo "$indent    limits:"
    echo "$indent      cpus: \"$cpus\""
    echo "$indent      memory: \"$memory\""
  else
    echo "$indent""cpus: \"$cpus\""
    echo "$indent""mem_limit: \"$memory\""
  fi
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
        --cpu_limit=*|--cpu=*|--cpu-limit=*)
            cpu_limit="${arg#*=}"
            ;;
        --cpu_limit_engine=*)
            cpu_limit_engine="${arg#*=}"
            ;;
        --cpu_limit_vdi=*)
            cpu_limit_vdi="${arg#*=}"
            ;;
        --cpu_limit_mng=*)
            cpu_limit_mng="${arg#*=}"
            ;;
        --no_api)
            no_api=1
            ;;
        --no_events)
            no_events=1
            ;;
        --no_jaeger)
            no_jaeger=1
            ;;
        --mem_limit_vdi=*)
            mem_limit_vdi_pct="${arg#*=}"
            ;;
        --mem_limit_engine=*)
            mem_limit_eng_pct="${arg#*=}"
            ;;
        --mem_limit_mng=*)
            mem_limit_mng_pct="${arg#*=}"
            ;;
        --mem_limit_tlm=*)
            mem_limit_tlm_pct="${arg#*=}"
            ;;
        --swarm=*)
            use_swarm=${arg#*=}
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

# Check memory % values if provided:
# shellcheck disable=SC2235
for pct in "$mem_limit_vdi_pct" "$mem_limit_eng_pct" "$mem_limit_mng_pct" "$mem_limit_tlm_pct"; do
  if [ -n "$pct" ] && ([ "$pct" -lt 1 ] || [ "$pct" -gt 100 ]); then
    echo "ERROR: Memory limit percentages must be between 1 and 100."
    exit 1
  fi
done

# Automatically set CPU limit to total available cores if not set
total_cpus=$(detect_cpu_count)

if [ -z "$cpu_limit" ]; then
    cpu_limit="$total_cpus"
fi

# Set default values for CPU limits if not provided
cpu_limit_engine=${cpu_limit_engine:-$cpu_limit}
cpu_limit_vdi=${cpu_limit_vdi:-$cpu_limit}
cpu_limit_mng=${cpu_limit_mng:-$cpu_limit}

# Automatically set memory limits to 80% of total memory if not set
total_memory_mb=$(detect_total_memory_mb)
if [ -z "$mem_limit_vdi_pct" ]; then
    mem_limit_vdi_pct=$((total_memory_mb * 80 / 100))
else
    mem_limit_vdi_pct=$((total_memory_mb * mem_limit_vdi_pct / 100))
fi
if [ -z "$mem_limit_eng_pct" ]; then
    mem_limit_eng_pct=$((total_memory_mb * 80 / 100))
else
    mem_limit_eng_pct=$((total_memory_mb * mem_limit_eng_pct / 100))
fi
if [ -z "$mem_limit_mng_pct" ]; then
    mem_limit_mng_pct=$((total_memory_mb * 80 / 100))
else
    mem_limit_mng_pct=$((total_memory_mb * mem_limit_mng_pct / 100))
fi
if [ -z "$mem_limit_tlm_pct" ]; then
    mem_limit_tlm_pct=$((total_memory_mb * 80 / 100))
else
    mem_limit_tlm_pct=$((total_memory_mb * mem_limit_tlm_pct / 100))
fi

# Convert to appropriate mem units
mem_limit_vdi_pct=$(to_mem_unit "$mem_limit_vdi_pct")
mem_limit_eng_pct=$(to_mem_unit "$mem_limit_eng_pct")
mem_limit_mng_pct=$(to_mem_unit "$mem_limit_mng_pct")
mem_limit_tlm_pct=$(to_mem_unit "$mem_limit_tlm_pct")

# Generate extra tags for swarm mode
if [ "$use_swarm" != "yes" ]; then
  platform="platform: \${DOCKER_DEFAULT_PLATFORM:-linux/amd64}"
  pull_policy_never="pull_policy: never"
  net_driver="bridge"
else
  platform=""
  pull_policy_never=""
  net_driver="overlay"
fi

# Generate docker-compose.yml
cat << EOF > docker-compose.yml
---
services:
EOF

# Add crowler-api and crowler-events if not disabled
if [ "$no_api" == "0" ]; then
    cat << EOF >> docker-compose.yml

  crowler-api:
    container_name: "crowler-api"
$(emit_limits "    " "${cpu_limit_mng:-1.0}" "${mem_limit_mng_pct:-2g}")
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
      - TZ=\${VDI_TZ:-UTC}
      - MICROSERVICE_NAME=crowler-api
    build:
      context: .
      dockerfile: Dockerfile.searchapi
    ${platform}
    image: crowler-api
    ${pull_policy_never}
    stdin_open: true # For interactive terminal access (optional)
    tty: true        # For interactive terminal access (optional)
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
EOF
fi
if [ "$no_events" == "0" ]; then
    cat << EOF >> docker-compose.yml

  crowler-events:
    container_name: "crowler-events"
$(emit_limits "    " "${cpu_limit_mng:-1.0}" "${mem_limit_mng_pct:-2g}")
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
      - TZ=\${VDI_TZ:-UTC}
      - MICROSERVICE_NAME=crowler-events
    build:
      context: .
      dockerfile: Dockerfile.events
    ${platform}
    image: crowler-events
    ${pull_policy_never}
    stdin_open: true # For interactive terminal access (optional)
    tty: true        # For interactive terminal access (optional)
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
fi

# Add crowler-db
# shellcheck disable=SC2086
if [ "$postgres" == "yes" ]; then
    cat << EOF >> docker-compose.yml

  crowler-db:
    image: postgres:15.10-bookworm
    container_name: "crowler-db"
$(emit_limits "    " "${cpu_limit_mng:-1.0}" "${mem_limit_mng_pct:-3g}")
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
      - PROXY_SERVICE=\${VDI_PROXY_SERVICE:-}
      - TZ=\${VDI_TZ:-UTC}
      - MICROSERVICE_NAME=crowler-db
    command: ["postgres", "-c", "timezone=\${VDI_TZ:-UTC}"]
    ${platform}
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
if [ "$engine_count" != "0" ]; then
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
$(emit_limits "    " "${cpu_limit_eng:-1.0}" "${mem_limit_eng_pct:-2g}")
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
      - TZ=\${VDI_TZ:-UTC}
      - MICROSERVICE_NAME=crowler-engine-$i
    build:
      context: .
      dockerfile: Dockerfile.thecrowler
    ${platform}
    image: crowler-engine-$i
    ${pull_policy_never}
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
fi

# Add Jaeger service if required
if [ "$vdi_count" != "0" ] && [ "$no_jaeger" == "0" ]; then
    cat << EOF >> docker-compose.yml

  crowler-jaeger:
    image: jaegertracing/all-in-one:1.54
    container_name: "crowler-jaeger"
    environment:
      - COLLECTOR_ZIPKIN_HTTP_PORT=9411
      - JAEGER_AGENT_HOST=crowler-jaeger
      - JAEGER_SERVICE_NAME=crowler-jaeger
      - JAEGER_SAMPLER_TYPE=const
      - JAEGER_SAMPLER_PARAM=1
      - TZ=\${VDI_TZ:-UTC}
      - MICROSERVICE_NAME=crowler-jaeger
$(emit_limits "    " "${cpu_limit_tlm:-1.0}" "${mem_limit_tlm_pct:-2g}")
    ${platform}
    ports:
      - "16686:16686" # Jaeger UI
      - "4317:4317"   # OpenTelemetry gRPC endpoint
    restart: unless-stopped
    networks:
      - crowler-net
EOF

    # Add Jaeger networks dynamically for all VDIs
    for i in $(seq 1 "$vdi_count"); do
        cat << EOF >> docker-compose.yml
      - crowler-vdi-$i
EOF
    done
fi

# Add crowler-vdi instances
if [ "$vdi_count" != "0" ]; then
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
$(emit_limits "    " "${cpu_limit_vdi:-1.0}" "${mem_limit_vdi_pct:-2g}")
    env_file:
      - .env
    environment:
      - COMPOSE_PROJECT_NAME=crowler
      - INSTANCE_ID=$i
      - SE_SCREEN_WIDTH=1920
      - SE_SCREEN_HEIGHT=1080
      - SE_SCREEN_DEPTH=24
      - SE_ROLE=standalone
      - SE_REJECT_UNSUPPORTED_CAPS=true
      - SE_NODE_ENABLE_CDP=true
      - SE_ENABLE_TRACING=\${SE_ENABLE_TRACING:-true}
      - SE_OTEL_TRACES_EXPORTER=otlp
      - SE_OTEL_EXPORTER_ENDPOINT=\${SE_OTEL_EXPORTER_ENDPOINT:-http://crowler-jaeger:4317}
      - SEL_PASSWD=\${SEL_PASSWD:-secret}
      - TZ=\${VDI_TZ:-UTC}
      - MICROSERVICE_NAME=crowler-vdi-$i
    shm_size: "2g"
    image: \${DOCKER_SELENIUM_IMAGE:-selenium/standalone-chromium:4.27.0-$(get_date)}
    ${pull_policy_never}
    ${platform}
    ports:
      - "$HOST_PORT_START1-$HOST_PORT_END1:4444-4445"
      - "$HOST_PORT_START2:5900"
      - "$HOST_PORT_START3:7900"
      - "$HOST_PORT_START4:9222"
    volumes:
      - /dev/shm:/dev/shm
    expose:
      - "$HOST_PORT_START4"
    networks:
      - $NETWORK_NAME
    restart: unless-stopped
EOF
done
fi

# Add Prometheus PushGateway
if [ "$prometheus" == "yes" ]; then
    cat << EOF >> docker-compose.yml

  crowler-push-gateway:
    image: prom/pushgateway
    container_name: "crowler-push-gateway"
$(emit_limits "    " "${cpu_limit_tlm:-1.0}" "${mem_limit_tlm_pct:-2g}")
    ports:
      - "9091:9091"
    env_file:
      - .env
    environment:
      - COMPOSE_PROJECT_NAME=crowler
      - MICROSERVICE_NAME=crowler-push-gateway
    ${platform}
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
    driver: ${net_driver}
EOF

# Add all dynamically created networks for VDIs
for i in $(seq 1 "$vdi_count"); do
    cat << EOF >> docker-compose.yml
  crowler-vdi-$i:
    driver: ${net_driver}
EOF
done

# Add Static Volumes (if needed)
if [ "$no_api" == "0" ] || [ "$no_events" == "0" ] || [ "$postgres" == "yes" ] || [ "$engine_count" != "0" ]; then
cat << EOF >> docker-compose.yml

volumes:
EOF
fi
if [ "$no_api" == "0" ]; then
    cat << EOF >> docker-compose.yml
  api_data:
EOF
fi
if [ "$no_events" == "0" ]; then
    cat << EOF >> docker-compose.yml
  events_data:
EOF
fi

if [ "$postgres" == "yes" ]; then
    cat << EOF >> docker-compose.yml
  db_data:
    driver: local
EOF
fi

if [ "$engine_count" != "0" ]; then
cat << EOF >> docker-compose.yml
  engine_data:
    driver: local
EOF
fi

echo "docker-compose.yml has been successfully generated."
