.PHONY: dev gen build migrate-up migrate-down test

dev:
	docker compose up -d
	cd backend && go run ./cmd/app serve

gen:
	oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml
	cd backend && sqlc generate
	cd frontend && npm run gen

# CLAUDE.md §3: フロントエンドをビルドして backend/web/ に置いてから、
# Go の embed で単一バイナリに固める（WS-8）。
build:
	cd frontend && npm run build
	cd backend && go build -o ../bin/app ./cmd/app

migrate-up:
	cd backend && migrate -path db/migrations -database "$${DATABASE_URL}" up

migrate-down:
	cd backend && migrate -path db/migrations -database "$${DATABASE_URL}" down

test:
	cd backend && go test ./...
