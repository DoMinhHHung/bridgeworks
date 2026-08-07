# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26.5

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/identity-service \
    ./cmd/identity-service && \
    CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/identity-migrate \
    ./cmd/identity-migrate && \
    CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/identity-platform-access \
    ./cmd/identity-platform-access

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && \
    addgroup -S -g 65532 nonroot && \
    adduser -S -D -H -u 65532 -G nonroot nonroot

COPY --from=build --chown=nonroot:nonroot /out/identity-service /usr/local/bin/identity-service
COPY --from=build --chown=nonroot:nonroot /out/identity-migrate /usr/local/bin/identity-migrate
COPY --from=build --chown=nonroot:nonroot /out/identity-platform-access /usr/local/bin/identity-platform-access

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/identity-service"]
