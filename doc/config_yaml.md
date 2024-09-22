# Understanding the config.yaml file

* [Introduction](#introduction)
* [The database section](#the-database-section)
* [The crawler section](#the-crawler-section)
* [The image_storage and file_storage sections](#the-image_storage-and-file_storage-sections)
* [Loading the configuration](#loading-the-configuration)
* [Reloading the configuration](#reloading-the-configuration)
* [Adding configuration validation in VSCode](#adding-configuration-validation-in-vscode)
* [Example of working config](#example-of-working-config)

## legend

|  Symbol means choice: a|b means a OR b

[] Means Optional: \[a\] means a is optional

## Introduction

The config.yaml file is used to configure the CROWler and the API. It should be
placed in the root directory of the repository.

The CROWler support a "dynamic" configuration, which means that you can use ENV
variables in the config.yaml file. This is useful when you want to use the same
config.yaml file in different environments or when you don't want to hardcode
sensitive data in the config.yaml file.

The CROWler configuration supports also an `include` directive, which allows you
to include another yaml file in the config.yaml file. This is useful when you
want to split the configuration in multiple files.
**Please note:** if you use multiple configuration files, ensure they all get copied
in the Docker image!

The config.yaml file is divided in sections, each section configures a specific
component of the CROWler.

**Please note:** You must create your config.yaml file before building the Docker
image! If you don't do this, the CROWler will not work.

For a detailed and up-to-date reference to the config.yam available options,
please have a look at the [config reference](./config_reference.md), or
you can have a look at the [config schema](../schemas/crowler-config-schema.json).

The config.yaml file should look like this:

```yaml
remote:                      # Optional, this is the remote configuration section, here you can configure the remote CROWler API to be used to download the actual configuration
  type: ""                   # Required, this is the type of the remote configuration API
  host: localhost            # Required, this is the IP of the remote configuration API
  port: 8080                 # Required, this is the port of the remote configuration API
  token: ${TOKEN}            # Optional, this is the token to use to authenticate to the remote configuration API
  secret: ${SECRET}          # Optional, this is the secret to use to authenticate to the remote configuration API
  region: ${REGION}          # Optional, this is the region of the remote configuration API
  timeout: 10                # Optional, this is the timeout for the remote configuration API
  sslmode: "enable|disable"  # Optional, this is the SSL mode for the remote configuration API

database:
  type: postgres
  host: ${POSTGRES_DB_HOST}
  port: 5432
  user: ${CROWLER_DB_USER}
  password: ${CROWLER_DB_PASSWORD}
  dbname: SitesIndex
  sslmode: disable

crawler:
  workers: 5                 # Required, this is the number of workers the crawler will use
  max_depth: 1               # Optional, this is the maximum depth the crawler will reach (0 for no limit)
  delay: "2"                 # Optional, this is the delay between two requests (this is important to avoid being banned by the target website, you can also use remote(x,y) to use a random delay between x and y seconds)
  timeout: 10                # Optional, this is the timeout for a request
  maintenance: 60            # Optional, this is the time between two maintenance operations (in seconds)
  interval: 10               # Optional, this is the time before start executing action rules on a just fetched page (this is useful for slow websites)
  source_screenshot: true    # Optional, this is the flag to enable or disable the source screenshot for the source URL
  full_site_screenshot: true # Optional, this is the flag to enable or disable the screenshots for the entire site (not just the source URL)
  max_sources: 4             # Optional, this is the maximum number of sources to be crawled per engine
  delay: random(random(1,2), random(3,5)) # Optional, this is the delay between two requests (this is important to avoid being banned by the target website, you can also use remote(x,y) to use a random delay between x and y seconds)
  browsing_mode: "headless|normal" # Optional, this is the browsing mode for the crawler (headless or normal)
  max_retries: 3             # Optional, this is the maximum number of retries for a request
  max_requests: 10           # Optional, this is the maximum number of requests for a source
  collect_html: true         # Optional, this is the flag to enable or disable the collection of the HTML content
  collect_images: true       # Optional, this is the flag to enable or disable the collection of the images
  collect_files: true        # Optional, this is the flag to enable or disable the collection of the files
  collect_content: true      # Optional, this is the flag to enable or disable the collection of the content
  collect_keywords: true     # Optional, this is the flag to enable or disable the collection of the keywords
  collect_metatags: true     # Optional, this is the flag to enable or disable the collection of the metatags
  control:                   # This section allow you to configure the control API
    host: localhost          # Optional, this is the IP of the control API
    port: 8080               # Optional, this is the port of the control API
    sslmode: disable         # Optional, this is the SSL mode for the control API
    cert_file: ""            # Optional, this is the SSL certificate for the control API
    key_file: ""             # Optional, this is the SSL key for the control API
    timeout: 10              # Optional, this is the timeout for the control API
    rate_limit: 10           # Optional, this is the rate limit for the control API
    readheader_timeout: 10   # Optional, this is the read header timeout for the control API
    write_timeout: 10        # Optional, this is the write timeout for the control API

image_storage:
  type: local                # Required, this is the type of the image storage API
  path: /images              # Required, this is the path of the image storage API
  host: localhost            # Optional, this is the IP of the image storage API
  port: 8080                 # Optional, this is the port of the image storage API
  token: ${TOKEN}            # Optional, this is the token to use to authenticate to the image storage API
  secret: ${SECRET}          # Optional, this is the secret to use to authenticate to the image storage API
  region: ${REGION}          # Optional, this is the region of the image storage API
  timeout: 10                # Optional, this is the timeout for the image storage API

file_storage:
  # This is identical to image_storage, however it applies to files and web objects (not to images!)

api:                         # This is the API configuration (this is the general API)
  port: 8080                 # Required, this is the port of the API
  host: 0.0.0.0              # Required, this is the IP of the API
  timeout: 10                # Optional, this is the timeout for the API
  rate_limit: 10             # Optional, this is the rate limit for the API
  readheader_timeout: 10     # Optional, this is the read header timeout for the API
  write_timeout: 10          # Optional, this is the write timeout for the API
  read_timeout: 10           # Optional, this is the read timeout for the API
  ssl_mode: "enable|disable" # Optional, this is the SSL mode for the API
  cert_file: ""              # Optional, this is the SSL certificate for the API
  key_file: ""               # Optional, this is the SSL key for the API
  enable_console: true       # Optional, this (if set to true) will enable the extra end points for adding and removing sources etc.

selenium:                    # This is the Selenium container configuration (please note that this tag will soon be replaced by "vdi" and that is because a VDI image is not just selenium but also other tools which will soon need to be configured)
  - name: thecrowler_vdi_1   # Optional, This field allow you to specify a name for the VDI container
    location: AWS_EU_WEST_1  # Optional, This field allow you to specify the location of the VDI container
    type: chrome             # Required, this is the type of the Selenium container
    port: 4444               # Required, this is the port of the Selenium container
    headless: true           # Optional, if true the Selenium container will run in headless mode (not recommended)
    host: ${SELENIUM_HOST}   # required, this is the IP of the Selenium container
    proxy_url: ""            # Optional and if populated will configure the proxy for the selenium container
    download_path: /app/data # Optional, this is the download path for the VDI container, this path is used to store temporarily the downloaded files

  - type: chrome             # This configure ANOTHER instance of the Selenium container (useful for parallel crawling)
    port: 4445               # Required, this is the port of the Selenium container
    headless: true           # Optional, if true the Selenium container will run in headless mode (not recommended)
    host: localhost          # required, this is the IP of the Selenium container
    proxy_url: ""            # Optional and if populated will configure the proxy for the selenium container
                             # YES you can use different proxies, the CROWler is not a toy ;)
                             # Note: if you use or need a single Selenium container, you can remove this second section!

network_info:
  dns:
    enabled: true            # Enables DNS information gathering (recursive and authoritative)
    delay: 1                 # Delay between two requests
    timeout: 60              # Timeout for a request
  whois:
    enabled: true            # Enables WHOIS information gathering
    timeout: 60              # Timeout for a request
  httpinfo:
    enabled: true            # Enables HTTP information gathering
    timeout: 60              # Timeout for a request
    ssl_discovery: true      # Enables SSL information gathering
  service_scout:
    enabled: true            # Enables service discovery (this is a network scanner, use with caution!)
    timeout: 60              # Timeout for a request
    idle_scan: true          # Enables idle scan (this is a network scanner, use with caution!)
      host: ""               # The host to use for the idle scan
      port: "80"             # The port to use for the idle scan
    ping_scan: true          # Enables ping scan (this is a network scanner, use with caution!)
    connect_scan: true       # Enables connect scan (this is a network scanner, use with caution!)
    syn_scan: true           # Enables syn scan (this is a network scanner, use with caution!)
    udp_scan: true           # Enables udp scan (this is a network scanner, use with caution!)
    no_dns_resolution: true  # Disables DNS resolution (this is a network scanner, use with caution!)
    service_detection: true  # Enables service detection (this is a network scanner, use with caution!)
    service_db: ""           # The service database to use for the service detection
    os_finger_print: true    # Enables OS finger print (this is a network scanner, use with caution!)
    aggressive_scan: true    # Enables aggressive scan (this is a network scanner, use with caution!)
    script_scan: true        # Enables script scan (this is a network scanner, use with caution!)
      - default
      - vuln
    excluded_hosts:          # A list of hosts to NEVER scan (the list of host to scan is automatically generated by the DNS, Whois and NetInfo modules, this list here allows to specify IPs that should never be scanned)
      - 192.168.101.1
    timing_template: "T3"    # The timing template to use for the scan
    host_timeout: "60"       # The host timeout for the scan, this is expressed as a string so you can use the expterpreter to specify the time
    min_rate: "10"           # The minimum rate for the scan, this is expressed as a string so you can use the expterpreter to specify the rate
    max_retries: "100"       # The maximum number of retries for the scan, this is expressed as a string so you can use the expterpreter to specify the number
    source_port: "80"        # The source port for the scan, this is expressed as a string so you can use the expterpreter to specify the port
    interface: "eth0"        # The interface to use for the scan
    spoof_ip: "10.10.10.10"  # The IP to spoof for the scan
    randomize_hosts: true    # Randomize the hosts to scan
    data_length: 10          # The data length for the scan, this is expressed as a string so you can use the expterpreter to specify the length
    delay: "1"               # The delay for the scan, this is expressed as a string so you can use the expterpreter to specify the delay
    mtu_discovery: true      # Enable the MTU discovery
    scan_flags: "SYN"        # The scan flags to use for the scan
    ip_fragment: true        # Enable the IP fragment
    max_port_number: 65535   # The maximum port number to scan
    max_parallelism: 10      # The maximum parallelism for the scan
    dns_servers:             # The DNS servers to use for the scan
      - 1.1.1.1
    proxies:                 # The proxies to use for the scan
      - "http://proxy1:8080"
      - "http://proxy2:8080"

  geo_localization:
    enabled: false           # Enables geo localization information gathering
    type: "local|remote"     # The type of the geo localization service (for local it uses maxmind, for remote it uses an IP2Location API)
    path: ""                 # The path to the geo localization database (for maxmind)
    timeout: 60              # Timeout for a request
    host: ""                 # The geo localization service host (if they are remote)
    port: "80"               # The geo localization service port (if they are remote)
    region: ""               # The region of the geo localization service (if they are remote)
    token: ""                # The token to use to authenticate to the geo localization service (if they are remote)
    secret: ""               # The secret to use to authenticate to the geo localization service (if they are remote)

rulesets:
  - type: "local|remote"     # The type of the ruleset distribution (local or remote)
    path: ""                 # The path to the ruleset file
    timeout: 60              # Timeout for a request
    host: ""                 # The ruleset distribution host (if they are remote)
    port: "80"               # The ruleset distribution port (if they are remote)
    region: ""               # The region of the ruleset distribution (if they are remote)
    token: ""                # The token to use to authenticate to the ruleset distribution (if they are remote)
    secret: ""               # The secret to use to authenticate to the ruleset distribution (if they are remote)
  - type: "local|remote"     # YES you can use multiple rulesets, and YES mix of local and remote too!
    path: ""                 # The path to the ruleset file
    timeout: 60              # Timeout for a request
    host: ""                 # The ruleset distribution host (if they are remote)
    port: "80"               # The ruleset distribution port (if they are remote)
    region: ""               # The region of the ruleset distribution (if they are remote)
    token: ""                # The token to use to authenticate to the ruleset distribution (if they are remote)
    secret: ""               # The secret to use to authenticate to the ruleset distribution (if they are remote)

debug_level: 0               # Optional, this is the debug level (0 for no debug, 1 or more for debug, the higher the number the more verbose the output will be)
```

The sections are:

* The remote section configures the remote configuration API
  (if you use the remote section then please do NOT use the other sections!)
  (if you use the other sections, then please do NOT use the remote section!)
* The database section configures the database
* The crawler section configures the crawler
* The image_storage_api section configures the image storage API
* The file_storage_api section configures the file storage API (downloaded files and/or web objects)
* The API section configures the API
* The selenium section configures the Selenium Chrome container
* The network_info section configures the network information gathering
* The rulesets section configures the rulesets that will be loaded on the specific CROWler engine
* The debug_level section configures the debug level

## The database section

The database section configures the database. It should look like this:

```yaml
database:
  host: localhost
  port: 5432
  user: ${POSTGRES_USER}
  password: ${POSTGRES_PASSWORD}
  dbname: SitesIndex
```

* host is the database host name or IP
* port is the database port
* user is the database user
* password is the database password
* dbname is the database name (by default SitesIndex).
* You can use ENV variables in the config.yaml file as in the example above, you can name the variables as you wish. If you want to use ENV variables remember to put them between `${}` like this `${POSTGRES_USER}`.

## The crawler section

The crawler section configures the crawler. It should look like this:

```yaml
crawler:
  workers: 5
  depth: 1
  delay: 2
  interval: 10
  timeout: 10
  maintenance: 60
```

* workers is the number of workers the crawler engine will use
* depth is the maximum depth the crawler will reach
* delay is the delay between two requests
* interval is the time before start executing action rules on a just fetched page (this is useful for slow websites)
* timeout is the timeout for a request and maintenance is the time
between two maintenance operations.
* maintenance is the time between two maintenance operations (in minutes). Keep in mind that DB maintenance is done only if there aren't active crawling operations. So, if you set maintenance to 1 hour and you have a crawling operation that lasts 2 hours, the DB maintenance will be done after the crawling operation is finished.

## The image_storage and file_storage sections

The image_storage_api section configures the image storage API. It should look
like this:

```yaml
image_storage_api:
  type: local
  path: /images
  host: localhost
  port: 8080
  token: ${TOKEN}
  secret: ${SECRET}
  region: ${REGION}
  timeout: 10
```

**type:**

is the type of the image storage API.

* local means that we'll use the local disc as images storage. This is the
default value.
* s3 means that we'll use AWS S3 as images storage.
* api means that we'll use an API as images storage.

**path:**

* If we selected local storage then it's the path where the images will
be stored.
* If we selected S3 storage then it's the bucket name.
* If we selected the API storage then it's the API URL.

**token:**

* If we selected the API storage then it's the token to use to authenticate
to the API.
* If we selected the S3 storage then it's the AWS access key.
* If we selected local storage then it's ignored.

**secret:**

* If we selected the API storage then it's the secret to use to
authenticate to the API.
* If we selected the S3 storage then it's the AWS secret key.
* If we selected local storage then it's ignored.

**region:**

* If we selected the API storage then it's ignored.
* If we selected the S3 storage then it's the AWS region.
* If we selected local storage then it's ignored.

**timeout:**

is the timeout for the image storage API. It's expressed in seconds.

## Loading the configuration

The CROWler will load the configuration from the config.yaml file in the
current path if no cli arguments are provided.

If you wish to place your config.yaml file in a different location, you can
then tell the crowler and the API where to find it by using the `--config`
argument.

## Reloading the configuration

Regardless of where you've stored your configuration, locally or remotely, you
can reload the configuration by sending a `SIGHUP` signal to the CROWler.

When a `SIGHUP` signal is received, the CROWler will reload the configuration AFTER
the current crawling operations are completed.

## Adding configuration validation in VSCode

To add the CROWler configuration validation in VSCode, you can use the
following extension:

* YAML Language Support by Red Hat

Install the extension above on your Visual Studio Code.

Then open (or create) your VS Code settings.json file (in your .vscode
directory) and add the following:

```json
"yaml.schemas": {
    "./schemas/crowler-config-schema.json": ["config.y*ml", "*-config.y*ml"],
}
```

This will ensure that every file called config.yaml (or .yml), or *-config.yaml
(or .yml) will be validated against the schema file.

This will help you a lot when editing your config.yaml file.

## Example of working config

**Please Note**: The following config.yaml uses few ENV variables, so pay attention to them and set them with your own values before running your docker-rebuild.sh

```yaml
database:
  type: postgres
  host: ${POSTGRES_DB_HOST}
  port: 5432
  user: ${CROWLER_DB_USER}
  password: ${CROWLER_DB_PASSWORD}
  dbname: ${POSTGRES_DB_NAME}
  sslmode: ${POSTGRES_SSL_MODE}

crawler:
  workers: 5
  depth: 1
  delay: random(random(1,2), random(3,5))
  interval: random(1,2)
  timeout: 10
  maintenance: 60
  source_screenshot: true

image_storage:
  type: local
  path: /app/data

api:
  port: 8080
  host: 0.0.0.0
  timeout: 10
  enable_console: true
  return_404: false

selenium:
  - type: chrome
    path: ""
    port: 4444
    headless: false
    host: ${SELENIUM_HOST}
    sslmode: disable
    use_service: false

network_info:
  dns:
    enabled: true
    timeout: 10
  whois:
    enabled: true
    timeout: 10
  netlookup:
    enabled: true
    timeout: 10
  httpinfo:
    enabled: true
    timeout: 10
    ssl_discovery: true
  service_scout:
    enabled: true
    timeout: 600

debug_level: 0
```

The above configuration has been tested with the docker images we provide with this repo.
