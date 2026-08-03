SHELL := /usr/bin/env bash

.PHONY: repo-check gateway-up gateway-down gateway-restart gateway-logs stack-up stack-down stack-restart stack-logs gateway-smoke identity-migrate-up identity-migrate-status identity-migrate-version identity-db-logs identity-db-shell ci

repo-check:
	@test -f compose.yaml
	@test -f gateway/apisix/conf/config.yaml
	@test -f gateway/apisix/conf/apisix.yaml
	@test -x gateway/apisix/scripts/smoke-test.sh
	@test -f service/identity-service/go.mod
	@test -f service/identity-service/Dockerfile
	@test -f service/identity-service/cmd/identity-migrate/main.go
	@test -f service/identity-service/migrations/000001_create_app_users.sql
	@test "$$(tail -n 1 gateway/apisix/conf/apisix.yaml)" = "#END"
	@docker compose --env-file .env.example config --quiet
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
	docker compose logs --follow --tail=200 apisix identity-service identity-postgres

gateway-smoke:
	./gateway/apisix/scripts/smoke-test.sh

identity-migrate-up:
	docker compose run --rm identity-migrate up

identity-migrate-status:
	docker compose run --rm identity-migrate status

identity-migrate-version:
	docker compose run --rm identity-migrate version

identity-db-logs:
	docker compose logs --follow --tail=200 identity-postgres

identity-db-shell:
	docker compose exec identity-postgres sh -c \
		'psql --username "$$POSTGRES_USER" --dbname "$$POSTGRES_DB"'

ci: repo-check stack-up gateway-smoke
