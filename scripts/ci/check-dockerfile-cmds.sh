#!/usr/bin/env bash
# CLAUDE.md regra 6.7 — CLI novo em workers/cmd/* NÃO entra na imagem sozinho:
# o workers.Dockerfile builda e copia cada binário explicitamente. Esquecer as
# duas linhas passa no `go build ./...` e só quebra em prod, no
# `docker compose exec api <novo>` ("not found").
#
# Este check falha se algum cmd/<nome> não tiver o `go build -o /out/<nome>`
# E o `COPY --from=builder /out/<nome>` no Dockerfile — a não ser que esteja na
# lista FORA_DA_IMAGEM abaixo, com o motivo.
set -euo pipefail
cd "$(dirname "$0")/../.."

DOCKERFILE=infra/docker/Dockerfiles/workers.Dockerfile

# cmd/* que NÃO vão pra imagem de propósito (motivo obrigatório):
#   fingerprint   — CLI de dev: gera hashes de um master local; em prod quem faz
#                   isso é o serviço `fingerprint` (Python) via NATS
#   measure-quota — harness DESCARTÁVEL de medição da categorização por cota
FORA_DA_IMAGEM=" fingerprint measure-quota "

falhas=0
for dir in workers/cmd/*/; do
  nome="$(basename "$dir")"
  case "$FORA_DA_IMAGEM" in *" $nome "*) continue ;; esac
  if ! grep -qE "go build .*-o /out/${nome} \./cmd/${nome}([[:space:]]|$)" "$DOCKERFILE"; then
    echo "✗ cmd/${nome}: falta 'RUN ... go build -o /out/${nome} ./cmd/${nome}' em $DOCKERFILE"
    falhas=$((falhas + 1))
  fi
  if ! grep -qE "COPY --from=builder /out/${nome}[[:space:]]" "$DOCKERFILE"; then
    echo "✗ cmd/${nome}: falta 'COPY --from=builder /out/${nome} /usr/local/bin/${nome}' em $DOCKERFILE"
    falhas=$((falhas + 1))
  fi
done

if [ "$falhas" -gt 0 ]; then
  echo
  echo "Adicione as linhas (CLAUDE.md 6.7) ou, se o CLI for só de dev, coloque-o em"
  echo "FORA_DA_IMAGEM neste script com o motivo."
  exit 1
fi
echo "ok: todo workers/cmd/* está no $DOCKERFILE (exceto: $FORA_DA_IMAGEM)"
