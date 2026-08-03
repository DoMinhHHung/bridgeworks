SHELL := /usr/bin/env bash

.PHONY: repo-check gateway-up gateway-down gateway-restart gateway-logs stack-up stack-down stack-restart stack-logs gateway-smoke ci

repo-check:
	@test -f compose.yaml
	@test -f gateway/apisix/conf/config.yaml
	@test -f gateway/apisix/conf/apisix.yaml
	@test -x gateway/apisix/scripts/smoke-test.sh
	@test -f service/identity-service/go.mod
	@test -f service/identity-service/Dockerfile
	@test -f service/identity-service/migrations/000001_create_app_users.sql
	@test "$$(tail -n 1 gateway/apisix/conf/apisix.yaml)" = "#END"
	@docker compose config --quiet
	@echo "Repository checks passed."

gateway-up:
	docker compose up -d apisix

gateway-down:
	docker compose stop apisix

gateway-restart:
	docker compose restart apisix

gateway-logs:
	docker compose logs --follow --tail=200 apisix

stack-up:
	docker compose up -d --build

stack-down:
	docker compose down --remove-orphans

stack-restart:
	docker compose restart apisix identity-service

stack-logs:
	docker compose logs --follow --tail=200 apisix identity-service

gateway-smoke:
	./gateway/apisix/scripts/smoke-test.sh

ci: repo-check stack-up gateway-smoke
