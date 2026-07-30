---
status: implementado
ultima-verificacao: 2026-07-30
codigo-relacionado:
  - workers/internal/supervisor/disambiguation.go
  - migrations/0049_dedup_suppressions.up.sql
  - infra/prometheus/alerts.yml
---

# DedupSuppressedSuspect

**Severidade:** warning · **Categoria:** Detecção
**Métrica:** `radiocheck_match_disambiguation_total{action="suppressed_suspect"}` (qualquer ocorrência em 1h)
**Origem:** incidente [2026-07-24](../incidents/incident-2026-07-24-pulso-milium-nao-detectado.md) — PULSO SONORO MILIUM (5.7s) zerado em 2 de 8 emissoras.

## Sintomas

- O dedup §18.2.2 suprimiu uma detecção **mais confiante** do que a que manteve
  (`suppressed_confidence >= kept_confidence + 0.25`).
- Assinatura de **veiculação real morta por um false-confirm** de trecho compartilhado: o
  caso canônico é material <10s cujo áudio está contido num spot ≥10s do MESMO cliente —
  o curto toca sozinho, o spot false-confirma de carona a conf ~0.2, e a regra v1 por
  duração mata o curto.
- A supressão **não deixa row em `detections`** — para o cliente a veiculação simplesmente
  não existe. A única forense é `dedup_suppressions` + esta métrica.

## Causas Comuns

1. **Par curto⊂longo do mesmo cliente** (canônico). `sharing.go` pula o flagging de
   shared-hash pra material <10s dos dois lados (`MinShareableDurationSeconds=10`), então
   nada impede o co-fire. Ver [version-disambiguation.md](../architecture/version-disambiguation.md).
2. **Par 15s⊂30s/30s⊂60s com o corte curto tocando** — mesmo mecanismo, versão já conhecida
   (incidente 2026-06-16/17).
3. **Gêmeos acústicos** (mesma duração, cortes quase idênticos) — cai no tie-break por
   short_id; aqui a confiança raramente abre 0.25 de gap, então é causa improvável para este alerta.

## Diagnóstico

```bash
cd ~/radiocheck
DC="docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env"
$DC exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
SELECT to_char(detected_at AT TIME ZONE 'America/Sao_Paulo','DD/MM HH24:MI:SS') AS quando,
       s.name AS emissora, suppressed_short_id, kept_short_id,
       suppressed_duration AS dur_supr, kept_duration AS dur_kept,
       round(suppressed_confidence::numeric,3) AS conf_supr,
       round(kept_confidence::numeric,3) AS conf_kept, reason
FROM dedup_suppressions ds JOIN stations s ON s.id=ds.station_id
WHERE suppressed_confidence >= kept_confidence + 0.25
  AND detected_at > now() - interval '24 hours'
ORDER BY detected_at DESC;
SQL
```

Confirme se a v2 resgatou a tocada no pós-audit (com `DISAMBIG_BY_COVERAGE=true` ela
reatribui/retrata via a row do corte mantido):

```bash
grep DISAMBIG infra/docker/.env || echo "(nenhuma flag = todas OFF)"
docker logs docker-api-1 2>&1 | grep -E 'reattributed by coverage|co-fire' | tail -20
```

## Correção

1. **`DISAMBIG_BY_COVERAGE` está OFF?** É o fix desta classe de perda (validado E2E nas duas
   direções, incident §4e). Ligar seguindo
   [disambig-by-coverage-rollout.md](../operations/disambig-by-coverage-rollout.md) —
   nunca ligar `DISAMBIG_CONFIDENCE_AWARE` (reprovada, §4d: rouba tocadas reais do spot).
2. **Flag ON e a v2 resgatou** (log `reattributed by coverage` / `co-fire` no mesmo horário):
   nada a fazer — o alerta está medindo a supressão da v1, que a v2 desfez. Confirme a row
   viva do material curto no horário.
3. **Flag ON e a v2 NÃO resgatou** (sem row do corte mantido, ou audit em erro): a tocada é
   perda seca. Registrar **veiculação manual** para o material curto e anotar no follow-up
   **F-125** ([follow-ups-fase2.md](../roadmap/follow-ups-fase2.md)) — é exatamente o caso
   residual que o F-125 endereça (publicar-provisório no supervisor).
4. **Volume alto e recorrente no mesmo par**: avaliar com o dono desvincular o material curto
   das emissoras onde o spot que o contém também roda, ou faturá-lo por contagem manual até
   o F-125. Diagnóstico do histórico: `scripts/sql/diagnose-pulso-subset-rows.sql`.

## Escalação

Se houver >10 supressões suspeitas/dia em campanha ativa, ou se o material afetado estiver
em faturamento no mês corrente, avisar o dono no mesmo dia — a subcontagem vira crédito
para o cliente. Não espere a janela de sombra do rollout terminar.

## Prevenção

- Não cadastrar pulso/vinheta <10s do mesmo cliente de um spot que o contenha sem antes ler
  o aviso do wizard (Task 5 deste fix) — a limitação é estrutural com a flag OFF.
- Manter `DISAMBIG_BY_COVERAGE=true` após o aceite da sombra.
- F-125 fecha o caso residual (supressão v1 sem row do vencedor para resgatar).
