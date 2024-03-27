# Understanding the config.yaml file

## legend

|  Symbol means choice: a|b means a OR b
[] Means Optional

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

The config.yaml file should look like this:

```yaml
remote:                     # Optional, this is the remote configuration section, here you can configure the remote CROWler API to be used to download the actual configuration
  type: ""                  # Required, this is the type of the remote configuration API
  host: localhost           # Required, this is the IP of the remote configuration API
  port: 8080                # Required, this is the port of the remote configuration API
  token: ${TOKEN}           # Optional, this is the token to use to authenticate to the remote configuration API
  secret: ${SECRET}         # Optional, this is the secret to use to authenticate to the remote configuration API
  region: ${REGION}         # Optional, this is the region of the remote configuration API
  timeout: 10               # Optional, this is the timeout for the remote configuration API
  sslmode: "enable|disable" # Optional, this is the SSL mode for the remote configuration API

database:
  host: localhost
  port: 5432
  user: ${POSTGRES_USER}
  password: ${POSTGRES_PASSWORD}
  dbname: SitesIndex

crawler:
  workers: 5        # Required, this is the number of workers the crawler will use
  depth: 1          # Required, this is the maximum depth the crawler will reach (0 for no limit)
  delay: "2"        # Required, this is the delay between two requests (this is important to avoid being banned by the target website, you can also use remote(x,y) to use a random delay between x and y seconds)
  timeout: 10       # Required, this is the timeout for a request
  maintenance: 60   # Required, this is the time between two maintenance operations (in seconds)

image_storage:
  type: local       # Required, this is the type of the image storage API
  path: /images     # Required, this is the path of the image storage API
  host: localhost   # Optional, this is the IP of the image storage API
  port: 8080        # Optional, this is the port of the image storage API
  token: ${TOKEN}   # Optional, this is the token to use to authenticate to the image storage API
  secret: ${SECRET} # Optional, this is the secret to use to authenticate to the image storage API
  region: ${REGION} # Optional, this is the region of the image storage API
  timeout: 10       # Optional, this is the timeout for the image storage API

file_storage:
  # This is identical to image_storage, however it applies to files and web objects (not to images!)

api:
  port: 8080        # Required, this is the port of the API
  host: 0.0.0.0     # Required, this is the IP of the API
  timeout: 10       # Optional, this is the timeout for the API
  rate_limit: 10    # Optional, this is the rate limit for the API
  readheader_timeout: 10 # Optional, this is the read header timeout for the API
  write_timeout: 10 # Optional, this is the write timeout for the API
  read_timeout: 10  # Optional, this is the read timeout for the API
  ssl_mode: "enable|disable" # Optional, this is the SSL mode for the API
  cert_file: ""      # Optional, this is the SSL certificate for the API
  key_file: ""       # Optional, this is the SSL key for the API
  enable_console: true  # Optional, this (if set to true) will enable the extra end points for adding and removing sources etc.

selenium:
  - type: chrome    # Required, this is the type of the Selenium container
    port: 4444      # Required, this is the port of the Selenium container
    headless: true  # Optional, if true the Selenium container will run in headless mode (not recommended)
    host: localhost # required, this is the IP of the Selenium container
    proxy_url: ""   # Optional and if populated will configure the proxy for the selenium container
  - type: chrome    # This configure another instance of the Selenium container (useful for parallel crawling)
    port: 4445      # Required, this is the port of the Selenium container
    headless: true  # Optional, if true the Selenium container will run in headless mode (not recommended)
    host: localhost # required, this is the IP of the Selenium container
    proxy_url: ""   # Optional and if populated will configure the proxy for the selenium container
                    # YES you can use different proxies, the CROWler is not a toy ;)
network_info:
  dns:
    enabled: true   # Enables DNS information gathering (recursive and authoritative)
    delay: 1        # Delay between two requests
    timeout: 60     # Timeout for a request
  whois:
    enabled: true   # Enables WHOIS information gathering
    timeout: 60     # Timeout for a request
  httpinfo:
    enabled: true   # Enables HTTP information gathering
    timeout: 60     # Timeout for a request
    ssl_discovery: true # Enables SSL information gathering
  service_scout:
    enabled: true   # Enables service discovery (this is a network scanner, use with caution!)
    timeout: 60     # Timeout for a request
  geo_localization:
    enabled: false  # Enables geo localization information gathering
    type: "local|remote" # The type of the geo localization service (for local it uses maxmind, for remote it uses an IP2Location API)
    path: ""        # The path to the geo localization database (for maxmind)
    timeout: 60     # Timeout for a request
    host: ""        # The geo localization service host (if they are remote)
    port: "80"      # The geo localization service port (if they are remote)
    region: ""      # The region of the geo localization service (if they are remote)
    token: ""       # The token to use to authenticate to the geo localization service (if they are remote)
    secret: ""      # The secret to use to authenticate to the geo localization service (if they are remote)

rulesets:
  - type: "local|remote" # The type of the ruleset distribution (local or remote)
    path: ""      # The path to the ruleset file
    timeout: 60   # Timeout for a request
    host: ""      # The ruleset distribution host (if they are remote)
    port: "80"    # The ruleset distribution port (if they are remote)
    region: ""    # The region of the ruleset distribution (if they are remote)
    token: ""     # The token to use to authenticate to the ruleset distribution (if they are remote)
    secret: ""    # The secret to use to authenticate to the ruleset distribution (if they are remote)
  - type: "local|remote" # YES you can use multiple rulesets, and YES mix of local and remote too!
    path: ""      # The path to the ruleset file
    timeout: 60   # Timeout for a request
    host: ""      # The ruleset distribution host (if they are remote)
    port: "80"    # The ruleset distribution port (if they are remote)
    region: ""    # The region of the ruleset distribution (if they are remote)
    token: ""     # The token to use to authenticate to the ruleset distribution (if they are remote)
    secret: ""    # The secret to use to authenticate to the ruleset distribution (if they are remote)

debug_level: 0      # Optional, this is the debug level (0 for no debug, 1 or more for debug, the higher the number the more verbose the output will be)
```

The sections are:

- The remote section configures the remote configuration API
- The database section configures the database
- The crawler section configures the crawler
- The image_storage_api section configures the image storage API
- The file_storage_api section configures the file storage API (downloaded files and/or web objects)
- The API section configures the API
- The selenium section configures the Selenium Chrome container
- The network_info section configures the network information gathering
- The rulesets section configures the rulesets that will be loaded on the specific CROWler engine
- The debug_level section configures the debug level

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

- host is the database host name or IP
- port is the database port
- user is the database user
- password is the database password
- dbname is the database name (by default SitesIndex).
- You can use ENV variables in the config.yaml file as in the example above, you can name the variables as you wish. If you want to use ENV variables remember to put them between `${}` like this `${POSTGRES_USER}`.

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

- workers is the number of workers the crawler engine will use
- depth is the maximum depth the crawler will reach
- delay is the delay between two requests
- interval is the time before start executing action rules on a just fetched page (this is useful for slow websites)
- timeout is the timeout for a request and maintenance is the time
between two maintenance operations.
- maintenance is the time between two maintenance operations (in minutes). Keep in mind that DB maintenance is done only if there aren't active crawling operations. So, if you set maintenance to 1 hour and you have a crawling operation that lasts 2 hours, the DB maintenance will be done after the crawling operation is finished.

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

- local means that we'll use the local disc as images storage. This is the
default value.
- s3 means that we'll use AWS S3 as images storage.
- api means that we'll use an API as images storage.

**path:**

- If we selected local storage then it's the path where the images will
be stored.
- If we selected S3 storage then it's the bucket name.
- If we selected the API storage then it's the API URL.

**token:**

- If we selected the API storage then it's the token to use to authenticate
to the API.
- If we selected the S3 storage then it's the AWS access key.
- If we selected local storage then it's ignored.

**secret:**

- If we selected the API storage then it's the secret to use to
authenticate to the API.
- If we selected the S3 storage then it's the AWS secret key.
- If we selected local storage then it's ignored.

**region:**

- If we selected the API storage then it's ignored.
- If we selected the S3 storage then it's the AWS region.
- If we selected local storage then it's ignored.

**timeout:**

is the timeout for the image storage API. It's expressed in seconds.

## Adding configuration validation in VSCode

To add the CROWler configuration validation in VSCode, you can use the
following extension:

Open (or create) your VSCode settings.json file and add the following:

```json
"yaml.schemas": {
    "./schemas/crowler-config-schema.json": ["config.y*ml", "*-config.y*ml"],
}
```

Then, ensure you call all your config files with the `-config.yaml` or
`-config.yml` extension.

This will allow you to validate your configurations in VSCode as you type them.
