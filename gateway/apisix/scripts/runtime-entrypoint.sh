#!/bin/sh
set -eu

fail() {
  echo "bridgeworks-apisix: $*" >&2
  exit 1
}

if [ "${PORT:-9080}" != "9080" ]; then
  fail "APISIX container port must be configured as 9080"
fi

/usr/local/bin/bridgeworks-apisix-runtime-config \
  "${RUNTIME_TARGET:-}" \
  /usr/local/apisix/bridgeworks/conf \
  /usr/local/apisix/conf

export APISIX_STAND_ALONE=true

exec /docker-entrypoint.sh docker-start
