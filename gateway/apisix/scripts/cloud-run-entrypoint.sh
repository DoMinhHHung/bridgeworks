#!/bin/sh
set -eu

require_non_empty() {
  variable_name="$1"
  variable_value="$2"

  if [ -z "$variable_value" ]; then
    echo "required environment variable is missing: $variable_name" >&2
    exit 1
  fi
}

validate_run_app_host() {
  variable_name="$1"
  variable_value="$2"

  case "$variable_value" in
    *.run.app) ;;
    *)
      echo "$variable_name must be a Cloud Run run.app hostname" >&2
      exit 1
      ;;
  esac

  case "$variable_value" in
    *[!A-Za-z0-9.-]*)
      echo "$variable_name contains invalid hostname characters" >&2
      exit 1
      ;;
  esac
}

validate_run_app_audience() {
  variable_name="$1"
  variable_value="$2"

  case "$variable_value" in
    https://*.run.app) ;;
    *)
      echo "$variable_name must be an HTTPS Cloud Run origin without a path" >&2
      exit 1
      ;;
  esac

  origin_without_scheme="${variable_value#https://}"
  case "$origin_without_scheme" in
    */*|*\?*|*\#*)
      echo "$variable_name must not contain a path, query, or fragment" >&2
      exit 1
      ;;
    *[!A-Za-z0-9.-]*)
      echo "$variable_name contains invalid hostname characters" >&2
      exit 1
      ;;
  esac
}

require_non_empty IDENTITY_SERVICE_HOST "${IDENTITY_SERVICE_HOST:-}"
require_non_empty IDENTITY_SERVICE_AUDIENCE "${IDENTITY_SERVICE_AUDIENCE:-}"
require_non_empty ORGANIZATION_SERVICE_HOST "${ORGANIZATION_SERVICE_HOST:-}"
require_non_empty ORGANIZATION_SERVICE_AUDIENCE "${ORGANIZATION_SERVICE_AUDIENCE:-}"
require_non_empty CLERK_AUTHORIZED_PARTIES "${CLERK_AUTHORIZED_PARTIES:-}"

validate_run_app_host IDENTITY_SERVICE_HOST "$IDENTITY_SERVICE_HOST"
validate_run_app_host ORGANIZATION_SERVICE_HOST "$ORGANIZATION_SERVICE_HOST"
validate_run_app_audience IDENTITY_SERVICE_AUDIENCE "$IDENTITY_SERVICE_AUDIENCE"
validate_run_app_audience ORGANIZATION_SERVICE_AUDIENCE "$ORGANIZATION_SERVICE_AUDIENCE"

if [ "${PORT:-9080}" != "9080" ]; then
  echo "Cloud Run container port must be configured as 9080" >&2
  exit 1
fi

export APISIX_STAND_ALONE=true

exec /docker-entrypoint.sh docker-start
