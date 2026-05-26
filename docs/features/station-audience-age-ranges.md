---
status: implementado
ultima-verificacao: 2026-05-26
codigo-relacionado:
  - workers/internal/catalog/stations.go
  - frontend/src/pages/StationEditPage.jsx
  - migrations/0033_migrate_audience_age_ranges.up.sql
---

# Faixa etária estruturada no perfil de audiência

O perfil de audiência da emissora (`stations.metadata.audience_profile`) passou a ter o
dado de faixa etária armazenado como três percentuais quantitativos, no mesmo formato
do bloco de classe social — e não mais como string livre.

## Forma anterior

```json
{
  "audience_profile": {
    "gender":      { "male": 60, "female": 40 },
    "ageRange":    "77% 30+",
    "socialClass": { "classeAB": 50, "classeC": 35, "classeDE": 15 }
  }
}
```

`ageRange` era um campo texto sem estrutura. O valor podia ser qualquer coisa —
"77% 30+", "Adultos", "30-49 anos: 60%" — o que dificultava qualquer agregação,
filtro ou visualização programática.

## Forma atual

```json
{
  "audience_profile": {
    "gender":      { "male": 60, "female": 40 },
    "ageRanges": {
      "range18to24": 36.0,
      "range25to49": 34.0,
      "range50plus": 30.0
    },
    "ageRangeLegado": "77% 30+",
    "socialClass": { "classeAB": 50, "classeC": 35, "classeDE": 15 }
  }
}
```

Três percentuais que somam 100 (com tolerância visual — soft warning, ver
[validação](#validação)), nas faixas:

- `range18to24` — 18 a 24 anos
- `range25to49` — 25 a 49 anos
- `range50plus` — Acima de 50 anos

## Migração em massa no deploy (0033)

A migration [`0033_migrate_audience_age_ranges`](../../migrations/0033_migrate_audience_age_ranges.up.sql)
roda automaticamente no deploy e aplica a heurística abaixo a todas as stations
que ainda têm `ageRange` em texto livre, derivando os 3 percentuais e movendo o
texto original pra `ageRangeLegado`:

```
range18to24 = XX / 2
range25to49 = XX / 2
range50plus = 100 - XX
```

onde `XX` é o percentual extraído de strings no formato `XX% YY+` (ex: `90% 18+`,
`77% 30+`). O limite de idade `YY` do texto legado é **ignorado** — a heurística
assume que XX é a fatia "adulta" da audiência e o complemento vai pra 50+.

Exemplos:

| ageRange legado | range18to24 | range25to49 | range50plus |
|-----------------|-------------|-------------|-------------|
| `90% 18+`       | 45.0        | 45.0        | 10.0        |
| `77% 30+`       | 38.5        | 38.5        | 23.0        |
| `60% 25+`       | 30.0        | 30.0        | 40.0        |

Stations cujo `ageRange` não casa com a regex `XX% YY+` (texto não-paramétrico, como
"Adultos jovens" ou vazio) **não recebem `ageRanges`** — o texto original vai pra
`ageRangeLegado` e os 3 percentuais ficam pra serem preenchidos manualmente pelo
operador no editor.

A migration é re-runnable: stations com `ageRanges` já presente são puladas.

## Preservação no editor (após migração)

Stations que receberam a migration têm tanto `ageRanges` quanto `ageRangeLegado`
no JSON. No `StationEditPage`:

1. Os 3 inputs lêem `ap.ageRanges?.range18to24` etc.
2. O hint discreto abaixo mostra `ageRangeLegado ?? ageRange` (priorizando o legado
   já migrado, com fallback pro texto bruto em casos onde algo escapou da migration).

> Faixa etária anterior preservada: *77% 30+*

A chave JSON antiga (`ageRange`) **não é mais escrita** pelo editor — o save
sempre produz `ageRanges` + (opcionalmente) `ageRangeLegado`. O struct Go
(`workers/internal/catalog/stations.go`) ainda declara `AgeRange` como campo
opcional pra tolerar leituras de eventuais resíduos de stations não cobertas
pela migration.

## Validação

Soft: se a soma dos 3 percentuais for maior que zero e diferir de 100% por mais
de 0.05 pontos, aparece o hint:

> Soma atual: 97.0% — esperado 100%

O save **não** é bloqueado. Mesmo padrão do bloco de classe social e gênero —
edição parcial é permitida. A validação visual está no
[`StationEditPage.jsx`](../../frontend/src/pages/StationEditPage.jsx) inline no
bloco "Faixa etária".

## Schema do banco

Nenhuma migração SQL — `stations.metadata` é JSONB e absorve a mudança de forma
de objeto livre. Stations existentes mantêm o `ageRange` antigo no JSONB até
serem editadas.

## Pontos de leitura

A única tela que lê o `audience_profile` hoje é
[`StationEditPage.jsx`](../../frontend/src/pages/StationEditPage.jsx) (rota
`/stations/:id/edit`). Nenhuma outra view do frontend exibe esse dado, e nenhum
worker depende dele para detecção. Reports de campanha também não consomem
faixa etária. Por isso a mudança fica localizada ao editor.
