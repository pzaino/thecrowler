# Generate-DockerCompose.ps1
# This PowerShell script replicates the behavior of your Bash script.
# It processes command‐line arguments, prompts for missing values,
# and generates a docker-compose.yml file.

#region Functions

function Show-Usage {
    Write-Host "Usage: $($MyInvocation.MyCommand.Name) [OPTIONS]"
    Write-Host "Options:"
    Write-Host "  --engine_count=<number>  Number of crowler-engine instances"
    Write-Host "  -e=<number>              Number of crowler-engine instances"
    Write-Host "  --vdi_count=<number>     Number of crowler-vdi instances"
    Write-Host "  -v=<number>              Number of crowler-vdi instances"
    Write-Host "  --prometheus=<yes/no>    Include Prometheus PushGateway"
    Write-Host "  --prom=<yes/no>          Include Prometheus PushGateway"
    Write-Host "  --postgres=<yes/no>      Include PostgreSQL database"
    Write-Host "  --pg=<yes/no>            Include PostgreSQL database"
}

function Read-IntegerInput($prompt) {
    while ($true) {
        $value = Read-Host $prompt
        if ($value -match '^[0-9]+$' -and [int]$value -ge 0) {
            return $value
        }
        else {
            Write-Host "Invalid input. Please provide a positive integer."
        }
    }
}

function Read-YesNoInput($prompt) {
    while ($true) {
        $value = Read-Host "$prompt (yes/no)"
        $value = $value.Trim().ToLower()
        if ($value -eq "yes" -or $value -eq "no") {
            return $value
        }
        else {
            Write-Host "Invalid input. Please provide 'yes' or 'no'."
        }
    }
}

function Get-DateStr {
    return (Get-Date -Format "yyyyMMdd")
}

#endregion Functions

#region Process Arguments

# Initialize variables
[string]$engine_count = ""
[string]$vdi_count    = ""
[string]$prometheus   = ""
[string]$postgres     = ""

# Process command-line arguments (available in $args)
foreach ($arg in $args) {
    switch -Regex ($arg) {
        '^--help$' { Show-Usage; exit 0 }
        '^-h$'    { Show-Usage; exit 0 }
        '^--engine_count=(.*)$' { $engine_count = $Matches[1] }
        '^--engine=(.*)$'       { $engine_count = $Matches[1] }
        '^-e=(.*)$'            { $engine_count = $Matches[1] }
        '^--vdi_count=(.*)$'    { $vdi_count = $Matches[1] }
        '^--vdi=(.*)$'          { $vdi_count = $Matches[1] }
        '^-v=(.*)$'            { $vdi_count = $Matches[1] }
        '^--prometheus=(.*)$'   { $prometheus = $Matches[1] }
        '^--prom=(.*)$'         { $prometheus = $Matches[1] }
        '^--postgres=(.*)$'     { $postgres = $Matches[1] }
        '^--pg=(.*)$'           { $postgres = $Matches[1] }
    }
}

# Prompt for missing parameters
if (-not $engine_count) {
    $engine_count = Read-IntegerInput "Enter the number of crowler-engine instances"
}
if (-not $vdi_count) {
    $vdi_count = Read-IntegerInput "Enter the number of crowler-vdi instances"
}
if (-not $prometheus) {
    $prometheus = Read-YesNoInput "Do you want to include the Prometheus PushGateway?"
}
if (-not $postgres) {
    $postgres = Read-YesNoInput "Do you want to include the PostgreSQL database?"
}

#endregion Process Arguments

#region Build docker-compose.yml Content

# We will build the output as an array of strings.
$composeLines = @()

# To avoid PowerShell variable expansion in the file (for Docker variables)
# we escape each literal '$' with a backtick:  `$

$composeLines += '---'
$composeLines += 'services:'
$composeLines += ''

# crowler-api service
$composeLines += '  crowler-api:'
$composeLines += '    container_name: "crowler-api"'
$composeLines += '    env_file:'
$composeLines += '      - .env'
$composeLines += '    environment:'
$composeLines += '      - COMPOSE_PROJECT_NAME=crowler'
$composeLines += '      - INSTANCE_ID=`${INSTANCE_ID:-1}'
$composeLines += '      - POSTGRES_DB=`${DOCKER_POSTGRES_DB_NAME:-SitesIndex}'
$composeLines += '      - CROWLER_DB_USER=`${DOCKER_CROWLER_DB_USER:-crowler}'
$composeLines += '      - CROWLER_DB_PASSWORD=`${DOCKER_CROWLER_DB_PASSWORD}'
$composeLines += '      - POSTGRES_DB_HOST=`${DOCKER_DB_HOST:-crowler-db}'
$composeLines += '      - POSTGRES_DB_PORT=`${DOCKER_DB_PORT:-5432}'
$composeLines += '      - POSTGRES_SSL_MODE=`${DOCKER_POSTGRES_SSL_MODE:-disable}'
$composeLines += '      - TZ=`${VDI_TZ:-UTC}'
$composeLines += '    build:'
$composeLines += '      context: .'
$composeLines += '      dockerfile: Dockerfile.searchapi'
$composeLines += '    platform: `${DOCKER_DEFAULT_PLATFORM:-linux/amd64}'
$composeLines += '    image: crowler-api'
$composeLines += '    pull_policy: never'
$composeLines += '    stdin_open: true # For interactive terminal access (optional)'
$composeLines += '    tty: true        # For interactive terminal access (optional)'
$composeLines += '    ports:'
$composeLines += '      - "8080:8080"'
$composeLines += '    networks:'
$composeLines += '      - crowler-net'
$composeLines += '    volumes:'
$composeLines += '      - api_data:/app/data'
$composeLines += '    user: apiuser'
$composeLines += '    read_only: true'
$composeLines += '    healthcheck:'
$composeLines += '      test: ["CMD-SHELL", "healthCheck"]'
$composeLines += '      interval: 10s'
$composeLines += '      timeout: 5s'
$composeLines += '      retries: 5'
$composeLines += '    restart: unless-stopped'
$composeLines += ''

# crowler-events service
$composeLines += '  crowler-events:'
$composeLines += '    container_name: "crowler-events"'
$composeLines += '    env_file:'
$composeLines += '      - .env'
$composeLines += '    environment:'
$composeLines += '      - COMPOSE_PROJECT_NAME=crowler'
$composeLines += '      - INSTANCE_ID=`${INSTANCE_ID:-1}'
$composeLines += '      - POSTGRES_DB=`${DOCKER_POSTGRES_DB_NAME:-SitesIndex}'
$composeLines += '      - CROWLER_DB_USER=`${DOCKER_CROWLER_DB_USER:-crowler}'
$composeLines += '      - CROWLER_DB_PASSWORD=`${DOCKER_CROWLER_DB_PASSWORD}'
$composeLines += '      - POSTGRES_DB_HOST=`${DOCKER_DB_HOST:-crowler-db}'
$composeLines += '      - POSTGRES_DB_PORT=`${DOCKER_DB_PORT:-5432}'
$composeLines += '      - POSTGRES_SSL_MODE=`${DOCKER_POSTGRES_SSL_MODE:-disable}'
$composeLines += '      - TZ=`${VDI_TZ:-UTC}'
$composeLines += '    build:'
$composeLines += '      context: .'
$composeLines += '      dockerfile: Dockerfile.events'
$composeLines += '    platform: `${DOCKER_DEFAULT_PLATFORM:-linux/amd64}'
$composeLines += '    image: crowler-events'
$composeLines += '    pull_policy: never'
$composeLines += '    stdin_open: true # For interactive terminal access (optional)'
$composeLines += '    tty: true        # For interactive terminal access (optional)'
$composeLines += '    ports:'
$composeLines += '      - "8082:8082"'
$composeLines += '    networks:'
$composeLines += '      - crowler-net'
$composeLines += '    volumes:'
$composeLines += '      - events_data:/app/data'
$composeLines += '    user: eventsuser'
$composeLines += '    read_only: true'
$composeLines += '    healthcheck:'
$composeLines += '      test: ["CMD-SHELL", "healthCheck"]'
$composeLines += '      interval: 10s'
$composeLines += '      timeout: 5s'
$composeLines += '      retries: 5'
$composeLines += '    restart: unless-stopped'

# Add crowler-db service if PostgreSQL is enabled
if ($postgres -eq "yes") {
    $composeLines += ''
    $composeLines += '  crowler-db:'
    $composeLines += '    image: postgres:15.10-bookworm'
    $composeLines += '    container_name: "crowler-db"'
    $composeLines += '    ports:'
    $composeLines += '      - "5432:5432"'
    $composeLines += '    env_file:'
    $composeLines += '      - .env'
    $composeLines += '    environment:'
    $composeLines += '      - COMPOSE_PROJECT_NAME=crowler'
    $composeLines += '      - POSTGRES_DB=`${DOCKER_POSTGRES_DB_NAME:-SitesIndex}'
    $composeLines += '      - POSTGRES_USER=`${DOCKER_POSTGRES_USER:-postgres}'
    $composeLines += '      - POSTGRES_PASSWORD=`${DOCKER_POSTGRES_PASSWORD}'
    $composeLines += '      - CROWLER_DB_USER=`${DOCKER_CROWLER_DB_USER:-crowler}'
    $composeLines += '      - CROWLER_DB_PASSWORD=`${DOCKER_CROWLER_DB_PASSWORD}'
    $composeLines += '      - PROXY_SERVICE=`${VDI_PROXY_SERVICE:-}'
    $composeLines += '      - TZ=`${VDI_TZ:-UTC}'
    $composeLines += '    platform: `${DOCKER_DEFAULT_PLATFORM:-linux/amd64}'
    $composeLines += '    volumes:'
    $composeLines += '      - db_data:/var/lib/postgresql/data'
    $composeLines += '      - ./pkg/database/postgresql-setup.sh:/docker-entrypoint-initdb.d/init.sh'
    $composeLines += '      - ./pkg/database/postgresql-setup-v1.5.pgsql:/docker-entrypoint-initdb.d/postgresql-setup-v1.5.pgsql'
    $composeLines += '    networks:'
    $composeLines += '      - crowler-net'
    $composeLines += '    healthcheck:'
    $composeLines += '      test: ["CMD-SHELL", "pg_isready -U `$${POSTGRES_USER}"]'
    $composeLines += '      interval: 10s'
    $composeLines += '      timeout: 5s'
    $composeLines += '      retries: 5'
    $composeLines += '    logging:'
    $composeLines += '      driver: "json-file"'
    $composeLines += '      options:'
    $composeLines += '        max-size: "10m"'
    $composeLines += '        max-file: "3"'
    $composeLines += '    restart: unless-stopped'
}

# Add crowler-engine instances if engine_count is not zero
if ([int]$engine_count -ne 0) {
    for ($i = 1; $i -le [int]$engine_count; $i++) {
        # Build the networks list for this engine based on the number of VDIs
        $engineNetworksLines = @()
        for ($j = 1; $j -le [int]$vdi_count; $j++) {
            $engineNetworksLines += "      - crowler-vdi-$j"
        }
        $ENGINE_NETWORKS = $engineNetworksLines -join "`n"

        $composeLines += ''
        $composeLines += "  crowler-engine-$i`:"
        # Use backticks to output literal double quotes where needed.
        $composeLines += "    container_name: `"crowler-engine-$i`""
        $composeLines += "    env_file:"
        $composeLines += "      - .env"
        $composeLines += "    environment:"
        $composeLines += "      - COMPOSE_PROJECT_NAME=crowler"
        $composeLines += "      - INSTANCE_ID=$i"
        $composeLines += "      - SELENIUM_HOST=`${DOCKER_SELENIUM_HOST:-crowler-vdi-$i}"
        $composeLines += "      - POSTGRES_DB=`${DOCKER_POSTGRES_DB_NAME:-SitesIndex}"
        $composeLines += "      - CROWLER_DB_USER=`${DOCKER_CROWLER_DB_USER:-crowler}"
        $composeLines += "      - CROWLER_DB_PASSWORD=`${DOCKER_CROWLER_DB_PASSWORD}"
        $composeLines += "      - POSTGRES_DB_HOST=`${DOCKER_DB_HOST:-crowler-db}"
        $composeLines += "      - POSTGRES_DB_PORT=`${DOCKER_DB_PORT:-5432}"
        $composeLines += "      - POSTGRES_SSL_MODE=`${DOCKER_POSTGRES_SSL_MODE:-disable}"
        $composeLines += "      - TZ=`${VDI_TZ:-UTC}"
        $composeLines += "    build:"
        $composeLines += "      context: ."
        $composeLines += "      dockerfile: Dockerfile.thecrowler"
        $composeLines += "    platform: `(${DOCKER_DEFAULT_PLATFORM:-linux/amd64}"
        $composeLines += "    image: crowler-engine-$i"
        $composeLines += "    pull_policy: never"
        $composeLines += "    networks:"
        $composeLines += "      - crowler-net"
        $composeLines += $ENGINE_NETWORKS
        $composeLines += "    cap_add:"
        $composeLines += "      - NET_ADMIN"
        $composeLines += "      - NET_RAW"
        $composeLines += "    stdin_open: true # For interactive terminal access (optional)"
        $composeLines += "    tty: true        # For interactive terminal access (optional)"
        $composeLines += "    volumes:"
        $composeLines += "      - engine_data:/app/data"
        $composeLines += "    user: crowler"
        $composeLines += "    healthcheck:"
        $composeLines += '      test: ["CMD-SHELL", "healthCheck"]'
        $composeLines += "      interval: 10s"
        $composeLines += "      timeout: 5s"
        $composeLines += "      retries: 5"
        $composeLines += "    restart: unless-stopped"
    }
}

# Add Jaeger service if at least one VDI instance exists
if ([int]$vdi_count -ne 0) {
    $composeLines += ''
    $composeLines += '  jaeger:'
    $composeLines += '    image: jaegertracing/all-in-one:1.54'
    $composeLines += '    container_name: "crowler-jaeger"'
    $composeLines += '    platform: `(${DOCKER_DEFAULT_PLATFORM:-linux/amd64}'
    $composeLines += '    ports:'
    $composeLines += '      - "16686:16686" # Jaeger UI'
    $composeLines += '      - "4317:4317"   # OpenTelemetry gRPC endpoint'
    $composeLines += '    networks:'
    $composeLines += '      - crowler-net'
    # Dynamically add all VDI networks
    for ($i = 1; $i -le [int]$vdi_count; $i++) {
        $composeLines += "      - crowler-vdi-$i"
    }
}

# Add crowler-vdi instances if vdi_count is not zero
if ([int]$vdi_count -ne 0) {
    # Pre-calculate the date string for the selenium image tag.
    $seleniumImageDate = Get-DateStr
    for ($i = 1; $i -le [int]$vdi_count; $i++) {
        # Calculate host port values
        $HOST_PORT_START1 = 4444 + (($i - 1) * 2)
        $HOST_PORT_END1   = 4445 + (($i - 1) * 2)
        $HOST_PORT_START2 = 5900 + ($i - 1)
        $HOST_PORT_START3 = 7900 + ($i - 1)
        $HOST_PORT_START4 = 9222 + ($i - 1)
        $NETWORK_NAME     = "crowler-vdi-$i"

        $composeLines += ''
        $composeLines += "  crowler-vdi-$i`:"
        $composeLines += "    container_name`: `"crowler-vdi-$i`""
        $composeLines += "    env_file`:"
        $composeLines += "      - .env"
        $composeLines += "    environment`:"
        $composeLines += "      - COMPOSE_PROJECT_NAME=crowler"
        $composeLines += "      - INSTANCE_ID=$i"
        $composeLines += "      - SE_SCREEN_WIDTH=1920"
        $composeLines += "      - SE_SCREEN_HEIGHT=1080"
        $composeLines += "      - SE_SCREEN_DEPTH=24"
        $composeLines += "      - SE_ROLE=standalone"
        $composeLines += "      - SE_REJECT_UNSUPPORTED_CAPS=true"
        $composeLines += "      - SE_NODE_ENABLE_CDP=true"
        $composeLines += "      - SE_ENABLE_TRACING=`${SE_ENABLE_TRACING:-true}"
        $composeLines += "      - SE_OTEL_TRACES_EXPORTER=otlp"
        $composeLines += "      - SE_OTEL_EXPORTER_ENDPOINT=`${SE_OTEL_EXPORTER_ENDPOINT:-http://crowler-jaeger:4317}"
        $composeLines += "      - SEL_PASSWD=`${SEL_PASSWD:-secret}"
        $composeLines += "      - TZ=`${VDI_TZ:-UTC}"
        $composeLines += '    shm_size: "2g"'
        # Note: the selenium image line concatenates a fixed default and the computed date.
        $composeLines += "    image: `${DOCKER_SELENIUM_IMAGE:-selenium/standalone-chromium:4.27.0-$seleniumImageDate}"
        $composeLines += "    pull_policy: never"
        $composeLines += "    platform: `${DOCKER_DEFAULT_PLATFORM:-linux/amd64}"
        $composeLines += "    ports:"
        $composeLines += "      - `"`$HOST_PORT_START1-`$HOST_PORT_END1:4444-4445`""
        $composeLines += "      - `"`$HOST_PORT_START2:5900`""
        $composeLines += "      - `"`$HOST_PORT_START3:7900`""
        $composeLines += "      - `"`$HOST_PORT_START4:9222`""
        $composeLines += "    volumes`:"
        $composeLines += "      - /dev/shm:/dev/shm"
        $composeLines += "    expose`:"
        $composeLines += "      - `"$HOST_PORT_START4`""
        $composeLines += "    networks`:"
        $composeLines += "      - $NETWORK_NAME"
        $composeLines += "    restart`: unless-stopped"
    }
}

# Add Prometheus PushGateway if enabled
if ($prometheus -eq "yes") {
    $composeLines += ''
    $composeLines += '  crowler-push-gateway:'
    $composeLines += '    image: prom/pushgateway'
    $composeLines += '    container_name: "crowler-push-gateway"'
    $composeLines += '    ports:'
    $composeLines += '      - "9091:9091"'
    $composeLines += '    env_file:'
    $composeLines += '      - .env'
    $composeLines += '    environment:'
    $composeLines += '      - COMPOSE_PROJECT_NAME=crowler'
    $composeLines += "    platform`: `${DOCKER_DEFAULT_PLATFORM`:-linux/amd64}"
    $composeLines += '    networks:'
    $composeLines += '      - crowler-net'
    $composeLines += '    restart: unless-stopped'
    $composeLines += '    logging:'
    $composeLines += '      driver: "json-file"'
    $composeLines += '      options:'
    $composeLines += '        max-size: "10m"'
    $composeLines += '        max-file: "3"'
}

# Add networks
$composeLines += ''
$composeLines += 'networks:'
$composeLines += '  crowler-net:'
$composeLines += '    driver: bridge'

# Add all dynamically created VDI networks
for ($i = 1; $i -le [int]$vdi_count; $i++) {
    $composeLines += "  crowler-vdi-$i`:"
    $composeLines += "    driver: bridge"
}

# Add static volumes and dynamic volumes based on options
$composeLines += ''
$composeLines += 'volumes:'
$composeLines += '  api_data:'
$composeLines += '  events_data:'
if ($postgres -eq "yes") {
    $composeLines += '  db_data:'
    $composeLines += '    driver: local'
}
if ([int]$engine_count -ne 0) {
    $composeLines += '  engine_data:'
    $composeLines += '    driver: local'
}

#endregion Build Content

#region Write File

# Write all lines to docker-compose.yml with UTF8 encoding.
$composeLines | Out-File -FilePath "docker-compose.yml" -Encoding utf8

Write-Host "docker-compose.yml has been successfully generated."

#endregion Write File
