#!/bin/sh
# Constrói as imagens e sobe sfu + manager como stack "videochat" no Docker Swarm local (nó único).
# PUBLIC_IP = IP pelo qual o CLIENTE alcança o nó. Padrão: IP da LAN. Não serve 127.0.0.1: o libwebrtc do cliente não
# forma par ICE entre o seu candidato da LAN e um candidato remoto em loopback (testado: falha em 127.0.0.1, conecta na LAN).
set -eu
cd "$(dirname "$0")"
# SFU_PUBLIC_WS_URL = URL do WebSocket do SFU que o manager entrega ao cliente. Padrão: direto no nó, porta 30081.
# Atrás do túnel (cord.kurz.fyi): SFU_PUBLIC_WS_URL=wss://cord.kurz.fyi/v1/media ./deploy.sh
PUBLIC_IP="${PUBLIC_IP:-$(ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null \
  || ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p' || true)}"
[ -n "$PUBLIC_IP" ] || { echo "sem IP de LAN: defina PUBLIC_IP" >&2; exit 1; }
SFU_PUBLIC_WS_URL="${SFU_PUBLIC_WS_URL:-ws://$PUBLIC_IP:30081/v1/media}"
export PUBLIC_IP SFU_PUBLIC_WS_URL

[ "$(docker info --format '{{.Swarm.LocalNodeState}}')" = active ] || docker swarm init
# o segredo nasce uma vez (.env está no .gitignore); trocar só invalida tokens em voo
[ -f .env ] || { umask 077; echo "KURZ_SECRET=$(openssl rand -hex 32)" > .env; }
set -a; . ./.env; set +a

docker build -t kurz/sfu:latest sfu
docker build -t kurz/manager:latest manager

docker stack deploy --resolve-image never -c stack.yml videochat
# :latest reconstruída não muda o spec, então força as tasks a recriar
for s in sfu manager; do docker service update --force --quiet "videochat_$s" >/dev/null; done
docker stack services videochat
echo "manager: http://$PUBLIC_IP:30080   sfu ws: $SFU_PUBLIC_WS_URL   sfu udp: $PUBLIC_IP:30100"
