#!/bin/sh
set -eu

fail() {
  echo "bridgeworks-apisix: $*" >&2
  exit 1
}

require_non_empty() {
  variable_name="$1"
  variable_value="$2"

  [ -n "$variable_value" ] || fail "required environment variable is missing: $variable_name"
}

validate_host() {
  variable_name="$1"
  variable_value="$2"

  require_non_empty "$variable_name" "$variable_value"

  case "$variable_value" in
    *://*|*/*|*\?*|*\#*)
      fail "$variable_name must be a hostname without scheme, path, query, or fragment"
      ;;
    *[!A-Za-z0-9.-]*)
      fail "$variable_name contains invalid hostname characters"
      ;;
    .*|*.|*..*)
      fail "$variable_name must be a valid hostname"
      ;;
  esac
}

validate_run_app_host() {
  variable_name="$1"
  variable_value="$2"

  validate_host "$variable_name" "$variable_value"
  case "$variable_value" in
    *.run.app) ;;
    *) fail "$variable_name must be a Cloud Run run.app hostname" ;;
  esac
}

validate_run_app_audience() {
  variable_name="$1"
  variable_value="$2"

  require_non_empty "$variable_name" "$variable_value"
  case "$variable_value" in
    https://*.run.app) ;;
    *) fail "$variable_name must be an HTTPS Cloud Run origin without a path" ;;
  esac

  origin_without_scheme="${variable_value#https://}"
  validate_run_app_host "$variable_name" "$origin_without_scheme"
}

validate_scheme() {
  variable_name="$1"
  variable_value="$2"

  require_non_empty "$variable_name" "$variable_value"
  case "$variable_value" in
    http|https) ;;
    *) fail "$variable_name must be http or https" ;;
  esac
}

validate_port() {
  variable_name="$1"
  variable_value="$2"

  require_non_empty "$variable_name" "$variable_value"
  case "$variable_value" in
    *[!0-9]*) fail "$variable_name must be a numeric TCP port" ;;
  esac
  [ "$variable_value" -ge 1 ] 2>/dev/null && [ "$variable_value" -le 65535 ] 2>/dev/null ||
    fail "$variable_name must be between 1 and 65535"
}

render_apisix_profile() {
  input_file="$1"
  output_file="$2"

  awk '
    BEGIN { upstream = "" }

    /^  - id: identity-cloud-run$/ {
      upstream = "identity"
      sub(/identity-cloud-run/, "identity-runtime")
    }
    /^  - id: organization-cloud-run$/ {
      upstream = "organization"
      sub(/organization-cloud-run/, "organization-runtime")
    }
    /^routes:$/ { upstream = "" }

    upstream == "identity" && /^    name: identity-cloud-run$/ {
      sub(/identity-cloud-run/, "identity-runtime")
    }
    upstream == "organization" && /^    name: organization-cloud-run$/ {
      sub(/organization-cloud-run/, "organization-runtime")
    }
    upstream == "identity" && /^    scheme: https$/ {
      print "    scheme: \"${{IDENTITY_SERVICE_SCHEME}}\""
      next
    }
    upstream == "organization" && /^    scheme: https$/ {
      print "    scheme: \"${{ORGANIZATION_SERVICE_SCHEME}}\""
      next
    }
    upstream == "identity" && /^        port: 443$/ {
      print "        port: ${{IDENTITY_SERVICE_PORT}}"
      next
    }
    upstream == "organization" && /^        port: 443$/ {
      print "        port: ${{ORGANIZATION_SERVICE_PORT}}"
      next
    }

    /^      google-cloud-run-auth:$/ {
      if ((getline next_line) <= 0 || next_line !~ /^        audience: /) {
        print "bridgeworks-apisix: malformed Cloud Run auth block" > "/dev/stderr"
        exit 2
      }
      next
    }

    {
      gsub(/identity-cloud-run/, "identity-runtime")
      gsub(/organization-cloud-run/, "organization-runtime")
      print
    }
  ' "$input_file" > "$output_file" || fail "failed to generate Render APISIX profile"
}

main() {
  [ "$#" -eq 3 ] || fail "usage: runtime-config.sh <runtime-target> <profile-dir> <output-dir>"

  target="$1"
  profile_dir="$2"
  output_dir="$3"
  canonical_apisix="$profile_dir/apisix.cloud-run.yaml"

  require_non_empty RUNTIME_TARGET "$target"
  require_non_empty CLERK_AUTHORIZED_PARTIES "${CLERK_AUTHORIZED_PARTIES:-}"

  case "$target" in
    render)
      validate_host IDENTITY_SERVICE_HOST "${IDENTITY_SERVICE_HOST:-}"
      validate_port IDENTITY_SERVICE_PORT "${IDENTITY_SERVICE_PORT:-}"
      validate_scheme IDENTITY_SERVICE_SCHEME "${IDENTITY_SERVICE_SCHEME:-}"
      validate_host ORGANIZATION_SERVICE_HOST "${ORGANIZATION_SERVICE_HOST:-}"
      validate_port ORGANIZATION_SERVICE_PORT "${ORGANIZATION_SERVICE_PORT:-}"
      validate_scheme ORGANIZATION_SERVICE_SCHEME "${ORGANIZATION_SERVICE_SCHEME:-}"
      ;;
    cloud-run)
      validate_run_app_host IDENTITY_SERVICE_HOST "${IDENTITY_SERVICE_HOST:-}"
      validate_run_app_audience IDENTITY_SERVICE_AUDIENCE "${IDENTITY_SERVICE_AUDIENCE:-}"
      validate_run_app_host ORGANIZATION_SERVICE_HOST "${ORGANIZATION_SERVICE_HOST:-}"
      validate_run_app_audience ORGANIZATION_SERVICE_AUDIENCE "${ORGANIZATION_SERVICE_AUDIENCE:-}"
      ;;
    *) fail "RUNTIME_TARGET must be one of render, cloud-run" ;;
  esac

  [ -d "$profile_dir" ] || fail "runtime profile directory does not exist"
  [ -d "$output_dir" ] || fail "APISIX output directory does not exist"
  [ -r "$profile_dir/config.$target.yaml" ] || fail "runtime config profile is missing for $target"
  [ -r "$canonical_apisix" ] || fail "canonical APISIX profile is missing"

  cat "$profile_dir/config.$target.yaml" > "$output_dir/config.yaml"
  case "$target" in
    cloud-run) cat "$canonical_apisix" > "$output_dir/apisix.yaml" ;;
    render) render_apisix_profile "$canonical_apisix" "$output_dir/apisix.yaml" ;;
  esac
}

main "$@"
