.PHONY: run-sfu run-api run-web genkey db-up db-down fmt vet test

# Muat .env jika ada.
ifneq (,$(wildcard .env))
include .env
export
endif

run-sfu:
	go run ./apps/sfu

run-api:
	go run ./apps/api

run-web:
	cd ../kamscode-meet-web && npm run dev

genkey:
	go run ./apps/api -genkey

db-up:
	docker compose -f infra/docker-compose.yml up -d

db-down:
	docker compose -f infra/docker-compose.yml down

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test -race ./...
