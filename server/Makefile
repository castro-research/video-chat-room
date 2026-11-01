DB_URL    = postgres://kurz:kurz@localhost:5433/kurz?sslmode=disable
ADMIN_URL = postgres://kurz:kurz@localhost:5433/postgres?sslmode=disable

.PHONY: db-up db-down test run

db-up:
	docker compose up -d --wait db

db-down:
	docker compose down

test:
	KURZ_TEST_DATABASE_URL='$(ADMIN_URL)' go test ./... -count=1

run:
	KURZ_DATABASE_URL='$(DB_URL)' KURZ_JWT_SECRET='dev-secret-dev-secret-dev-secret-32' go run ./cmd/kurz
