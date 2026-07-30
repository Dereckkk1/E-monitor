---
status: planejado
ultima-verificacao: 2026-07-30
codigo-relacionado:
  - workers/internal/evidence/service.go
  - workers/internal/evidence/cofire_guard.go
  - workers/internal/evidence/disambig_coverage.go
  - infra/docker/docker-compose.yml
---

# Rollout: DISAMBIG_BY_COVERAGE=true em prod

Contexto: [incident-2026-07-24-pulso-milium-nao-detectado.md](../incidents/incident-2026-07-24-pulso-milium-nao-detectado.md)
(§4d/§4e = validação E2E). A flag liga a arbitragem §18.2.2-v2 por cobertura de evidência
(pass-path + co-fire guard + reject-path). Em 30/06 ela rodou SEM o co-fire guard e causou
duplicatas — o guard corrige exatamente aquilo, e o E2E re-exercitou o cenário.

> **NÃO confundir com `DISAMBIG_CONFIDENCE_AWARE`** — essa foi testada e **REPROVADA**
> (§4d: conserta o pulso standalone mas rouba 4 de 6 tocadas reais do spot). Fica default
> `false` pra sempre até haver arbitragem confiável no supervisor.

## Pré-condições

- [ ] Deploy atual do api inclui o **co-fire guard** (`7e1cc83`, 30/06). Conferir na VM:
      `git merge-base --is-ancestor 7e1cc83 HEAD && echo "guard presente"`
      (o hash `4b2e112` citado em rascunhos anteriores é outro commit — reload de índice.)
- [ ] Métrica `suppressed_suspect` + alerta `DedupSuppressedSuspect` deployados
      (Tasks 2-3 deste fix — conferir `curl -s localhost:8080/metrics | grep suppressed_suspect`
      responde a série depois da primeira supressão, e `promtool check rules` passa)
- [ ] Janela de observação de 48h combinada (dias úteis)

## Ligar (VM — executa o Dereck)

```bash
cd ~/radiocheck && git pull
# .env: adicionar/editar
#   DISAMBIG_BY_COVERAGE=true
DC="docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env"
$DC build api && $DC up -d --force-recreate --no-deps api    # --no-deps OBRIGATÓRIO (CLAUDE.md 4.1)
docker logs docker-api-1 2>&1 | grep "coverage-based disambiguation ENABLED"   # tem que aparecer
```

Se a linha de log NÃO aparecer, a env não chegou no container — confira o passthrough em
`infra/docker/docker-compose.yml` (armadilha do `SHARING_MIN_SCORE`, incidente 2026-06-15)
antes de concluir qualquer coisa da sombra.

## Sombra 48h — o que olhar (1x/dia)

```bash
curl -s localhost:8080/metrics | grep radiocheck_match_disambiguation_total
# esperado: reattributed_by_coverage / duplicate_cofire_retracted / restored_on_reject > 0
# em dias com tocada standalone de material curto; suppressed_suspect estável ou caindo.
```

```sql
-- rows re-arbitradas nas últimas 24h (deve conter os resgates do pulso):
SELECT to_char(d.detected_at AT TIME ZONE 'America/Sao_Paulo','DD/MM HH24:MI') AS quando,
       s.name, m.short_id, m.title, d.evidence_status,
       (d.retracted_at IS NOT NULL) AS retratada, d.audit_coverage
FROM detections d JOIN stations s ON s.id=d.station_id
LEFT JOIN materials m ON m.id=d.commercial_id
WHERE d.detected_at > now() - interval '24 hours'
  AND m.short_id IN (<short_id do PULSO>, <short_id do SPOT DEMAIS RADIOS>)
ORDER BY d.detected_at DESC;
```

Também vale olhar o custo: a v2 re-audita os irmãos de cada row aprovada (~0.7-1.2s por audit,
medido). Monitorar `radiocheck_audit_duration_seconds` — se o p95 subir muito num dia de pico,
é o sinal de que o catálogo do cliente tem irmãos demais (spec §5).

Critérios de aceite da sombra: (1) pulso passa a ter rows contando nas 2 emissoras
(meninablu, loopert/303) nos dias com tocada; (2) contagem do SPOT não cai nos dias/emissoras
em que ele tocou de verdade (comparar com semana anterior); (3) zero duplicata
pulso+spot no mesmo minuto/emissora contando juntas.

**Edge conhecido a vigiar** (incident §4e): rotação absurda do material curto (>3 tocadas em
2min na mesma janela de evidência) infla a cobertura AGREGADA do spot no clipe e devolve o
quase-empate pra regra de duração. Em rádio real (1 pulso por break) não acontece; se
aparecer na sombra, é caso de recalibrar `coverageMargin`.

## Rollback (instantâneo)

```bash
# .env: DISAMBIG_BY_COVERAGE=false
$DC up -d --force-recreate --no-deps api
```

Rollback não desfaz reatribuições já gravadas — ele só para de fazer novas. Se precisar
reverter dado, use o diagnóstico do histórico (abaixo) e decida caso a caso.

## Depois do aceite

- Mover este doc pra `status: implementado` com a data.
- Avaliar o backfill histórico (`scripts/sql/diagnose-pulso-subset-rows.sql`) com o dono.
- Reavaliar o F-122 (supressão v1 sem row do vencedor pra resgatar) com o que a sombra mostrou.
