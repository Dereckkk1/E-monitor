# Regras de distribuição por material específico — Design

> Spec arquitetural. Documentação operacional final vai em `docs/features/` (regra 2 do CLAUDE.md).
> Data: 2026-06-29

## 1. Contexto e objetivo

Hoje, toda regra de distribuição é **por tipo** (`distribution_rules.type_id`, migration
0019): "Spot 30s = 3×/dia na rádio X" e qualquer material desse tipo cumpre a meta,
fungivelmente. O categorizador casa detecção × regra só por `type_id`
([categorizer.go](../../../workers/internal/categorizer/categorizer.go),
[detections.go](../../../workers/internal/catalog/detections.go)).

Isso impede rastrear desvios de um **material individual**. Exemplo do usuário:

- 5 spots de 30s rodam "3×/dia, seg–sex, 07–19h, durante um mês";
- 1 spot de 30s específico roda **só na 1ª semana, das 18 às 19h**.

Com o modelo por-tipo, o spot especial tocando às 10h na semana 2 conta como `in_slot`
(casa a faixa 07–19h do tipo) — o desvio fica invisível. O objetivo é **flagrar com
precisão** quando um material específico toca fora da data ou da faixa dele.

### Decisão fundamental (escolhas do usuário, 2026-06-29)

- **Precedência (carve-out):** quando um material é nomeado numa regra específica, ele é
  avaliado **só por essa regra** — as regras gerais do tipo deixam de valer pra ele.
- **`X/dia` = total do grupo:** numa regra que cobre vários materiais, `plays_per_day` é o
  total/dia somando os materiais selecionados (fungível entre eles), igual ao
  comportamento por-tipo atual.
- **Sem tela nova:** a exibição (grid emissora × tipo × dia, resumo, cores) **não muda**.
  Só a categorização de cada detecção fica mais fina. Numa célula do tipo, materiais
  corretos aparecem verdes e o material fora da regra aparece roxo/amarelo na mesma célula.

## 2. Abordagem

Coluna `material_ids UUID[]` em `distribution_rules`, espelhando o padrão `station_ids[]`
que já existe.

- `material_ids = '{}'` (vazio) → regra vale pra **todos** os materiais do tipo (= hoje).
- `material_ids` preenchido → regra vale **só** pra aqueles materiais.

Retrocompatível: toda regra existente recebe `'{}'` no `DEFAULT` e mantém o comportamento
atual. Sem migração destrutiva de dado. Alternativas descartadas: tabela de junção (joins
a mais, inconsistente com `station_ids[]` sem FK) e entidade "exceção" separada (segundo
caminho no categorizador, conceito a mais — o usuário pensa nisso como "uma regra").

## 3. Modelo de dados

```sql
-- migrations/0043_rule_material_scope.up.sql  (0042 é a última hoje; confirmar no impl)
ALTER TABLE distribution_rules
  ADD COLUMN material_ids UUID[] NOT NULL DEFAULT '{}';
```

- Sem FK (mesmo padrão de `station_ids`). Materiais deletados deixam UUIDs órfãos no array;
  inofensivo — o categorizador casa por `commercial_id` da detecção, e um UUID que não
  aponta mais pra material nenhum simplesmente nunca casa.
- **`distribution_overrides`: sem mudança** (continua por tipo — ver §7).
- **View `daily_play_summary`: sem mudança** (ver §6).

## 4. Semântica de categorização (o coração)

Definições, para uma detecção do material `M`, na campanha `C`, emissora `S`, instante `T`
(tudo em America/Sao_Paulo):

- **Regra específica de `M` em `S`:** uma `distribution_rules` com `type_id = tipo(M)`,
  `S ∈ station_ids`, `cardinality(material_ids) > 0` e `M ∈ material_ids`.
- **`M` está "carved-out" em `S`:** existe ≥1 regra específica de `M` em `S` —
  **independente de data/dia-da-semana**. (É isso que faz uma tocada fora do período do
  material virar desvio, não bônus.)
- **Regra geral do tipo:** `cardinality(material_ids) = 0`.

### Ordem de avaliação

1. `T` fora de `[campaign.start, campaign.end]` → `out_date`. *(inalterado)*
2. Existe **override** na célula `(C, tipo(M), S, data)` → lógica de override atual
   (faixa do override vence, vale pra todos os materiais da célula). *(inalterado — §7)*
3. `M` está carved-out em `S`?
   - **Sim** → avalia **só** as regras específicas de `M`:
     - alguma regra específica de `M` casa data ∈ `[start,end]` **e** dia-da-semana **e**
       `T ∈ [time_start−15min, time_end+15min]` → **`in_slot`** 🟢
     - senão, alguma regra específica de `M` casa data **e** dia-da-semana (mas `T` fora da
       faixa) → **`out_slot`** 🟡
     - senão (nenhuma regra específica de `M` cobre essa data+dia) → **`out_date`** 🟣
       *(tocou fora do período/dia programado pra ele)*
     - **nunca `orphan`** — material com regra é sempre julgado pela regra.
   - **Não** (material comum) → avalia **só** as regras gerais do tipo, **exatamente como
     hoje**: `in_slot` / `out_slot` / `orphan`. *(inalterado)*

Tolerância de 15 min (`SlotToleranceSeconds` = 900s) preservada em ambos os extremos.

### Tabela-resumo (material carved-out, dentro da campanha)

| Tocada | Categoria | Cor |
|---|---|---|
| No período + dia + faixa da regra dele | `in_slot` | 🟢 |
| No período + dia, fora da faixa horária | `out_slot` | 🟡 |
| Fora do período/dia programado dele | `out_date` | 🟣 |

### Exemplo do usuário (validação)

Regra A `{M1..M5}`, 3×/dia, seg–sex, 07–19h, mês todo. Regra B `{M6}`, 1×/dia, 1ª semana,
seg–sex, 18–19h. Todos os 6 ficam carved-out.

- M1 toca semana 1, 10h → regra A casa data+dia+faixa → 🟢
- M6 toca semana 1, 18h30 → regra B casa → 🟢
- M6 toca **semana 2**, 18h30 → regra B (1ª semana) não cobre essa data → **🟣 out_date**
- M6 toca semana 1, 10h → regra B cobre data+dia, faixa 18–19h não → **🟡 out_slot**

Na célula `(spot30, S, dia)` do grid, M1..M5 contam verde e o desvio de M6 conta
roxo/amarelo — "2 verdes e 1 roxo". Exatamente o pedido.

### Carve-out é por emissora

`M` carved-out só vale na(s) emissora(s) das regras específicas dele. Se `M6` tem regra só
em `{A,B}` e toca em `C`, em `C` ele **não** está carved-out → as regras gerais do tipo (se
houver) valem; sem regra geral → `orphan`. Coerente: ele não foi programado em `C`.

### Implementação Go (`categorizer.Categorize`)

- `Rule` ganha `MaterialIDs []uuid.UUID`.
- Assinatura ganha o `materialID` da detecção:
  `Categorize(detectedAt, cmp, materialID, rules, override)`.
- As `rules` passadas continuam pré-filtradas por `campaign + tipo(M) + station` (o SELECT
  do insert não filtra por material — Go faz o carve-out). Regras específicas de **outros**
  materiais do mesmo tipo entram na lista mas são ignoradas pra `M` (não estão em
  `general` nem nas específicas de `M`).
- Pseudo:
  ```
  carved := any(r in rules where len(r.MaterialIDs)>0 && contains(r.MaterialIDs, M))
  if carved {
      spec := rules where contains(r.MaterialIDs, M) && dateInRange && weekdayMatch
      if any(spec, T in window±15) return in_slot
      if len(spec)>0               return out_slot
      return out_date
  } else {
      general := rules where len(r.MaterialIDs)==0
      // lógica atual sobre `general`
  }
  ```

## 5. Recategorização retroativa

O SQL compartilhado [`recatClassifyTailSQL`](../../../workers/internal/catalog/distribution_rules.go#L218)
precisa espelhar **exatamente** o carve-out do Go — divergência Go×SQL é o risco histórico
desse módulo (ver comentário sobre tolerância de 15 min na própria função). A CTE `scope` já
fornece `material_id`; o `CASE` ganha:

- ramo carved-out: `EXISTS` de regra específica de `s.material_id` (cardinality>0 e
  `s.material_id = ANY(r.material_ids)`) →
  `in_slot` se casar data+dia+faixa, senão `out_slot` se casar data+dia, senão `out_date`;
- ramo geral: o `EXISTS` atual **filtrado a** `cardinality(r.material_ids) = 0`.

**Escopo da recategorização ao criar/editar regra:** hoje `RecategorizeForRule` recategoriza
`(campaign, tipo, rule.stations, rule.start..rule.end)`. Com carve-out, criar uma regra
específica de `M6` (1ª semana) precisa reavaliar também as tocadas de `M6` **fora** da 1ª
semana (que passam de `orphan`/`in_slot` → `out_date`). Logo: ao criar/editar **qualquer**
regra, recategorizar `(campaign, tipo, rule.stations, campaign.start..campaign.end)` — o
período **da campanha**, não o da regra. Delete segue recategorizando a campanha inteira
(comportamento atual). Continua tudo em SQL puro, milissegundos.

## 6. View `daily_play_summary` — por que não muda

- **Lado `expected`:** `SUM(plays_per_day)` agrupado por `(campaign, type, station, date)`.
  Regras específicas têm `type_id`, `station_ids`, datas e `weekday_mask` → entram na soma
  automaticamente. O esperado do tipo passa a ser "regras gerais + específicas", somado por
  dia respeitando cada período/dia-da-semana — que é o desejado.
- **Lado `actual`:** conta `detections.category` agrupado por tipo. As categorias já vêm
  corrigidas pelo categorizador → zero mudança na view.

> Guardrail (UX, opcional v1): evitar misturar, no mesmo tipo, uma regra **geral** com
> regras **específicas** cobrindo os mesmos materiais — o `expected` somaria os dois e
> duplicaria. O fluxo natural (enumerar materiais por regra, como no exemplo) não cai nisso.

## 7. Overrides — fora de escopo nesta entrega

`distribution_overrides` continua por `(campaign, type, station, date)`. Quando há override
numa célula+dia, ele **vence pra todos os materiais** daquela célula naquele dia (a precisão
por material fica suspensa só naquele dia). É a regra de precedência de override que já
existe ([override-time-window](../../features/override-time-window.md)); aceito pro v1.
Override por material seria entrega futura.

## 8. Frontend

### `RuleSidePanel`
- Seletor de materiais **opcional**, visível só quando exatamente **1 tipo** está
  selecionado (materiais pertencem a um tipo). Lista os materiais **daquele tipo
  vinculados à campanha** (de `campaignMaterials` + `materialsById`, já disponíveis no
  `DistributionStep`).
- Multi-select de tipos e seleção de materiais são mutuamente exclusivos: escolher um 2º
  tipo limpa/desabilita os materiais.
- Vazio = "todos do tipo" (regra de tipo, hoje). Texto-resumo do painel reflete o escopo
  ("vale pra qualquer material do tipo X" vs "vale só pra: A, B, C").
- Edit mode (1 regra = 1 tipo): pré-preenche `material_ids` da regra.

### `DistributionStep` / `RuleChipList`
- Chip da regra mostra o escopo: "todos do tipo" vs "N materiais" (ou o nome quando 1).
- `submitRule`: o fan-out por tipo continua; em modo 1-tipo + materiais é 1 POST com
  `material_ids`. (Multi-tipo nunca carrega `material_ids`.)
- `ruleWindowsForCell` (alimenta o popover de override): pode seguir mostrando as faixas de
  todas as regras da célula, incluindo específicas — informativo, sem mudança de
  comportamento de override.

### API client / hooks
- `useCreateDistributionRule` / `useUpdateDistributionRule`: incluir `material_ids` no body.

## 9. API

- `rulePayload` + `CreateDistributionRuleInput` + `DistributionRule` + SQL de Create/Update/
  SELECT (`ruleColumns`): adicionar `material_ids`.
- `ListApplicable` (se ainda usado em algum caminho) e o SELECT inline do insert
  ([detections.go:186](../../../workers/internal/catalog/detections.go#L186)): passar a
  selecionar `material_ids` pra alimentar o `Rule.MaterialIDs`.

## 10. Testes

- `categorizer_test.go`: carved-out in_slot/out_slot/out_date; precedência (geral ignorada);
  carve-out por emissora; fungibilidade do grupo; material comum inalterado.
- **Paridade Go×SQL:** teste que roda os mesmos cenários pelo `recatClassifyTailSQL` (sobre
  um Postgres de teste) e confere igualdade com o `Categorize` Go — é o ponto de maior risco.
- Smoke manual do wizard: criar regra com materiais, ver chip, ver categorização no grid.

## 11. Docs a atualizar (regra 2 do CLAUDE.md)

- Novo: `docs/features/material-specific-distribution-rules.md` (header YAML, `status`).
- Atualizar `docs/architecture/distribution-rules.md` (semântica de carve-out, nova coluna).
- Atualizar `docs/features/campaign-wizard.md` (Step 5: seletor de materiais na regra).
- Atualizar índice em `docs/README.md` e o mapa de consulta no `CLAUDE.md`.

## 12. Arquivos afetados (mapa)

| Arquivo | Mudança |
|---|---|
| `migrations/0043_rule_material_scope.up.sql` (+ `.down.sql`) | coluna `material_ids` |
| `workers/internal/categorizer/categorizer.go` | `Rule.MaterialIDs`, carve-out, assinatura |
| `workers/internal/categorizer/categorizer_test.go` | casos novos |
| `workers/internal/catalog/detections.go` | SELECT de `material_ids`, passar `materialID` |
| `workers/internal/catalog/distribution_rules.go` | struct/input, Create/Update/SELECT, `recatClassifyTailSQL`, escopo da recategorização |
| `workers/internal/api/handlers/distribution_rules.go` | `rulePayload.material_ids` |
| `frontend/src/components/RuleSidePanel.jsx` | seletor de materiais |
| `frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx` | passar materiais por tipo, chip de escopo, fan-out |
| `frontend/src/api/hooks.js` | `material_ids` no body |

## 13. Fora de escopo

- Override por material (overrides seguem por tipo — §7).
- Mudança de exibição/grid/resumo (decisão do usuário — §1).
- Validação "future-only edit" no backend (F-91, pré-existente).
- Reconhecimento de versão / desambiguação 30s×60s (independente).
