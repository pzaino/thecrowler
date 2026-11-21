FROM golang:1.27-alpine AS build
WORKDIR /src
COPY tests/integration/infoseed-compose/fixture-server/main.go /src/main.go
RUN CGO_ENABLED=0 go build -o /fixture-server /src/main.go
FROM alpine:3.22
RUN adduser -D -H fixture
COPY --from=build /fixture-server /usr/local/bin/fixture-server
USER fixture
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/fixture-server"]
