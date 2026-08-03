SHELL := /usr/bin/env bash

.PHONY: repo-check gateway-up gateway-down gateway-restart gateway-logs gateway-smoke ci

repo-check:
	@test -f compose.yaml
	@test -f gateway/apisix/conf/config.yaml
	@test -f gateway/apisix/conf/apisix.yaml
	@test -f gateway/apisix/scripts/smoke-test.sh
	@test "$$(tail -n 1 gateway/apisix/conf/apisix.yaml)" = "#END"
	@docker compose config --quiet
	@echo "Repository checks passed."

gateway-up:
	docker compose up -d apisix

gateway-down:
	docker compose down --remove-orphans

gateway-restart:
	docker compose restart apisix

gateway-logs:
	docker compose logs --follow --tail=200 apisix

gateway-smoke:
	./gateway/apisix/scripts/smoke-test.sh

ci: repo-check gateway-up gateway-smoke
