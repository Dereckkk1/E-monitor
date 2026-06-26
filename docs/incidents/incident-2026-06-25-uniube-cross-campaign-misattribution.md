---
status: implementado
ultima-verificacao: 2026-06-25
codigo-relacionado:
  - workers/internal/evidence/disambig_coverage.go
  - workers/internal/evidence/attribution.go
  - migrations/0041_detection_campaigns.up.sql
  - docs/features/multi-attribution.md
---

# Incidente 2026-06-25 — UNIUBE: veiculações na campanha errada (material duplicado em campanhas sobrepostas)

## Resumo

Operador reportou que a emissora **Jovem Pan News FM (98.9) / Ituiutaba** (`24757c2a-15c0-4c17-99f3-fcf05ba8bc93`, short_id 2911) "não teve detecção ontem (2026-06-24)" e perguntou se foi **bloqueio** ou **nenhum match**. Investigação mostrou que **nenhum dos dois**: a emissora estava saudável e o spot foi detectado **16×**, mas **atribuído à campanha errada**. Causa raiz: o mesmo áudio subido como **dois materiais** em **duas campanhas sobrepostas** que compartilham a emissora. Corrigido manualmente em prod (forward fix + backfill + recategorização) e, em definitivo, pela feature **F-119 multi-atribuição** (mergeada em `master`, atrás de flag; deploy pendente).

## Linha do tempo / diagnóstico

1. **Premissa furada.** O banco tinha **16 detecções em 06-24** (e 18+ em 06-25). O "zero" vinha de uma **tela filtrada** pela campanha 248, não da emissora.
2. **Não foi bloqueio.** Prometheus: ~**5,4 GB** recebidos no dia, **0 stall restarts**, ~1 reconnect, sem `connect_backoff`. Stream saudável; logs com `window match` ao vivo.
3. **Má-atribuição.** Todas as 16 caíram em **campanha 191 "(MRA) UNIUBE | CAMPEONATO"** (ativa, 06-01→06-30, material **130**), quando deviam contar pra **campanha 248 "(MRA) UNIUBE | JUNHO/JULHO"** (`bd8b5ff2-607a-45c5-b9f0-594dc11078d2`, ativa, 06-24→07-22, material **138**).
4. **Duplicata acústica.** Materiais 130 e 138 têm `master_sha256` **idêntico** (`d2a7478ac86fdfa8…`) — o mesmo arquivo subido duas vezes. As duas campanhas targetam Ituiutaba e **se sobrepõem** (06-24→06-30).

## Causa raiz

O mesmo áudio em duas campanhas que compartilham emissora. Quando o spot toca, ambas as state machines confirmam; a **desambiguação de versão §18.2.2** (`evidence/disambig_coverage.go` `chooseByCoverage`) trata 130 e 138 como cortes-irmãos; cobertura e duração empatam (áudio idêntico) → desempate pelo **menor short_id** → **130 vence** → campanha 191. O perdedor (138/248) **não deixa row**. A atribuição por-emissora funciona por design (confirmação por short_id da emissora + `resolveAttribution` filtra `target_stations`), mas **fura quando a MESMA emissora está nas duas campanhas com áudio idêntico**. Ver memória `duplicate-material-cross-campaign-misattribution`.

## Correção imediata (prod, manual)

1. **Forward fix (UI):** remover Ituiutaba do `campaign_materials` do material 130 na campanha 191 → dispara `Supervisor.Reload(191)`; depois disso `resolveAttribution(130, Ituiutaba)` falha e o gate em `evidence/service.go:561-570` (§18.2.2 v2) deixa a row em 138/248.
2. **Backfill + recategorização:** `UPDATE detections` movendo as veiculações de 130/191 → 138/248, seguido de **recategorização** contra as regras da 248 (passo obrigatório — a grade pinta verde só `in_slot`; sem recategorizar, as linhas chegavam `orphan` e a tela "não funfava"). 0 override, todas viraram `in_slot`, déficit zerou.
3. **Varredura de raio:** a mesma duplicata 130/138 colidia em **2 emissoras compartilhadas** — **Favorita (496)** além da Jovem Pan (2911) — **75 veiculações** presas em 130/191 no total. Backfill em lote pras duas.

## Correção definitiva — F-119 (multi-atribuição)

Em vez de exigir deduplicação manual a cada caso, a feature **F-119** faz uma tocada contar para **todas** as campanhas que rodam o mesmo áudio na emissora. Implementação: **tabela de ligação** `detection_campaigns` (1 tocada física em `detections`, N projeções por campanha), **sem tocar** no subsistema frágil de §18.2.2/audit/dedup. Atrás da flag `MULTI_ATTRIBUTION` (OFF = 1:1 idêntico). Migração 0041. Detalhes: [multi-attribution.md](../features/multi-attribution.md), spec `docs/superpowers/specs/2026-06-25-multi-attribution-f119-design.md`. **No `master` (não deployado).**

## Correções colaterais

- **`start.sh` travado:** o DB de dev local estava `dirty` na migração 0040 (`similarity_segments` — coluna já aplicada, flag dirty preso). `UPDATE schema_migrations SET dirty=false WHERE version=40` destravou.
- **`Campaigns.Create` nil-safe:** `TargetStations` nil virava NULL e violava a constraint; passou a coalescer pra `'{}'` (casa com o default da coluna). Destravou fixtures de teste velhas.

## Ações pós-incidente / follow-ups

- **Deploy do F-119 faseado:** 1º com flag OFF (valida migração 0041 + backfill contra dados de prod via shadow test, regra 4.8, zero mudança de comportamento) → depois liga `MULTI_ATTRIBUTION=true` no `.env` da VM + recreate `--no-deps api`.
- **Pré-reqs antes de fan-out em larga escala** (em `multi-attribution.md`): recategorizador **por-projeção** (hoje sincroniza só a canônica) e audit **per-airing**. Impacto moderado (categoria stale na grade após editar regra; não quebra contagem — o filtro aprovado fica na tocada base).
- **Higiene operacional:** evitar subir o mesmo áudio como materiais distintos; reusar **um** material vinculado a N campanhas (a biblioteca já suporta). O alerta de similaridade ≥50% no upload já existe.
