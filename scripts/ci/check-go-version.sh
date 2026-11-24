#!/usr/bin/env bash
# CLAUDE.md regra 6.2 — a diretiva `go` do workers/go.mod tem que ser <= a
# major.minor da imagem do builder (golang:X.Y-alpine no workers.Dockerfile).
# Senão o build da imagem quebra em prod com "go.mod requires go >= X".
set -euo pipefail
cd "$(dirname "$0")/../.."

mod="$(awk '/^go [0-9]/ {print $2; exit}' workers/go.mod)"
img="$(grep -oE 'golang:[0-9]+\.[0-9]+' infra/docker/Dockerfiles/workers.Dockerfile | head -1 | cut -d: -f2)"
[ -n "$mod" ] && [ -n "$img" ] || { echo "✗ não achei a versão (go.mod='$mod', imagem='$img')"; exit 1; }

mod_mm="$(echo "$mod" | cut -d. -f1,2)"
# sort -V: se a menor das duas não for a do go.mod, o go.mod pede mais que a imagem tem
menor="$(printf '%s\n%s\n' "$mod_mm" "$img" | sort -V | head -1)"
if [ "$menor" != "$mod_mm" ]; then
  echo "✗ workers/go.mod pede go $mod, mas o builder é golang:$img — suba a imagem no workers.Dockerfile"
  exit 1
fi
echo "ok: go.mod (go $mod) <= imagem do builder (golang:$img)"
