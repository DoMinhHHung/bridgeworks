local core = require("apisix.core")
local http = require("resty.http")
local json = require("cjson.safe")

local plugin_name = "google-cloud-run-auth"
local header_name = "X-Serverless-Authorization"
local metadata_endpoint =
    "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/identity"

local token_cache = {}

local schema = {
    type = "object",
    properties = {
        audience = {
            type = "string",
            minLength = 1,
            maxLength = 2048,
        },
        timeout_ms = {
            type = "integer",
            minimum = 100,
            maximum = 5000,
            default = 1000,
        },
        refresh_skew_seconds = {
            type = "integer",
            minimum = 30,
            maximum = 600,
            default = 300,
        },
    },
    required = {"audience"},
    additionalProperties = false,
}

local _M = {
    version = 0.1,
    priority = 100,
    name = plugin_name,
    schema = schema,
}

local function trim(value)
    return value and value:match("^%s*(.-)%s*$") or nil
end

local function decode_base64url(value)
    if type(value) ~= "string" or value == "" then
        return nil, "base64url value is empty"
    end

    local normalized = value:gsub("-", "+"):gsub("_", "/")
    local remainder = #normalized % 4

    if remainder == 2 then
        normalized = normalized .. "=="
    elseif remainder == 3 then
        normalized = normalized .. "="
    elseif remainder == 1 then
        return nil, "base64url value has invalid length"
    end

    local decoded = ngx.decode_base64(normalized)
    if not decoded then
        return nil, "base64url value cannot be decoded"
    end

    return decoded
end

local function token_expiration(token)
    if type(token) ~= "string" then
        return nil, "identity token is not a string"
    end

    local encoded_claims = token:match("^[^.]+%.([^.]+)%.[^.]+$")
    if not encoded_claims then
        return nil, "identity token is not a JWT"
    end

    local decoded_claims, decode_error = decode_base64url(encoded_claims)
    if not decoded_claims then
        return nil, decode_error
    end

    local claims, json_error = json.decode(decoded_claims)
    if not claims then
        return nil, "identity token claims are invalid JSON: " .. tostring(json_error)
    end

    local expiration = tonumber(claims.exp)
    if not expiration then
        return nil, "identity token has no numeric exp claim"
    end

    return expiration
end

local function fetch_identity_token(conf)
    local timeout_ms = conf.timeout_ms or 1000
    local request_url = metadata_endpoint
        .. "?audience="
        .. ngx.escape_uri(conf.audience)

    local client = http.new()
    client:set_timeout(timeout_ms)

    local response, request_error = client:request_uri(request_url, {
        method = "GET",
        headers = {
            ["Metadata-Flavor"] = "Google",
        },
        keepalive = true,
        keepalive_timeout = 60000,
        keepalive_pool = 5,
    })

    if not response then
        return nil, nil, "metadata request failed: " .. tostring(request_error)
    end

    if response.status ~= 200 then
        return nil, nil, "metadata request returned HTTP " .. tostring(response.status)
    end

    local token = trim(response.body)
    if not token or token == "" then
        return nil, nil, "metadata response returned an empty identity token"
    end

    local expiration, expiration_error = token_expiration(token)
    if not expiration then
        return nil, nil, expiration_error
    end

    local now = ngx.time()
    if expiration <= now + 30 then
        return nil, nil, "metadata response returned an identity token near expiration"
    end

    return token, expiration
end

local function cached_identity_token(conf)
    local now = ngx.time()
    local refresh_skew_seconds = conf.refresh_skew_seconds or 300
    local cached = token_cache[conf.audience]

    if cached and cached.expiration - refresh_skew_seconds > now then
        return cached.token
    end

    local token, expiration, fetch_error = fetch_identity_token(conf)
    if token then
        token_cache[conf.audience] = {
            token = token,
            expiration = expiration,
        }
        return token
    end

    if cached and cached.expiration > now + 15 then
        core.log.warn(
            "failed to refresh Google Cloud Run identity token; using cached token",
            ", audience=", conf.audience,
            ", error=", fetch_error
        )
        return cached.token
    end

    return nil, fetch_error
end

function _M.check_schema(conf)
    local ok, schema_error = core.schema.check(schema, conf)
    if not ok then
        return false, schema_error
    end

    if not conf.audience:match("^https://[A-Za-z0-9.-]+%.run%.app$") then
        return false, "audience must be an HTTPS Cloud Run run.app origin without a path"
    end

    return true
end

function _M.access(conf, ctx)
    core.request.set_header(ctx, header_name, nil)

    local token, token_error = cached_identity_token(conf)
    if not token then
        core.log.error(
            "failed to acquire Google Cloud Run identity token",
            ", audience=", conf.audience,
            ", error=", token_error
        )

        return 503, {
            error = {
                code = "gateway_upstream_auth_unavailable",
                message = "gateway cannot authenticate the upstream service",
            },
        }
    end

    core.request.set_header(ctx, header_name, "Bearer " .. token)
end

_M._test = {
    decode_base64url = decode_base64url,
    token_expiration = token_expiration,
    reset_cache = function()
        token_cache = {}
    end,
}

return _M
