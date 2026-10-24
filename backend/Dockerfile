# syntax=docker/dockerfile:1
#
# Targets:
#   runtime  (default, LAST stage) — production image: API + watcher-backfill.
#            It contains NO key-generation / key-rotation tooling.
#   tools    (opt-in)              — operator image: custody-keygen,
#            gasstation-keygen, custody-reencrypt. Never deploy it as a service.
#
#   docker build -t korzadi-wallet-api:TAG backend/                    # production
#   docker build --target tools -t korzadi-wallet-tools:TAG backend/   # operators
#
# See backend/docs/CUSTODY_KEY_MANAGEMENT.md.

FROM golang:1.26.6-alpine AS build
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /korzadi-wallet-api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -o /watcher-backfill ./cmd/watcher-backfill

# Integration runner: source and module cache only. It intentionally does
# not build production binaries or contain production credentials; Compose
# runs its tests on an internal network against ephemeral Postgres/Redis.
FROM golang:1.26.6-alpine AS integration-tests
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .

# Tools that handle key material are compiled in their OWN stage so they can
# never leak into `runtime` through a stray COPY.
FROM build AS build-tools
RUN CGO_ENABLED=0 GOOS=linux go build -o /custody-keygen ./cmd/custody-keygen
RUN CGO_ENABLED=0 GOOS=linux go build -o /gasstation-keygen ./cmd/gasstation-keygen
RUN CGO_ENABLED=0 GOOS=linux go build -o /custody-reencrypt ./cmd/custody-reencrypt

FROM alpine:3.19 AS tools
LABEL org.korzadi.image-role="tools" \
      org.korzadi.warning="OPERATIONAL TOOLS WITH KEY ACCESS - do not deploy as a service"
RUN apk add --no-cache ca-certificates && \
    addgroup -S korzadi && adduser -S korzadi -G korzadi
COPY --from=build-tools /custody-keygen /usr/local/bin/custody-keygen
COPY --from=build-tools /gasstation-keygen /usr/local/bin/gasstation-keygen
COPY --from=build-tools /custody-reencrypt /usr/local/bin/custody-reencrypt
USER korzadi
CMD ["sh"]

# Keep `runtime` as the LAST stage: a plain `docker build` / compose build
# without --target must produce the production image.
FROM alpine:3.19 AS runtime
LABEL org.korzadi.image-role="runtime"
RUN apk add --no-cache ca-certificates && \
    addgroup -S korzadi && adduser -S korzadi -G korzadi
COPY --from=build /korzadi-wallet-api /korzadi-wallet-api
COPY --from=build /watcher-backfill /watcher-backfill
COPY migrations /migrations
RUN chown -R korzadi:korzadi /migrations
USER korzadi
EXPOSE 8060
ENTRYPOINT ["/korzadi-wallet-api"]
