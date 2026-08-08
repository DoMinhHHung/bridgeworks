# syntax=docker/dockerfile:1.7

ARG APISIX_BASE_IMAGE=apache/apisix:3.17.0-debian@sha256:0e5377839f4ff5e322a5686ab6ce6797ba768008aca1bfc9b71149c3b326c4df
FROM ${APISIX_BASE_IMAGE}

USER root

COPY --chown=apisix:0 conf/config.cloud-run.yaml /usr/local/apisix/bridgeworks/conf/config.cloud-run.yaml
COPY --chown=apisix:0 conf/config.render.yaml /usr/local/apisix/bridgeworks/conf/config.render.yaml
COPY --chown=apisix:0 conf/upstreams.cloud-run.yaml /usr/local/apisix/bridgeworks/conf/upstreams.cloud-run.yaml
COPY --chown=apisix:0 conf/upstreams.render.yaml /usr/local/apisix/bridgeworks/conf/upstreams.render.yaml
COPY --chown=apisix:0 conf/routes.runtime.yaml /usr/local/apisix/bridgeworks/conf/routes.runtime.yaml
COPY --chown=apisix:0 custom/ /usr/local/apisix/custom/
COPY --chown=apisix:0 scripts/runtime-config.sh /usr/local/bin/bridgeworks-apisix-runtime-config
COPY --chown=apisix:0 scripts/runtime-entrypoint.sh /usr/local/bin/bridgeworks-apisix-entrypoint

RUN chmod 0555 \
      /usr/local/bin/bridgeworks-apisix-runtime-config \
      /usr/local/bin/bridgeworks-apisix-entrypoint \
    && chmod 0444 /usr/local/apisix/bridgeworks/conf/*.yaml \
    && find /usr/local/apisix/custom -type d -exec chmod 0555 {} \; \
    && find /usr/local/apisix/custom -type f -exec chmod 0444 {} \; \
    && : > /usr/local/apisix/conf/config.yaml \
    && : > /usr/local/apisix/conf/apisix.yaml \
    && chown apisix:0 /usr/local/apisix/conf/config.yaml /usr/local/apisix/conf/apisix.yaml \
    && chmod 0600 /usr/local/apisix/conf/config.yaml /usr/local/apisix/conf/apisix.yaml

USER apisix

EXPOSE 9080

ENTRYPOINT ["/usr/local/bin/bridgeworks-apisix-entrypoint"]
