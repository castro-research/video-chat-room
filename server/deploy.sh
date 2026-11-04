#!/bin/sh
# Constrói a imagem e sobe o monólito Kurz (+ Postgres 18 + SeaweedFS) como stack "kurzserver" no Docker Swarm (nó único).
# Rode NO nó do swarm (ex.: pizito): o build usa o Docker local.
#
# Variáveis opcionais (o padrão serve para a LAN):
#   PUBLIC_IP               IP pelo qual o CLIENTE alcança o nó (padrão: IP da LAN)
#   KURZ_SFU_URL            ws(s):// do /v1/media do SFU; atrás de um proxy/túnel: wss://<host>/v1/media (padrão: ws://$PUBLIC_IP:30081/v1/media)
#   KURZ_S3_PUBLIC_ENDPOINT host:porta do S3 como o CLIENTE alcança (padrão: $PUBLIC_IP:30083)
#   KURZ_TRUSTED_PROXY      true só se todo acesso passar por um proxy que sobrescreve o X-Forwarded-For
set -eu
cd "$(dirname "$0")"

PUBLIC_IP="${PUBLIC_IP:-$(ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null \
  || ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p' || true)}"
[ -n "$PUBLIC_IP" ] || { echo "sem IP de LAN: defina PUBLIC_IP" >&2; exit 1; }
KURZ_SFU_URL="${KURZ_SFU_URL:-ws://$PUBLIC_IP:30081/v1/media}"
KURZ_S3_PUBLIC_ENDPOINT="${KURZ_S3_PUBLIC_ENDPOINT:-$PUBLIC_IP:30083}"
KURZ_IMAGE="${KURZ_IMAGE:-kurz/server:latest}"
export KURZ_SFU_URL KURZ_S3_PUBLIC_ENDPOINT KURZ_IMAGE

[ "$(docker info --format '{{.Swarm.LocalNodeState}}')" = active ] || docker swarm init

# Os segredos nascem uma vez (.env está no .gitignore). Trocar POSTGRES_PASSWORD depois não muda a senha do banco já criado.
if [ ! -f .env ]; then
  umask 077
  {
    echo "POSTGRES_PASSWORD=$(openssl rand -hex 16)"
    echo "KURZ_JWT_SECRET=$(openssl rand -hex 32)"
    echo "S3_ACCESS_KEY=kurz$(openssl rand -hex 4)"
    echo "S3_SECRET_KEY=$(openssl rand -hex 24)"
  } > .env
fi
# KURZ_SECRET tem de ser o mesmo do SFU, que já o guarda em experimentos/.env.
if ! grep -q '^KURZ_SECRET=' .env; then
  grep '^KURZ_SECRET=' ../experimentos/.env >> .env 2>/dev/null \
    || { echo "KURZ_SECRET ausente: copie a linha de experimentos/.env (o segredo do SFU) para server/.env" >&2; exit 1; }
fi
set -a; . ./.env; set +a

# credenciais do S3 para o SeaweedFS (identity com Admin: o servidor cria o bucket na subida)
umask 077
cat > .s3.json <<EOF
{"identities":[{"name":"kurz","credentials":[{"accessKey":"$S3_ACCESS_KEY","secretKey":"$S3_SECRET_KEY"}],"actions":["Admin","Read","Write","List","Tagging"]}]}
EOF
S3_CONFIG_NAME="kurz_s3_$(cksum < .s3.json | cut -d' ' -f1)"
export S3_CONFIG_NAME

docker build -t "$KURZ_IMAGE" .

docker stack deploy --resolve-image never -c stack.yml kurzserver
# :latest reconstruída não muda o spec, então força a task a recriar
docker service update --force --quiet kurzserver_kurz >/dev/null
docker stack services kurzserver
echo "api: http://$PUBLIC_IP:30082   s3: http://$KURZ_S3_PUBLIC_ENDPOINT   sfu ws: $KURZ_SFU_URL"
