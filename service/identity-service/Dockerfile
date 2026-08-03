# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26.5
ARG ALPINE_VERSION=3.24.1

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/identity-service \
    ./cmd/identity-service

FROM alpine:${ALPINE_VERSION}
RUN addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /out/identity-service /usr/local/bin/identity-service

USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/identity-service"]
