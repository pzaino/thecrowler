# Understanding the config.yaml file

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
database:
  host: localhost
  port: 5432
  user: ${POSTGRES_USER}
  password: ${POSTGRES_PASSWORD}
  dbname: SitesIndex
crawler:
  workers: 5
  depth: 1
  delay: "2"
  timeout: 10
  maintenance: 60
image_storage_api:
  type: local
api:
  port: 8080
  host: 0.0.0.0
  timeout: 10
selenium:
  type: chrome
  port: 4444
  headless: true
  host: localhost
debug_level: 0
```

The sections are:

- The database section configures the database
- The crawler section configures the crawler
- The image_storage_api section configures the image storage API
- The API section configures the API
- The selenium section configures the Selenium Chrome container
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

## The crawler section

The crawler section configures the crawler. It should look like this:

```yaml
crawler:
  workers: 5
  depth: 1
  delay: 2
  timeout: 10
  maintenance: 60
```

- workers is the number of workers the crawler will use
- depth is the maximum depth the crawler will reach
- delay is the delay between two requests
- timeout is the timeout for a request and maintenance is the time
between two maintenance operations.

## The image_storage_api section

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
    "./schemas/crowler-config-schema.json": "*-config.y*ml",
}
```

Then, ensure you call all your config files with the `-config.yaml` or
`-config.yml` extension.

This will allow you to validate your configurations in VSCode as you type them.
