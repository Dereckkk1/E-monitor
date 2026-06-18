---
status: implementado
ultima-verificacao: 2026-06-18
codigo-relacionado:
  - migrations/0039_backfill_legacy_commercials.up.sql
  - migrations/0024_unify_short_id_drop_detections_fk.up.sql
  - workers/internal/fingerprintqueue/reconciler.go
  - scripts/deploy.sh
  - CLAUDE.md (regra §4.8)
  - docs/operations/migrations.md
  # data-do-incidente: 2026-06-17
---

# Incidente 2026-06-17 — Migration 0039 deixou o schema dirty e travou o deploy

> **TL;DR:** uma migration de backfill (`0039`) passou no teste local porque o
> DB de dev tinha **0 commercials** — o `INSERT ... SELECT` era no-op. Em prod,
> com dados reais, ela copiava `commercials.short_id` pra `materials.short_id`,
> colidindo com a constraint `materials_short_id_key UNIQUE`. A migration falhou,
> o golang-migrate marcou o schema `dirty=true` na versão 39, e **todo deploy
> seguinte morria em ~0.7s** (o migrate recusa rodar em DB dirty). Nenhum dado
> foi perdido — a migration roda em transação e fez rollback. Recovery:
> `UPDATE schema_migrations SET version=38, dirty=false` + redeploy com a
> migration corrigida. Defesa que ficou: **teste de migrations em sombra** no
> `deploy.sh` (CLAUDE.md §4.8).

## Impacto

- **Deploy de prod bloqueado** por ~1h (várias tentativas falhando no passo
  `up -d` / `migrate exit 1`).
- **Sem perda de dado**: tripwire de row-count passou em todos os deploys, e a
  0039 (BEGIN/COMMIT) fez rollback ao falhar. Backup pré-deploy disponível o
  tempo todo.
- **Bugs originais** (recategorização ao trocar tipo + biblioteca incompleta)
  só foram pra prod após o fix da migration.

## Causa raiz

### #1 — Cópia de `short_id` colidiu com `materials.short_id UNIQUE`

A 0039 espelhava cada `commercial` legado num `material` reaproveitando o MESMO
`short_id` (imitando o backfill da 0016). Mas:

- `commercials` e `materials` tinham **sequences SEPARADAS** antes da migration
  0024 (que unificou em `catalog_short_id_seq`).
- Logo, um commercial pós-0016 e um material do wizard podem ter o **mesmo
  short_id** (valores sobrepostos entre as duas sequences antigas).
- O `ON CONFLICT (id) DO NOTHING` da 0039 só tratava colisão de **id (PK)** — não
  de `short_id`. Em prod (commercials short_id 1–19, materials ocupando esses
  números), o INSERT violou `materials_short_id_key` → erro → dirty.

**Fix:** a 0039 (e o `reconciler.mirrorReadyLegacyCommercials`) deixaram de
copiar `short_id`. O material espelho recebe um short_id NOVO do
`catalog_short_id_seq` (default desde a 0024). É inócuo: o fingerprint é casado
pelo **UUID** (não pelo short_id) e o dedup do índice é por `id`/`campaign_materials`.

### #2 — DB local vazio deu falso verde

O `go test` (`TestMirrorReadyLegacyCommercials`) e o teste manual da migration
rodaram contra um DB de dev com **0 commercials**. O `INSERT ... SELECT ... FROM
commercials` simplesmente não tocava nenhuma linha → passou. A falha **dependia
dos dados de prod** (colisão), invisível em qualquer base vazia/sparsa.

Classe do problema: backfills, `ADD CONSTRAINT`, `CREATE UNIQUE INDEX`, dedup —
qualquer migration cuja falha dependa do conteúdo existente.

### #3 — Bug latente na 0024 (descoberto durante o diagnóstico)

Os logs do migrate mostravam também
`setval: value 0 is out of bounds for sequence "catalog_short_id_seq"` na 0024.
`setval('catalog_short_id_seq', GREATEST(MAX(commercials), MAX(materials)))` vira
`setval(seq, 0)` quando ambas as tabelas estão vazias — o que **quebraria um
setup do zero** (DR / VM nova). Não afetou este incidente (prod tem dado, 0024 já
aplicada), mas era uma mina pra recovery. Endurecido: só faz `setval` quando há
dado; em banco vazio mantém o default (próximo `nextval = 1`).

## Timeline (2026-06-17)

1. Push dos fixes dos dois bugs originais, incluindo a 0039 (versão com `c.short_id`).
2. Deploy na VM → `migrate exit 1` no `up -d`. Schema marca `dirty=39`.
3. Re-deploys seguintes falham em ~0.7s (migrate recusa DB dirty). Cascata
   `redis/postgres dependency failed` (o migrate gateia a cadeia do compose).
4. Diagnóstico: `schema_migrations` = (39, dirty). Logs do migrate revelam
   `duplicate key value violates unique constraint "materials_short_id_key"` na
   0039 rodando a versão ANTIGA (com `c.short_id`), além do ruído histórico da
   0024 (`setval 0`).
5. Confirmado que o DB tem dado (1044 campanhas, 12026 detecções, etc.) e a
   sequence está saudável (110, acima do max) — descarta volume vazio/errado.
6. Fix da 0039 (omitir `short_id`) + teste de regressão `TestMirrorShortIDCollision`.
   Pull na VM → `UPDATE schema_migrations SET version=38, dirty=false` → redeploy.
7. Deploy verde: `version=39, dirty=f`, tripwire `materials: 99 → 100` (1
   commercial legado espelhado), API no ar.

## Ações pós-incidente

| Ação | Onde | Status |
|------|------|--------|
| 0039/reconciler param de copiar `short_id` (usam `nextval`) | `migrations/0039_*`, `reconciler.go` | ✅ |
| Teste de regressão da colisão de short_id | `TestMirrorShortIDCollision` | ✅ |
| **Teste de migrations em sombra no deploy** (aplica migrations numa cópia de prod antes do `up -d`; aborta se falhar) | `scripts/deploy.sh` (`shadow_migration_test`) | ✅ |
| Regra "migration de dado: testar contra cópia de prod" | CLAUDE.md §4.8 | ✅ |
| Procedimento manual de teste de migration contra clone de prod | `docs/operations/migrations.md` | ✅ |
| Endurecer 0024 contra `setval(seq, 0)` em banco vazio | `migrations/0024_*` | ✅ |

## Lições

1. **Migration de dado nunca é validada por DB local vazio.** O `go test`/dev
   sparso dá falso verde. Rode contra uma cópia dos dados reais — agora o
   `deploy.sh` faz isso automático (teste de sombra), e o procedimento manual
   está em migrations.md.
2. **`ON CONFLICT` cobre UMA constraint.** Um INSERT que pode violar PK **e**
   uma UNIQUE secundária não fica seguro só com `ON CONFLICT (id)`. Prefira não
   reaproveitar valores de colunas UNIQUE — deixe o default/sequence alocar.
3. **`migrate exit 1` em <1s = DB dirty**, não erro de SQL novo. O recovery é
   `force`/`UPDATE schema_migrations` pra versão boa + re-run da migration
   corrigida (a transação da migration falha faz rollback — sem dado parcial).

## Referências

- Commits: fix da 0039 (`1de1721`), guard de sombra + docs (`2363d5b`).
- [CLAUDE.md §4.8](../../CLAUDE.md) — migration de dado: testar contra cópia de prod.
- [docs/operations/migrations.md](../operations/migrations.md) — teste de sombra + procedimento manual.
- [docs/features/material-library.md](../features/material-library.md) — backfill/mirror commercials→materials.
- [Incidente 2026-05-12](incident-2026-05-12-pgdata-loss.md) — origem das regras §4.x e do `deploy.sh` defensivo.
