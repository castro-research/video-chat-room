DB_URL    = postgres://kurz:kurz@localhost:5433/kurz?sslmode=disable
ADMIN_URL = postgres://kurz:kurz@localhost:5433/postgres?sslmode=disable
S3_ENDPOINT = localhost:8333

.PHONY: db-up db-down s3-up s3-down test test-short run bench-search loadtest image kind-up kind-down kind-loadtest-mode kind-loadtest kind-kill

db-up:
	docker compose up -d --wait db

db-down:
	docker compose down

# SeaweedFS (S3 em :8333, chave kurzdev / kurzdev-secret definida em deploy/seaweedfs/s3.json)
s3-up:
	docker compose up -d seaweedfs
	@for i in $$(seq 1 40); do curl -sf http://$(S3_ENDPOINT)/healthz >/dev/null && echo "seaweedfs ok" && exit 0; sleep 0.5; done; echo "seaweedfs não subiu" >&2; exit 1

s3-down:
	docker compose rm -sf seaweedfs

# Sobe o SeaweedFS se der (os testes de S3 real rodam com ele; sem ele, usam o fake e os de S3 real dão SKIP explícito).
test:
	-@$(MAKE) --no-print-directory s3-up
	KURZ_TEST_DATABASE_URL='$(ADMIN_URL)' \
	KURZ_TEST_S3_ENDPOINT=$$(curl -sf http://$(S3_ENDPOINT)/healthz >/dev/null && echo $(S3_ENDPOINT)) \
	go test ./... -count=1

# Sem testes pesados (busca com volume) nem depender do SeaweedFS.
test-short:
	KURZ_TEST_DATABASE_URL='$(ADMIN_URL)' go test ./... -count=1 -short

# Mede o p95 da busca em volume (KURZ_BENCH_MESSAGES, padrão 1000000).
bench-search:
	KURZ_TEST_DATABASE_URL='$(ADMIN_URL)' KURZ_BENCH_SEARCH=1 go test ./internal/search -run TestSearchVolume -count=1 -v -timeout 30m

run:
	KURZ_DATABASE_URL='$(DB_URL)' KURZ_JWT_SECRET='dev-secret-dev-secret-dev-secret-32' KURZ_S3_ENDPOINT='$(S3_ENDPOINT)' go run ./cmd/kurz

# Carga contra um servidor já de pé (KURZ_RATELIMIT=off): URL=... USERS=... RATE=... DURATION=...
URL ?= http://localhost:8080
USERS ?= 20
RATE ?= 20
DURATION ?= 30s
loadtest:
	go run ./loadtest --url $(URL) --users $(USERS) --rate $(RATE) --duration $(DURATION)

# ---- Kubernetes local (kind) ----
KIND_CLUSTER = kurz
KCTL = kubectl --context kind-$(KIND_CLUSTER)
KIND_URL = http://localhost:28080

image:
	docker build -t kurz/server:dev .

# Sobe tudo: cluster kind, imagem, Postgres, SeaweedFS e 2 réplicas do monólito. API em http://localhost:28080, S3 em :29000.
kind-up: image
	@kind get clusters | grep -qx $(KIND_CLUSTER) || kind create cluster --config deploy/kind.yaml
	kind load docker-image kurz/server:dev --name $(KIND_CLUSTER)
	$(KCTL) apply -k deploy/k8s/overlays/kind
	$(KCTL) -n kurz rollout restart statefulset/kurz
	$(KCTL) -n kurz rollout status statefulset/postgres --timeout=180s
	$(KCTL) -n kurz rollout status deployment/seaweedfs --timeout=180s
	$(KCTL) -n kurz rollout status statefulset/kurz --timeout=180s
	@curl -sf $(KIND_URL)/healthz >/dev/null && echo "kurz ok em $(KIND_URL)"

kind-down:
	kind delete cluster --name $(KIND_CLUSTER)

# Desliga o rate limit (o gerador vem de um IP só) e espera as réplicas trocarem.
kind-loadtest-mode:
	$(KCTL) apply -k deploy/k8s/overlays/kind-loadtest
	$(KCTL) -n kurz rollout status statefulset/kurz --timeout=180s

kind-loadtest: kind-loadtest-mode
	go run ./loadtest --url $(KIND_URL) --users $(USERS) --rate $(RATE) --duration $(DURATION)

# Mata a réplica kurz-1 sem aviso (RNF8). Rode durante `make kind-loadtest` com CHAOS=1 no loadtest.
kind-kill:
	$(KCTL) -n kurz delete pod kurz-1 --grace-period=0 --force
