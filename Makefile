.PHONY: dev gen migrate-up migrate-down test

dev:
	docker compose up -d
	cd backend && go run ./cmd/app serve

gen:
	oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml
	cd backend && sqlc generate
	cd frontend && npm run gen

migrate-up:
	cd backend && migrate -path db/migrations -database "$${DATABASE_URL}" up

migrate-down:
	cd backend && migrate -path db/migrations -database "$${DATABASE_URL}" down

test:
	cd backend && go test ./...
