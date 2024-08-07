# The CROWler

<img align="right" width="320" height="280"
 src="https://raw.githubusercontent.com/pzaino/thecrowler/main/images/TheCROWler_v1JPG.jpg" alt="TheCROWLer Logo">

![Go build: ](https://github.com/pzaino/TheCROWler/actions/workflows/go.yml/badge.svg)
![CodeQL: ](https://github.com/pzaino/TheCROWler/actions/workflows/github-code-scanning/codeql/badge.svg)
![Scorecard supply-chain security: ](https://github.com/pzaino/TheCROWler/actions/workflows/scorecard.yml/badge.svg)
<!-- <a href="https://www.bestpractices.dev/projects/8344"><img
src="https://www.bestpractices.dev/projects/8344/badge"
alt="OpenSSF Security Best Practices badge"></a> //-->
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/8344/badge)](https://www.bestpractices.dev/projects/8344)
[![Codacy Badge](https://app.codacy.com/project/badge/Grade/bb3868a72f044f1fb2ebe5516224d943)](https://app.codacy.com/gh/pzaino/thecrowler/dashboard?utm_source=gh&utm_medium=referral&utm_content=&utm_campaign=Badge_grade)
<!-- ![Docker build: ]() -->
[![Go Report Card](https://goreportcard.com/badge/github.com/pzaino/TheCROWler)](https://goreportcard.com/report/github.com/pzaino/thecrowler)
[![Go-VulnCheck](https://github.com/pzaino/thecrowler/actions/workflows/go-vulncheck.yml/badge.svg)](https://github.com/pzaino/thecrowler/actions/workflows/go-vulncheck.yml)
[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Fpzaino%2Fthecrowler.svg?type=shield&issueType=license)](https://app.fossa.com/projects/git%2Bgithub.com%2Fpzaino%2Fthecrowler?ref=badge_shield&issueType=license)
[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Fpzaino%2Fthecrowler.svg?type=shield&issueType=security)](https://app.fossa.com/projects/git%2Bgithub.com%2Fpzaino%2Fthecrowler?ref=badge_shield&issueType=security)
![License: ](https://img.shields.io/github/license/pzaino/TheCROWler)

**Project status:** Still under active development! However, most of it is
already usable. Alpha testers welcome!
Full stats on daily work [here](https://repo-tracker.com/r/gh/pzaino/thecrowler?utm_source=github-stats).

**Please Note**: This is the new official repo for the project, the old C++
and Rust repositories are now closed and no longer available/maintained.
Please use this one for any new development.

## What is it?

The CROWler is an open-source, feature-rich web crawler designed with a unique
philosophy at its core: to be as gentle and low-noise as possible. In other
words, The CROWler tries to stand out by ensuring minimal impact on the
websites it crawls while maximizing convenience for its users.

Additionally, the system is equipped with an API, providing a streamlined
interface for data queries. This feature ensures easy integration and
access to indexed data for various applications.

The CROWler is designed to be micro-services based, so it can be easily
deployed in a containerized environment.

### Features

- **Low-noise**: The CROWler is designed to be as gentle as possible when
crawling websites. It respects robots.txt, and it's designed to try to appear
as a human user to the websites it crawls.
- **Customizable Crawling**: Tailor your crawling experience like never before.
Specify URLs and configure individual crawling parameters to fit your precise
needs. Whether it's a single page or an expansive domain, The CROWler adapts to
your scope with unmatched flexibility.
- **Scope Variability**: Define your crawling boundaries with precision. Choose
from:
  - Singular URL Crawling
  - Domain-wide Crawling (combining L3, L2, and L1 domains)
  - L2 and L1 Domain Crawling
  - L1 Domain Crawling (e.g., everything within ".com")
  - Full Recursive Crawling, venturing beyond initial boundaries to explore
  connected URLs
- **Advanced Detection Capabilities**: Discover a wealth of information with
features that go beyond basic crawling:
  - URL and Content Discovery
  - Page Content, Metadata, and and more
  - Keywords Analysis and Language Detection
  - Insightful HTTP Headers, Network Info, WHOIS, DNS, and Geo-localization
  Data
- **Sophisticated Ruleset**: To leverage rules-based activities and logic
  customization, The CROWler offers:
  - Scraping rules: To extract precisely what you need from websites
  - Actions rules: To interact with websites in a more dynamic way
  - Detection rules: To identify specific patterns or elements on a page,
    technologies used, etc.
  - Crawling rules: To define how the crawler should behave in different
    situations (for instance both recursive and non-recursive crawling,
    fuzzing, etc.)
- **Powerful Search Engine Integration**: Utilize an API-driven search engine
equipped with dorking capabilities and comprehensive content search, opening
new avenues for data analysis and insight.

### What problem does it solves?

The CROWler is designed to solve a set of problems about web crawling, content
discovery, and data extraction. It's designed to be able to crawl websites in a
respectful and efficient way. It's also designed to be able to crawl private
networks and intranets, so you can use it to create your own or company search
engine.

On top of that it can also be used as the "base" for a more complex cyber security
tool, as it can be used to gather information about a website, its network, its
owners, which services are being exposed etc.

Given it can also extract information, it can be used to create knowledge bases
with reference to the sources, or to create a database of information about a
specific topic.

Obviously, it can also be used to do keywords analysis, language detection, etc.
but this is something every single crawler can be used for. However all the
"classic" features are implemented/being implemented.

### How do I pronounce the name?

**The**: Pronounced as /ðə/ when before a consonant sound, it sounds like
"thuh."

**CROW**: Pronounced as /kroʊ/, rhymes with "know" or "snow."

**ler**: The latter part is pronounced as /lər/, similar to the ending of the
 word "crawler" or the word "ler" in "tumbler."

Putting it all together, it sounds like "**thuh KROH-lər**"

### What ChatGPT thinks about the CROWLer ;)

"The CROWler is not just a tool; it's a commitment to ethical, efficient, and
effective web crawling. Whether you're conducting academic research, market
analysis, or enhancing your cybersecurity posture, The CROWler delivers with
integrity and precision.

Join us in redefining the standards of web crawling. Explore more and contribute
to The CROWler's journey towards a more respectful and insightful digital
exploration."

😂 that's clearly a bit over the top, but it was fun and I decided to include
it here, just for fun. BTW it does make me fell like I want to add:

"...and there is one more thing!" (I wonder why?!?!) 😂

## How to use it?

### Prerequisites

The CROWler is designed to be micro-services based, so you'll need to install the
following:

- [Docker](https://docs.docker.com/install/)
- [Docker Compose](https://docs.docker.com/compose/install/)

For a docker compose based installation, that's all you need.
If you have docker and docker compose installed you can skip the next section
and go straight to the **Installation** section.

#### If you're planning to install it manually

If you're planning to install the CROWler manually, you'll need to install
the following Docker container:

- [PostgreSQL Container](https://hub.docker.com/_/postgres)
  - Postgres 15 up (for both ARM and x86) are supported at the moment.
  - And then run the DB Schema setup script on it (make sure you check the
    section of the db schema with the user credentials and set those SQL
    variables correctly)

### Build from source

If you'll use the docker compose then everything will build automatically,
all you'll need to do is follow the instructions in the Installation
section.

If, instead you want to build locally on your machine, then follow the
instructions in this section.

To build the CROWler from source, you'll need to install the following:

- [Go](https://golang.org/doc/install)

Then you'll need to clone the repository and build the targets you need.

To build everything at once run the following command:

```bash
./autobuild.sh
```

To build individual targets:

First, check which targets can be built and are available, run the following
command:

```bash
./autobuild name-of-the-target
```

This will build your requested component in `./bin`

```bash
./bin/removeSite
./bin/addSite
./bin/addCategory
./bin/api
./bin/thecrowler
```

Build them as you need them, or run the `autobuild.sh` (no arguments) to build
them all.

Optionally you can build the Docker image, to do so run the following command:

```bash
docker build -t <image name> .
```

**Note**: If you build the CROWler engine docker container, remember to run
it with the following docker command (it's required!)

```bash
docker run -it --rm --cap-add=NET_ADMIN --cap-add=NET_RAW crowler_engine
```

**Important Note**: If you build from source, you still need to build a
CROWler VDI docker image, that is needed because the CROWler uses a bunch of
external tools to do its job and all those tools are grouped and built in the
VDI image (Virtual Desktop Image).

### Installation

The **easiest way** to install the CROWler is to use the docker compose file.
To do so, follow the steps below.

**Before you start**: There are a bunch of ENV variables you can set to
customize the CROWler deployment. These ENV vars allow you to set up your
username and password for the database, the database name, the port the API
will listen on, etc.

To see the full list of ENV vars you can set, see [here](doc/env_vars.md).

There are 3 ENV vars **you must set**, otherwise the CROWler won't build or
work:

- `DOCKER_CROWLER_DB_PASSWORD`, this is the password for the CROWler user in
the database (non-admin level).
- `DOCKER_POSTGRES_PASSWORD`, this is the password for the postgres user in
the database (admin level).
- `DOCKER_DB_HOST`, this is the hostname, IP or FQDN of the Postgres database.
You normally set this one with the IP of the host where you're running the
Postgres container.

Once you've set your ENV vars, follow these steps:

1. If you haven't yet, clone TheCrowler repository on your build machine
   and `cd` into the root directory of the repository
2. Create your **config.yaml** file (see [here](doc/config_yaml.md) for more
info). You can use the `config.default` file as a template (just rename it
to `config.yaml` and edit it as you need).
3. Run `./docker-build.sh` to build the with Docker compose and the right
platform (see [here](doc/docker_build.md) for more info)

**Please note(1)**: If you have questions about config.yaml or the ENV vars,
or the ruleset etc, you can use the GPT chatbot to help you. Just go to this
link [here (it's freely available to everyone)](https://chatgpt.com/g/g-dEfqHkqrW-the-crowler-support)

**Please Note(2)**: If you're running the CROWler on a Raspberry Pi, you'll
need to build the CROWler with the `arm` platform. To do so, the easier way
is to build the CROWler with the `docker-build.sh` script directly on the
Raspberry Pi.

**Please Note(3)**: If need to do a rebuild and want to clean up everything,
run the following command:

**Please Note(4)**: To build the CROWler VDI docker image, it's required to
build also Selenium (don't worry everything is automatic), however you need
to ensure that GNU Make is installed on your system. That is required to
build selenium images (nothing to do with the CROWler itself).

```bash
./docker-rebuild.sh
```

This will clean up everything and rebuild the CROWler from scratch.

### Usage

For instruction on how to use it see [here](doc/usage.md).

#### Configuration

To configure both the API and the Crawler, you'll need to create a config.yaml
file in the root directory of the repository.

See [here](doc/config_yaml.md) for more info.

## Production

If you want to use the CROWler in production, I recommend to use the docker
compose installation. It's the easiest way to install it and it's the most
secure one.

For better security I strongly recommend to deploy the API in a separate container
than the CROWler one. Also, there is no need to expose the CROWler container to the
outside world, it will need internet access thought.

## DB Maintenance

The CROWler default configuration uses PostgreSQL as its database. The database is
stored in a Docker volume and is persistent.

The DB should need no maintenance, The CROWler will take care of that. Any time
there is no crawling activity and it's passed 1 hours from the previous
maintenance activity, The CROWler will clean up the database and optimize the
indexes.
