FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY pkg ./pkg
COPY tests/integration/infoseed-compose/probe ./tests/integration/infoseed-compose/probe
RUN CGO_ENABLED=0 go build -o /infoseed-probe ./tests/integration/infoseed-compose/probe
FROM alpine:3.22
RUN adduser -D -H probe
COPY --from=build /infoseed-probe /usr/local/bin/infoseed-probe
USER probe
ENTRYPOINT ["/usr/local/bin/infoseed-probe"]
