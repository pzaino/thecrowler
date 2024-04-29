ARG NAMESPACE
ARG VERSION
ARG AUTHORS
ARG BASE

# Build the extra browser Automation for the CROWler
FROM ${NAMESPACE}/${BASE}:${VERSION} AS builder

USER root

#====================================
# Install go lang to build "Rbee" for the CROWler
#====================================
# Add the PPA for up-to-date Go versions and install Go
RUN apt-get update \
    && apt-get install -y software-properties-common \
    && apt-get install -y wget \
    && apt-get install -y git \
    && apt-get install -y unzip
RUN sudo add-apt-repository ppa:longsleep/golang-backports \
    && apt-get update \
    && apt-get install -y golang-go

# Check Go version to ensure correct installation
RUN go version

# Install dependecies for the Go project
RUN apt install -y libx11-dev libxtst-dev libxext-dev

# Copy and build your Go project
WORKDIR /src
COPY ./Rbee/cmd ./cmd
COPY ./Rbee/pkg ./pkg
COPY ./Rbee/services ./services
COPY ./Rbee/go.mod .
COPY ./Rbee/go.sum .
COPY ./Rbee/main.go .
COPY ./Rbee/config.yaml .
COPY ./Rbee/schemas/ ./schemas
COPY ./Rbee/autobuild.sh .
COPY ./Rbee/browserAutomation.conf .
RUN chmod +x autobuild.sh

WORKDIR /src
RUN ./autobuild.sh rb

# Build the Selenium Standalone image and copy the binary
FROM ${NAMESPACE}/${BASE}:${VERSION}
LABEL authors=${AUTHORS}

USER ${SEL_UID}

#====================================
# Scripts to run Selenium Standalone
#====================================
COPY --chown="${SEL_UID}:${SEL_GID}" start-selenium-standalone.sh /opt/bin/start-selenium-standalone.sh
COPY --from=builder --chown="${SEL_UID}:${SEL_GID}" /src/bin/browserAutomation /opt/bin/browserAutomation
COPY --from=builder --chown="${SEL_UID}:${SEL_GID}" /src/browserAutomation.conf /etc/supervisor/conf.d/browserAutomation.conf

#==============================
# Supervisor configuration file
#==============================
COPY selenium.conf /etc/supervisor/conf.d/

# Copying configuration script generator
COPY --chown="${SEL_UID}:${SEL_GID}" generate_config /opt/bin/generate_config

# In seconds, maps to "--session-request-timeout"
ENV SE_SESSION_REQUEST_TIMEOUT 300
# In seconds, maps to "--session-retry-interval"
ENV SE_SESSION_RETRY_INTERVAL 15
# In seconds, maps to "--healthcheck-interval"
ENV SE_HEALTHCHECK_INTERVAL 120
# Boolean value, maps "--relax-checks"
ENV SE_RELAX_CHECKS true

EXPOSE 4444

ENV SE_OTEL_SERVICE_NAME "selenium-standalone"
