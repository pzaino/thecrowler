ARG NAMESPACE=selenium
ARG VERSION
ARG BASE=node-base
ARG AUTHORS=zfpsystems

# Build the extra browser Automation for the CROWler
FROM ${NAMESPACE}/${BASE}:${VERSION} AS builder
LABEL authors=${AUTHORS}

USER root

#====================================
# Install Go for ARM64 and AMD64 based on architecture
#====================================
# Set the locale to prevent encoding issues
ENV LANG C.UTF-8
ENV LC_ALL C.UTF-8
ENV LANGUAGE C.UTF-8

# Set the required Go version
ENV GO_VERSION 1.22.5

RUN apt-get clean \
    && apt-get update \
    && apt-get install -f \
    && dpkg --configure -a \
    && rm -rf /var/lib/apt/lists/*

# Remove any existing diversion of /lib32 (to prevent clashes)
RUN dpkg-divert --list | grep lib32 && dpkg-divert --remove /lib32 || true
# Remove any existing diversion of /lib64 (to prevent clashes)
RUN dpkg-divert --list | grep lib64 && dpkg-divert --remove /lib64 || true

RUN apt-get update \
    && apt-get install -y apt-utils \
    && apt-get install -y usrmerge \
    && rm -rf /var/lib/apt/lists/*

#RUN apt-get update \
#    && apt-get install -y base-files \
#    && rm -rf /var/lib/apt/lists/*

#RUN echo "base-files hold" | dpkg --set-selections

RUN apt-get update \
    && apt-get install -y wget \
    && apt-get install -y git \
    && apt-get install -y unzip \
    && apt-get install -y curl \
    && apt-get install -y software-properties-common \
    && apt-get install -y libx11-dev libxtst-dev libxext-dev \
    && rm -rf /var/lib/apt/lists/*

# Install build-essential for Go, it's required by RBee compilation
RUN apt-get update \
    && apt-get install -y gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*

# Detect architecture and install the appropriate Golang binary
RUN TARGETARCH="$(uname -m)" && \
    if [ "$TARGETARCH" = "amd64" ]; then \
        curl -L -o go${GO_VERSION}.linux-amd64.tar.gz https://dl.google.com/go/go${GO_VERSION}.linux-amd64.tar.gz; \
    elif [ "$TARGETARCH" = "arm64" ] || [ "$TARGETARCH" = "aarch64" ]; then \
        TARGETARCH="arm64"; \
        curl -L -o go${GO_VERSION}.linux-arm64.tar.gz https://dl.google.com/go/go${GO_VERSION}.linux-arm64.tar.gz; \
    fi \
    && tar -C /usr/local -xzf go${GO_VERSION}.linux-${TARGETARCH}.tar.gz \
    && rm go${GO_VERSION}.linux-${TARGETARCH}.tar.gz

# Add Go to PATH
ENV PATH $PATH:/usr/local/go/bin

# Check Go version to ensure correct installation
RUN go version

#====================================
# Copy and build the Go project
#====================================
WORKDIR /src
COPY ./Rbee/cmd ./cmd
COPY ./Rbee/pkg ./pkg
COPY ./Rbee/go.mod .
COPY ./Rbee/go.sum .
COPY ./Rbee/autobuild.sh .
COPY ./Rbee/browserAutomation.conf .
RUN chmod +x autobuild.sh

# Build the project
RUN ./autobuild.sh rb

#====================================
# Final stage: Build the Selenium Standalone image and copy the binary
#====================================
FROM ${NAMESPACE}/${BASE}:${VERSION}
LABEL authors=${AUTHORS}

USER ${SEL_UID}

# Copy the Selenium scripts and built binary
COPY --chown="${SEL_UID}:${SEL_GID}" start-selenium-standalone.sh /opt/bin/start-selenium-standalone.sh
COPY --from=builder --chown="${SEL_UID}:${SEL_GID}" /src/bin/rbee /opt/bin/rbee
COPY --from=builder --chown="${SEL_UID}:${SEL_GID}" /src/browserAutomation.conf /etc/supervisor/conf.d/browserAutomation.conf

# Copy the supervisor configuration
COPY selenium.conf /etc/supervisor/conf.d/

# Copy configuration script generator
COPY --chown="${SEL_UID}:${SEL_GID}" generate_config /opt/bin/generate_config

# Environment variables for Selenium
ENV SE_SESSION_REQUEST_TIMEOUT 300
ENV SE_SESSION_RETRY_INTERVAL 15
ENV SE_HEALTHCHECK_INTERVAL 120
ENV SE_RELAX_CHECKS true

# Expose Selenium port
EXPOSE 4444

# Service name for OpenTelemetry
ENV SE_OTEL_SERVICE_NAME "selenium-standalone"
