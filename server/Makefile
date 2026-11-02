DB_URL    = postgres://kurz:kurz@localhost:5433/kurz?sslmode=disable
ADMIN_URL = postgres://kurz:kurz@localhost:5433/postgres?sslmode=disable
S3_ENDPOINT = localhost:9000

.PHONY: db-up db-down s3-up s3-down test test-short run bench-search

db-up:
	docker compose up -d --wait db

db-down:
	docker compose down

# MinIO (API :9000, console :9001, usuário kurzdev / kurzdev-secret)
s3-up:
	docker compose up -d minio
	@for i in $$(seq 1 40); do curl -sf http://$(S3_ENDPOINT)/minio/health/live >/dev/null && echo "minio ok" && exit 0; sleep 0.5; done; echo "minio não subiu" >&2; exit 1

s3-down:
	docker compose rm -sf minio

# Sobe o MinIO se der (os testes de S3 real rodam com ele; sem ele, usam o fake e os de S3 real dão SKIP explícito).
test:
	-@$(MAKE) --no-print-directory s3-up
	KURZ_TEST_DATABASE_URL='$(ADMIN_URL)' \
	KURZ_TEST_S3_ENDPOINT=$$(curl -sf http://$(S3_ENDPOINT)/minio/health/live >/dev/null && echo $(S3_ENDPOINT)) \
	go test ./... -count=1

# Sem testes pesados (busca com volume) nem depender de MinIO.
test-short:
	KURZ_TEST_DATABASE_URL='$(ADMIN_URL)' go test ./... -count=1 -short

# Mede o p95 da busca em volume (KURZ_BENCH_MESSAGES, padrão 1000000).
bench-search:
	KURZ_TEST_DATABASE_URL='$(ADMIN_URL)' KURZ_BENCH_SEARCH=1 go test ./internal/search -run TestSearchVolume -count=1 -v -timeout 30m

run:
	KURZ_DATABASE_URL='$(DB_URL)' KURZ_JWT_SECRET='dev-secret-dev-secret-dev-secret-32' KURZ_S3_ENDPOINT='$(S3_ENDPOINT)' go run ./cmd/kurz
