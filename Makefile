SHELL := /usr/bin/env bash

SQLC_IMAGE := sqlc/sqlc:1.31.1
IDENTITY_SQLC_GENERATED := service/identity-service/internal/store/sqlcgen
ORGANIZATION_SQLC_GENERATED := service/organization-service/internal/store/sqlcgen

.PHONY: repo-check gateway-up gateway-down gateway-restart gateway-logs stack-up stack-down stack-restart stack-logs gateway-smoke identity-migrate-up identity-migrate-status identity-migrate-version identity-db-logs identity-db-shell identity-sqlc-generate identity-sqlc-check organization-build organization-test organization-sqlc-generate organization-sqlc-check organization-migrate-up organization-migrate-status organization-migrate-version organization-smoke loadtest-unit loadtest-smoke capacity-budget ci

repo-check:
	@test -f compose.yaml
	@test -f gateway/apisix/conf/config.yaml
	@test -f gateway/apisix/conf/apisix.yaml
	@test -x gateway/apisix/scripts/smoke-test.sh
	@test -f gateway/apisix/scripts/organization-smoke-test.sh
	@test -f service/identity-service/go.mod
	@test -f service/identity-service/Dockerfile
	@test -f service/identity-service/cmd/identity-migrate/main.go
	@test -f service/identity-service/migrations/000001_create_app_users.sql
	@test -f service/identity-service/sqlc.yaml
	@test -f service/organization-service/go.mod
	@test -f service/organization-service/Dockerfile
	@test -f service/organization-service/cmd/organization-migrate/main.go
	@test -f service/organization-service/migrations/000001_create_organization_foundation.sql
	@test -f service/organization-service/sqlc.yaml
	@test -f loadtest/k6/authenticated-read.js
	@test -f loadtest/k6/webhook.js
	@test -x loadtest/scripts/run.sh
	@test -x loadtest/scripts/run-suite.sh
	@test -f docs/load-test-capacity-runbook.md
	@test "$$(tail -n 1 gateway/apisix/conf/apisix.yaml)" = "#END"
	@docker compose --env-file .env.example config --quiet
	@if [[ -f .env ]]; then docker compose config --quiet; fi
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
	docker compose restart apisix identity-service organization-service

stack-logs:
	docker compose logs --follow --tail=200 apisix identity-service identity-postgres organization-service organization-postgres

gateway-smoke:
	./gateway/apisix/scripts/smoke-test.sh

organization-smoke:
	bash ./gateway/apisix/scripts/organization-smoke-test.sh

identity-migrate-up:
	docker compose run --rm identity-migrate up

identity-migrate-status:
	docker compose run --rm identity-migrate status

identity-migrate-version:
	docker compose run --rm identity-migrate version

identity-db-logs:
	docker compose logs --follow --tail=200 identity-postgres

identity-db-shell:
	docker compose exec identity-postgres sh -c 'psql --username "$$POSTGRES_USER" --dbname "$$POSTGRES_DB"'

identity-sqlc-generate:
	docker run --rm --volume "$(CURDIR):/src" --workdir /src/service/identity-service $(SQLC_IMAGE) generate

identity-sqlc-check: identity-sqlc-generate
	@git diff --exit-code -- $(IDENTITY_SQLC_GENERATED)
	@test -z "$$(git ls-files --others --exclude-standard -- $(IDENTITY_SQLC_GENERATED))"

organization-build:
	cd service/organization-service && go build ./cmd/organization-service ./cmd/organization-migrate

organization-test:
	cd service/organization-service && go test -race ./...

organization-sqlc-generate:
	docker run --rm --volume "$(CURDIR):/src" --workdir /src/service/organization-service $(SQLC_IMAGE) generate

organization-sqlc-check: organization-sqlc-generate
	@git diff --exit-code -- $(ORGANIZATION_SQLC_GENERATED)
	@test -z "$$(git ls-files --others --exclude-standard -- $(ORGANIZATION_SQLC_GENERATED))"

organization-migrate-up:
	docker compose run --rm organization-migrate up

organization-migrate-status:
	docker compose run --rm organization-migrate status

organization-migrate-version:
	docker compose run --rm organization-migrate version

loadtest-unit:
	python3 -m unittest discover -s loadtest/tests -v
	python3 -m py_compile loadtest/capacity.py loadtest/report.py loadtest/scripts/delay_proxy.py
	node --check loadtest/k6/lib/common.js
	node --check loadtest/k6/authenticated-read.js
	node --check loadtest/k6/webhook.js
	bash -n loadtest/scripts/harness.sh loadtest/scripts/run.sh loadtest/scripts/run-suite.sh

loadtest-smoke:
	bash loadtest/scripts/run-suite.sh smoke

capacity-budget:
	@mkdir -p loadtest-results/capacity
	python3 loadtest/capacity.py \
		--postgres-max-connections "$${CAPACITY_POSTGRES_MAX_CONNECTIONS:-100}" \
		--reserved-admin-connections "$${CAPACITY_RESERVED_ADMIN_CONNECTIONS:-5}" \
		--reserved-migration-connections "$${CAPACITY_RESERVED_MIGRATION_CONNECTIONS:-5}" \
		--operational-headroom-connections "$${CAPACITY_OPERATIONAL_HEADROOM_CONNECTIONS:-20}" \
		--identity-allocation "$${CAPACITY_IDENTITY_ALLOCATION:-30}" \
		--identity-max-replicas "$${CAPACITY_IDENTITY_MAX_REPLICAS:-3}" \
		--organization-allocation "$${CAPACITY_ORGANIZATION_ALLOCATION:-30}" \
		--organization-max-replicas "$${CAPACITY_ORGANIZATION_MAX_REPLICAS:-3}" \
		--output-json loadtest-results/capacity/capacity-budget.json \
		--output-markdown loadtest-results/capacity/capacity-budget.md

ci: repo-check identity-sqlc-check organization-sqlc-check stack-up gateway-smoke organization-smoke
