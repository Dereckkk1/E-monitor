---
status: implementado
ultima-verificacao: 2026-08-06
codigo-relacionado:
  - workers/internal/catalog/failures_daily.go
  - workers/internal/catalog/failures_daily_test.go
  - workers/internal/api/handlers/admin_failures_daily.go
  - workers/internal/api/handlers/admin_failures_daily_test.go
  - workers/internal/api/router.go
  - workers/cmd/api/main.go
  - frontend/src/components/FailuresDailyView.jsx
  - frontend/src/components/FailuresDailyView.css
  - frontend/src/pages/AdminStationFailuresPage.jsx
  - frontend/src/api/hooks.js
---

# Admin → Falhas por dia (aba "Por dia" de `/admin/station-failures`)

Terceira aba de `/admin/station-failures`, ao lado de **Por emissora** e **Por campanha**.
Enquanto as duas primeiras respondem *"quem falhou neste dia"*, esta responde
**"em que parte do mês a operação quebra"** — série temporal de falhas por dia
num período, com painéis de padrão (terços do mês e dia da semana).

## Por que existe

O operador conseguia investigar um dia por vez, mas não enxergava recorrência.
Perguntas que a página não respondia: *o problema é sempre na virada do mês? Toda
segunda-feira cai alguma coisa? Este mês foi pior que o passado?* Responder isso
exigia abrir 30 datas na mão e anotar.

## Quem pode ver

Admin only — mesma proteção das outras duas abas.

- **Rota frontend**: já coberta pelo `<RequireRole roles={['admin']}>` da página em `App.jsx`
- **Endpoint backend**: `auth.RequireRole("admin")` no grupo admin do `router.go`

## O que conta como falha (idêntico às outras abas)

**Emissora com `deficit > 0` no dia, em campanha não-cancelada.** É a mesma
definição de [admin-station-failures.md](admin-station-failures.md), e é
deliberado: clicar numa barra do gráfico leva pra aba "Por emissora" naquela
data, e os dois números têm que bater. Worker travado sem campanha agendada
continua não contando (spec 2026-05-25).

Consequência importante para a métrica **tempo fora do ar**: ela soma o downtime
**só das emissoras que tiveram déficit naquele dia**. Uma queda sem campanha
agendada não aparece — se aparecesse, o gráfico mostraria um pico num dia que a
aba "Por emissora" abre vazia. Para downtime bruto de infra, use `/admin/overview`
ou `/monitoring`.

## Endpoint

```
GET /v1/internal/admin/failures-daily?from=YYYY-MM-DD&to=YYYY-MM-DD&min_down_seconds=60
```

| Param | Default | Limites |
|-------|---------|---------|
| `from` | hoje-29 | `≥ hoje-90d`. Fora disso → 400 |
| `to` | hoje | Data futura é **cortada em hoje**, não 400 (ver abaixo) |
| `min_down_seconds` | 60 | 0–3600. Fora da faixa → ignora (mantém 60) |

Range mais largo que **92 dias** → 400. `from > to` → 400.

**Por que `to` no futuro não é erro:** o preset "Este mês" da UI manda o último
dia do mês corrente. Isso é uso normal, e devolver 400 obrigaria o frontend a
duplicar a regra de "hoje". O handler corta e segue.

**Por que o teto é 90 dias e não mais:** espelha o `/admin/station-failures`. Se
o gráfico mostrasse um dia mais antigo, clicar na barra levaria pra uma data que
o próprio input de data da aba "Por emissora" recusa.

### Resposta

```json
{
  "from": "2026-07-01",
  "to": "2026-07-31",
  "summary": {
    "days_total": 31,
    "days_with_failure": 27,
    "peak_stations": 9,
    "peak_date": "2026-07-13",
    "total_deficit": 431,
    "total_down_seconds": 83520,
    "avg_stations": 3.516
  },
  "days": [
    { "date": "2026-07-01", "stations": 3, "campaigns": 5,
      "deficit": 22, "down_seconds": 1920, "down_events": 4 },
    { "date": "2026-07-02", "stations": 0, "campaigns": 0,
      "deficit": 0, "down_seconds": 0, "down_events": 0 }
  ]
}
```

**A série é densa de propósito.** Todo dia de `[from, to]` aparece, inclusive os
sem falha, zerados. Um gráfico de barras com dias faltando mente sobre o padrão
do mês: um domingo sem falha viraria "não existiu domingo", e a média por dia da
semana ficaria errada.

`peak_date` vem `""` quando nada falhou (nunca `"0001-01-01"`) — o frontend usa
isso para decidir entre gráfico e empty state.

Empate no pico resolve pelo **dia mais antigo**, para o número ser determinístico
entre requests.

## Algoritmo

Duas queries + merge em Go (`catalog.FailuresDaily.ListDaily`):

1. **Q1 — déficit por (dia, emissora)**: `daily_play_summary_for(from, to, NULL)`
   com `deficit > 0` e `c.status <> 'cancelada'`, agrupado por `for_date, station_id`.
   O recorte por data é **pushdown dentro da função** (migration 0052), não um
   `WHERE` por cima.
2. **Q2 — downtime por (dia, emissora)**: `stream_health_events` do tipo `down`,
   agrupado por dia local (`AT TIME ZONE 'America/Sao_Paulo'`) e emissora, com
   `HAVING SUM(...) >= min_down_seconds`.

O merge só soma o downtime de emissoras presentes na Q1 daquele dia (ver seção
acima). Depois `buildDailyResult` preenche os dias faltantes com zeros e calcula
o sumário — função **pura**, testada isoladamente sem DB.

`stations` sai de um **set** de UUIDs, não de contagem de linhas: a mesma
emissora aparece uma vez por campanha, e contar linha inflaria o número que a
aba "Por emissora" mostra como lista.

### Armadilha evitada: `daily_play_summary_for` e `out_date`

Esta agregação é segura para o pushdown da 0052 porque agrupa por `for_date`
dentro da janela (`BETWEEN` de dia). Ela **não** agrega `out_date`/`extras` sem
lower bound — que é o caso que a função corta silenciosamente. Ver a memória
`daily-play-summary-for-out-date-lower-bound-trap` antes de estender esta query
para outras colunas.

## Frontend

Componente **autocontido** em `FailuresDailyView.jsx` + `.css`. Ele não depende
do CSS da página que o monta (as classes `.asf-*`): declara as próprias
`.fd-kpi*`, `.fd-error` etc. Isso foi consertado na inspeção — na primeira
versão ele reusava `.asf-hero`/`.asf-ckpi` e saía sem estilo fora da página.

### Estrutura

1. **Barra de filtros** (escopa tudo abaixo): presets de período · range manual · métrica
2. **KPIs**: dias com falha (X/N) · pior dia · média por dia · total no período
3. **Gráfico de barras** por dia + linha de referência da média
4. **Painéis de padrão**: terços do mês e dia da semana
5. **Tabela-gêmea** do gráfico (toggle "Ver tabela")

### As três métricas

Escolhidas na barra de filtros; a métrica escopa gráfico, KPIs **e** painéis.

| Métrica | Campo | Pergunta que responde |
|---|---|---|
| Emissoras (default) | `stations` | operação — quantas rádios quebraram |
| Veiculações perdidas | `deficit` | comercial — quanto custou em inserção |
| Tempo fora do ar | `down_seconds` | infra — quanto de ar se perdeu |

**Tempo fora do ar é plotado em minutos, não segundos.** Com segundos, o recharts
escolhe ticks como 2000/4000/6000/8000 e o formatador arredondava 6000 e 8000
para o **mesmo `"2h"`** — o eixo repetia rótulo e mentia. Em minutos os ticks caem
redondos (`0min / 30min / 1h / 1h30 / 2h`).

### Painéis de padrão — por que média, não soma

Os dois painéis comparam grupos com **quantidades diferentes de dias**: o 3º
terço tem 11 dias contra 10 dos outros, e "últimos 30 dias" pode cair com 20 dias
num terço e 10 noutro. A barra carrega a **média por dia**; comparar somas diria
que o 3º terço é sempre o pior.

Cada painel fecha com uma **frase-veredito** ("Concentra em dias 11–20: 2,4× a
média dos outros dois terços"). Ela só aponta um campeão quando a separação passa
de **1,25×** (`VERDICT_MIN_RATIO`) — abaixo disso a diferença é oscilação, e
cravar "as segundas são o problema" mandaria alguém investigar um padrão
inexistente. Sem separação suficiente, a frase diz que nada se destaca.

### Cores

Uma série por vez, então **uma cor para todas as barras** (`#ec4899`). Nada de
barra mais escura onde é maior: isso gastaria o canal de cor repetindo o que a
altura já diz. O único desvio é o **pior dia**, no passo escuro do mesmo tom
(`#a4114f`) — ênfase, não escala de valor.

O par foi validado com o script da skill `dataviz`
(`validate_palette.js`, surface `#fff`): ΔE 18.7 deutan / 19.2 normal, ambos
≥ 3:1 de contraste. **Revalide antes de trocar.**

### Interação

- **Clique numa barra** → vai pra aba "Por emissora" naquela data (+ scroll pro topo).
  Dia sem falha não é clicável.
- **Tabela-gêmea**: todos os dias com as três métricas + botão "Abrir dia" por linha.
  É o caminho acessível equivalente ao gráfico (o tooltip nunca é a única forma de ler um valor).
- **Botão atualizar** do header da página invalida a chave `['failures-daily']` —
  a aba é dona da própria query, então não há `refetch` pra a página segurar.

### Responsivo

- **Densidade do eixo X vem da largura medida** (`ResizeObserver`), não da contagem
  de pontos: 31 datas com rótulo a cada 3 é confortável em 1300px e vira tarja
  ilegível em 320px. `MIN_PX_PER_TICK = 92`.
- **≤ 640px**: KPIs viram grade 2×2 (em linha, "dias com falha" quebrava em 3
  linhas e o total era cortado pela borda); segmentados viram grade de largura
  total com alvo de toque de 44px.
- **≤ 560px**: o toggle de 3 abas da página vira grade 2×2 (não cabia em 360px).
- `prefers-reduced-motion`, `pointer: coarse` e `@media print` cobertos.

## Estados

| Estado | Comportamento |
|---|---|
| Carregando (1ª vez) | Skeleton com a forma real (KPIs + barras + 2 painéis); alturas fixas, não aleatórias |
| Refetch (troca de preset/métrica) | Segura o render anterior a 60% de opacidade — sem pulo de altura, sem flash de skeleton |
| Zero falhas no período | Empty state shadow-UI (§4.7 DESIGN.md) com preview do gráfico |
| Erro da API | `.fd-error` com `role="alert"` |
| Range de 1 dia (`from == to`) | 1 ponto; o gráfico degenera numa barra mas não quebra |
| Dia com déficit e zero downtime | Conta como dia com falha (é o silent-gap) |

## Performance

2 queries. A Q1 sobre `daily_play_summary_for` num range de 90 dias é a mais
cara — é a mesma função que o `/insights` usa. Sem cache no backend;
`staleTime: 60s` no React Query, sem polling (admin investigando sob demanda).

O merge é O(dias × emissoras) em memória: no pior caso ~90 × 200 = 18k linhas.

## Não cobre (escopo intencionalmente fora)

- **Range acima de 90 dias** — teto do backend, alinhado com as outras abas
- **Comparação entre períodos** (julho vs junho lado a lado) — troca-se o preset
- **Quebra por emissora ou campanha dentro do gráfico** — é justamente o que as
  outras duas abas fazem; o clique na barra faz a ponte
- **Exportação CSV/PDF** — a tabela-gêmea dá o dado na tela; export não foi pedido
- **Downtime sem impacto comercial** — usar `/admin/overview` ou `/monitoring`
- **Alerta proativo por padrão detectado** — pull-only

## Deploy

Feature full-stack: **backend primeiro** (`scripts/deploy.sh` na VM), frontend
depois (push pra `master` publica no CF Pages). Ver a memória
`deploy-split-frontend-backend-ordering`. Sem migration.

Smoke test após o deploy do backend:

```bash
curl -s -o /dev/null -w '%{http_code}\n' \
  'https://<api>/v1/internal/admin/failures-daily?from=2026-07-01&to=2026-07-31'
# 401 = rota existe (falta auth). 404 = backend ainda não subiu.
```
