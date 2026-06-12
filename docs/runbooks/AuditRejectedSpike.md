# AuditRejectedSpike

**Severidade:** warning · **Categoria:** Detecção
**Métrica:** `radiocheck_audit_attempts_total{result="rejected"}` (>8% do total em 6h)
**Origem:** incidente [2026-06-12](../incidents/incident-2026-06-12-detection-recall-gaps.md) — picos de 15-17% das detecções/dia rejeitadas em silêncio (03-05/06).

## Sintomas

- Fração anormal das detecções confirmadas ao vivo está sendo rejeitada pelo audit pré-persist (§9.9) e marcada `evidence_status='audit_rejected'`.
- Essas detecções **não aparecem** em /detections, /insights, relatórios nem daily_play_summary — pro usuário, é como se a veiculação não tivesse acontecido.
- Sinal típico associado: divergência crescente vs fornecedor externo.

## Causas Comuns

1. **Fingerprint do master inconsistente com o código** — migração de re-fingerprint incompleta (foi a causa do incidente; ver gate em `scripts/check-fingerprint-freshness.sh`).
2. **Extração de evidência desalinhada** — clipe salvo não contém o trecho atribuído (timing de segmentos).
3. **Threshold do audit rígido pra emissoras ruidosas** — score/coverage do clip degradado fica abaixo de `minScore=5`/`minCoverage=0.15`.
4. **Onda de falsos positivos reais** — o audit funcionando como deve (ex.: material com sting compartilhado novo). Confirmar antes de "consertar".

## Diagnóstico

```bash
# 1. Volume e distribuição por emissora/material:
docker compose -f infra/docker/docker-compose.yml \
               -f infra/docker/docker-compose.override.yml \
               --env-file infra/docker/.env \
  exec postgres psql -U radiocheck -d radiocheck -c "
SELECT s.name AS emissora, m.title AS material, count(*),
       min(d.detected_at) AS primeiro, max(d.detected_at) AS ultimo
FROM detections d
JOIN stations s ON s.id = d.station_id
LEFT JOIN materials m ON m.id = d.commercial_id
WHERE d.evidence_status = 'audit_rejected'
  AND d.detected_at > now() - interval '24 hours'
GROUP BY 1, 2 ORDER BY 3 DESC LIMIT 20;"

# 2. Fingerprints estão consistentes com o código?
bash scripts/check-fingerprint-freshness.sh

# 3. Score/coverage das rejeições (log do api):
docker compose ... logs --since 6h api | grep "audit REJECTED" | tail -10
```

Leitura: concentrado em UMA emissora → ruído/threshold (causa 3). Concentrado em UM material → fingerprint/compartilhamento (causas 1/4). Espalhado → extração (causa 2) ou migração (causa 1).

## Correção

- **Causa 1:** re-rodar a migração de re-fingerprint para os materiais apontados pelo check (ver [refingerprint-density-migration.md](../operations/refingerprint-density-migration.md)).
- **Causa 3:** comparar os scores logados com os thresholds; se sistematicamente "quase" (score 3-4, coverage 0.10-0.14) numa emissora ruidosa, considerar recalibração do audit (T9 do [plano de remediação](../roadmap/2026-06-12-plano-remediacao-recall.md) — não mexer sem cruzar com o fornecedor antes; o audit é a rede anti-FP).
- **Causa 4:** nenhuma — o audit está protegendo. Investigar o material (similaridade/sting compartilhado).

## Escalação

Se >15% por mais de 24h, tratar como incidente de recall (impacto direto na concordância com o fornecedor, meta §1.4 = 98%).

## Prevenção

Cruzamento periódico de uma amostra de `audit_rejected` com o relatório do fornecedor (T5 do plano de remediação) — distingue "audit salvando de FP" de "audit comendo detecção verdadeira".
