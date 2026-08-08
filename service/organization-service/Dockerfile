# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26.5
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/organization-service ./cmd/organization-service && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/organization-migrate ./cmd/organization-migrate && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/organization-maintenance ./cmd/organization-maintenance

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && \
    addgroup -S -g 65532 nonroot && \
    adduser -S -D -H -u 65532 -G nonroot nonroot
COPY --from=build --chown=nonroot:nonroot /out/organization-service /usr/local/bin/organization-service
COPY --from=build --chown=nonroot:nonroot /out/organization-migrate /usr/local/bin/organization-migrate
COPY --from=build --chown=nonroot:nonroot /out/organization-maintenance /usr/local/bin/organization-maintenance
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/organization-service"]
