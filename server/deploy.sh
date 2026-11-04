#!/bin/sh
set -eu
cd "$(dirname "$0")"

set -a; . ./.env; set +a

docker build -t "$KURZ_IMAGE" .
docker stack deploy --resolve-image never -c stack.yml concordia
