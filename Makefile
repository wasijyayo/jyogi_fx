.PHONY: dev gen migrate-up migrate-down test

dev:
	docker compose up -d
	cd backend && go run ./cmd/app serve

# TODO: oapi-codegen (Go) と orval (TS) は openapi.yaml 導入時に追加する。
gen:
	cd backend && sqlc generate

migrate-up:
	cd backend && migrate -path db/migrations -database "$${DATABASE_URL}" up

migrate-down:
	cd backend && migrate -path db/migrations -database "$${DATABASE_URL}" down

test:
	cd backend && go test ./...
