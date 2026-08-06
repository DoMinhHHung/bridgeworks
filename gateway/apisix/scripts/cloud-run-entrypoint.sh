#!/bin/sh
set -eu

required_variables="
IDENTITY_SERVICE_HOST
IDENTITY_SERVICE_AUDIENCE
ORGANIZATION_SERVICE_HOST
ORGANIZATION_SERVICE_AUDIENCE
CLERK_AUTHORIZED_PARTIES
"

for variable_name in $required_variables; do
  eval "variable_value=\${$variable_name:-}"
  if [ -z "$variable_value" ]; then
    echo "required environment variable is missing: $variable_name" >&2
    exit 1
  fi
done

validate_run_app_host() {
  variable_name="$1"
  eval "variable_value=\${$variable_name}"

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
  eval "variable_value=\${$variable_name}"

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
  esac
}

validate_run_app_host IDENTITY_SERVICE_HOST
validate_run_app_host ORGANIZATION_SERVICE_HOST
validate_run_app_audience IDENTITY_SERVICE_AUDIENCE
validate_run_app_audience ORGANIZATION_SERVICE_AUDIENCE

if [ "${PORT:-9080}" != "9080" ]; then
  echo "Cloud Run container port must be configured as 9080" >&2
  exit 1
fi

export APISIX_STAND_ALONE=true

exec /docker-entrypoint.sh docker-start
