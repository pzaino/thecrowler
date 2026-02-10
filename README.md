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

## What is it?

The CROWler is a self-hosted, event-driven Content Discovery and Intelligence development platform
designed for advanced web crawling, scraping, detection, and automation using real browsers,
rulesets, plugins, and agents.

**Project status:** Still under active development (WIP). Most components are usable.
Beta testers welcome. Full [daily progress stats](https://githubtracker.com/pzaino/thecrowler).

Additionally, the system is equipped with a powerful search API, providing
a streamlined interface for data queries. This feature ensures easy
integration and access to indexed data for various applications.

The CROWler is designed to be micro-services based, so it can be easily
deployed in a containerized environment.

As a Content Discovery Development Platform, The CROWler integrates a wide
range of technologies and features, including Plugins, Rulesets, Agents,
Events and more. These components work together to provide a comprehensive
platform to develop your own solutions for content discovery, data extraction,
and more.

## What Makes the CROWler Different

- **Real browsers, not abstractions**
  The CROWler uses actual browser engines (Chromium, Chrome, Firefox) instead of
  simplified request pipelines.

- **Declarative rulesets**
  Crawling, scraping, detection, and actions are defined in versioned YAML/JSON rulesets.

- **First-class detection logic**
  Technologies, frameworks, objects, and vulnerabilities are detected using user-defined rules.

- **Event-driven automation**
  Crawling and detection events can trigger agents, plugins, and workflows.

- **Extensible by design**
  JavaScript plugins can extend the engine, browser, API, and event system.

- **You own the data and infrastructure**
  The CROWler is fully self-hosted and auditable.

## Who Is the CROWler For?

The CROWler is designed for:

- Engineers and developers
- Security researchers
- Intelligence and OSINT teams
- Advanced data collection pipelines
- Organizations that require full control and auditability

It is **not** designed as a point-and-click scraping SaaS or a turnkey data service.

## Design Philosophy

The CROWler is built on a few core principles:

- Control is better than abstraction
- Logic should be explicit and "inspectable"
- Automation should be event-driven
- Intelligence should be user-defined
- Infrastructure and data ownership matter

## Getting Started

- Documentation: [doc/](https://github.com/pzaino/thecrowler/tree/develop/doc#readme)
- GPT-based Support [Chatbot](https://chatgpt.com/g/g-dEfqHkqrW-the-crowler-support) (You must be logged in on CHatGPT to use it properly, CustomGPTs have very limited access otherwise)
- Configuration examples: [config.default](config.default)
- Ruleset schemas: [schemas/](schemas/)
- Plugin examples: [plugins/](plugins/)

Start with the installation guide (below) and the minimal configuration example.

## Table of contents

- [Features](#features-overview)
- [What problem does it solve?](#what-problem-does-it-solve)
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

## Features (Overview)

The CROWler is a **full-spectrum Content Discovery and Intelligence platform**.
Its capabilities span crawling, interaction, detection, automation, security,
and large-scale data analysis.

Below is a **high-level overview** of the main feature areas.
For a complete and detailed breakdown, see: **[doc/features.md](doc/features.md)**

### Web Crawling & Interaction

- Recursive and scoped crawling (URL, domain, subdomain, recursive)
- Real browser rendering (Chromium, Chrome, Firefox)
- Human Behavior Simulation (HBS)
- Dynamic JavaScript content handling
- Custom User-Agent, request filtering, and bandwidth control

### Search & Discovery

- High-performance API-based search engine
- Advanced query operators and dorking
- Entity extraction and correlation
- Result export (CSV, JSON)

### Scraping & Data Processing

- Declarative scraping rules (CSS, XPath, regex)
- Post-processing, transformation, and enrichment
- Plugin- and AI-based data pipelines

### Detection & Intelligence

- Technology, framework, and library detection
- Vulnerability and security header analysis
- TLS/SSL fingerprinting (JA3, JA4, certificates)
- Integration with external intelligence sources

### Rules, Actions & Automation

- Declarative rulesets (crawl, scrape, action, detection)
- System-level action execution via real browsers
- Event-driven workflows and scheduling

### Extensibility

- JavaScript plugin system (engine, browser, API, event)
- Custom API endpoints
- Agent-controlled execution

### Agents & AI

- Traditional and AI agents
- Event-driven agent orchestration
- Pre-deployed containerized AI models (CUDA / non-CUDA)
- Multi-model AI workflows

### Security & Cybersecurity

- Network reconnaissance (DNS, WHOIS, service discovery)
- Fuzzing and security testing
- Native support for third-party security services

### Deployment & Scalability

- Microservices architecture
- Horizontal scaling of engines, VDIs, APIs
- Docker-based deployment
- On-prem, cloud, and hybrid environments

**Full feature list and detailed explanations:**
[doc/features.md](doc/features.md)

### What problem does it solve?

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

(The following section is intentionally light-hearted and non-authoritative.)

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

The CROWler is designed as a microservices-based system, allowing independent scaling, isolation, and orchestration of engines, VDIs, APIs, and event management.

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
