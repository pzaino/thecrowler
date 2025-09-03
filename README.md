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
[![Go Reference](https://pkg.go.dev/badge/github.com/pzaino/thecrowler.svg)](https://pkg.go.dev/github.com/pzaino/thecrowler)
![GitHub language count](https://img.shields.io/github/languages/count/pzaino/thecrowler)
![GitHub commit activity](https://img.shields.io/github/commit-activity/t/pzaino/thecrowler)
![GitHub contributors](https://img.shields.io/github/contributors/pzaino/thecrowler)
![GitHub code search count](https://img.shields.io/github/search?query=thecrowler)

![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/pzaino/thecrowler)
![GitHub Tag](https://img.shields.io/github/v/tag/pzaino/thecrowler)

**Project status:** **Still under active development! (aka WIP)** However, most of it is
already usable. Alpha testers welcome!
Full stats on daily work [here](https://githubtracker.com/pzaino/thecrowler).

**Please Note**: This is the new official repo for the project, the old C++
and Rust repositories are now closed and no longer available/maintained.
Please use this one for any new development.

## What is it?

The CROWler is an open-source, feature-rich web crawler and Content Discovery
Development Platform, designed with a unique philosophy at its core: to be as
gentle and low-noise as possible. In other words, The CROWler tries to stand
out by ensuring minimal impact on the websites it crawls while maximizing
convenience for its users.

Additionally, the system is equipped with an powerful search API, providing
a streamlined interface for data queries. This feature ensures easy
integration and access to indexed data for various applications.

The CROWler is designed to be micro-services based, so it can be easily
deployed in a containerized environment.

As a Content Discovery Development Platform, The CROWler integrates a wide
range of technologies and features, including Plugins, Rulesets, Agents,
Events and more. These components work together to provide a comprehensive
platform to develop your own solutions for content discovery, data extraction,
and more.

## Table of contents

- [Features](#features)
- [What problem does it solves?](#what-problem-does-it-solves)
- [How do I pronounce the name?](#how-do-i-pronounce-the-name)
- [How to use it?](#how-to-use-it)
  - [Prerequisites](#prerequisites)
  - [Installation](#installation)
    - [Easy Installation and deployment](#1-easy-installation-and-deployment)
    - [If you're planning to install it manually](#2-if-youre-planning-to-install-it-manually)
    - [Build from source](#build-from-source)
- [Production](#production)
- [DB Maintenance](#db-maintenance)
- [License](#license)
- [Contributing](#contributing)
- [Code of Conduct](#code-of-conduct)
- [Acknowledgements](#acknowledgements)
- [Disclaimer](#disclaimer)
- [Top Contributors](#top-contributors)

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
  - Page Content, Metadata, XHR, fetch, and more
  - Keywords extraction and analysis, Language Detection
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
- **Plugin System**: Extend The CROWler's capabilities with a robust plugin system
  architecture, allowing developers to create entirely new solutions and/or integrate seamlessly with existing ones.
  - Plugins can be executed in the browser context, enabling rich interactions with web pages.
  - They can also be executed in the engine context, allowing for deeper integration with the crawling process.
  - Plugins are written in JavaScript and Javascript has been extended to support ETL (Extract, Transform, Load) operations as well as Event-driven programming, multiple DBs (PostgreSQL, MongoDB, Neo4J, MySQL, SQLite) and there are also extensions to use CSV and other file types directly and seamlessly.
  - Plugins can also be controlled via the new Agents Architecture as well as the Events API.
- **Powerful Search Engine Integration**: Utilize an API-driven search engine
equipped with dorking capabilities and comprehensive content search, opening
new avenues for data analysis and insight.

For more information on the features, see the [features](doc/features.md) page.

### What problem does it solves?

The CROWler is designed to solve a set of problems about web crawling, content
 discovery, technology detection and data extraction.

While it’s main goal is to enable private, professional and enterprise users to
quickly develop their content discovery solutions, It’s also designed to be
able to crawl private networks and intranets, so you can use it to create your
own or your company search engine.

On top of that it can also be used as the "base" for a more complex cyber security
tool, as it can be used to gather information about a website, its network, its
owners, vulnerabilities, which services are being exposed etc.

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

### Installation

#### 1. Easy Installation and deployment

The **easiest way** to install the CROWler is to use the docker compose file.
To do so, follow the [instructions here](doc/docker_build.md).

**Please note(1)**: If you have questions about config.yaml or the ENV vars,
or the ruleset etc, you can use the GPT chatbot to help you. Just go to this
link [here (it's freely available to everyone)](https://chatgpt.com/g/g-dEfqHkqrW-the-crowler-support)

**Please Note(2)**: If you're running the CROWler on a Raspberry Pi, you'll
need to build the CROWler for the `arm64` platform. To do so, the easier way
is to build the CROWler with the `docker-build.sh` script directly on the
Raspberry Pi.

#### 2. If you're planning to install it manually

If, instead, you're planning to install the CROWler manually, you'll need to
install the following Docker container:

- [PostgreSQL Container](https://hub.docker.com/_/postgres)
  - Postgres 15 up (for both ARM and x86) are supported at the moment.
  - And then run the DB Schema setup script on it (make sure you check the
    section of the db schema with the user credentials and set those SQL
    variables correctly)

- Also please note: The Crowler will need its VDI image to be built, so you'll
  need to build the VDI image as well.

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

### Usage

For instruction on how to use it see [here](doc/usage.md).

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

## License

The CROWler is licensed under the Apache 2.0 License. For more information, see
the [LICENSE](LICENSE) file.

## Contributing

If you want to contribute to the project, please read the [CONTRIBUTING](CONTRIBUTING.md)
file.

## Code of Conduct

The CROWler has adopted the Contributor Covenant Code of Conduct. For more
information, see the [CODE_OF_CONDUCT](CODE_OF_CONDUCT.md) file.

## Acknowledgements

The CROWler is built on top of a lot of open-source projects, and I want to
thank all the developers that contributed to those projects. Without them, the
CROWler would not be possible.

Also, I want to thank the people that are helping me with the project, either
by contributing code, by testing it, or by providing feedback. Thank you all!

## Disclaimer

The CROWler is a tool designed to help you crawl websites in a respectful way.
However, it's up to you to use it in a respectful way. The CROWler is not
responsible for any misuse of the tool.

## Top Contributors

<a href="https://github.com/pzaino/thecrowler/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=pzaino/thecrowler" />
</a>
