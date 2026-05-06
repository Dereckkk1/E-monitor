# Detections Calendar View — Design

**Data:** 2026-05-06
**Página afetada:** `/detections` ([frontend/src/pages/DetectionsPage.jsx](../../../frontend/src/pages/DetectionsPage.jsx))
**Escopo:** apenas frontend. Sem mudança de backend, schema ou API.

## 1. Objetivo

Substituir a tabela plana de detecções por uma **grade calendário** onde:

- linhas = emissoras-alvo da campanha selecionada
- colunas = dias do período escolhido
- cada célula = quantidade de veiculações daquela emissora naquele dia
- clicar na célula abre um **modal compacto** listando as detecções daquele dia/emissora

Esta é a versão V1, focada em visualização do **veiculado**. A camada de "programado vs faixa" do mockup do cliente (PROGRAMADO / DENTRO DA FAIXA / FORA DA FAIXA / FORA DA DATA / DEFICIT / EXTRAS) **fica fora deste escopo** — depende de integração futura com o sistema externo de plano de mídia, ainda não definida.

## 2. Não-objetivos

- Conceito de "spots programados" (plano de mídia).
- Cores/legenda do mockup baseadas em comparação com o programado.
- Faixas horárias programadas, deficit/extras, rating de estrelas, PI, "Spot 30".
- Mudanças no modelo de dados, migrations ou endpoints novos.

## 3. Fluxo do usuário

1. Usuário entra em `/detections`.
2. Seleciona uma campanha no dropdown (já existe).
3. Escolhe período via presets (Hoje / 7d / 30d) ou date pickers (já existe — **mantido como está**).
4. Vê uma grade com **todas as emissoras-alvo da campanha** nas linhas e os dias do período nas colunas. Cada célula traz a contagem de veiculações.
5. Clica numa célula com contagem ≥ 1 → abre modal listando as detecções daquele dia/emissora, com áudio inline.

Empty states (sem campanha selecionada / sem detecções no período) permanecem como hoje.

## 4. Componentes

### 4.1 `DetectionsCalendar`

Substitui o bloco `<table className="table">` atual em `DetectionsPage.jsx` (linhas 292–333). Recebe:

- `stations: Station[]` — lista das emissoras-alvo da campanha (resolvida via `useStations()` + `campaign.target_stations`).
- `detections: Detection[]` — lista plana já carregada por `useDetections()`.
- `period: { start: Date, end: Date }` — período selecionado.
- `onCellClick: (stationId, day) => void` — abre o modal.

Lógica interna:

- Gera o array de dias entre `period.start` e `period.end` (inclusive), em fuso `America/Sao_Paulo`.
- Bucketiza `detections` num mapa `Map<string, Map<string, Detection[]>>` chaveado por `station_id` → `YYYY-MM-DD` → `Detection[]`.
- Para cada emissora-alvo renderiza uma linha; emissoras sem detecções no período aparecem com células zeradas.

### 4.2 `StationRowHeader`

Lateral esquerda fixa de cada linha:

- `<StationAvatar station={station} size={40} />` (já existe)
- Nome formatado: `${name} - ${band} (${frequency_mhz})` quando `frequency_mhz` existir; senão só `${name} - ${band}`.
- Linha menor: `${city} / ${state}` quando ambos existirem; senão `—`.

Sem PI, sem estrelas, sem "Spot 30".

### 4.3 `DayCell`

Célula numérica:

- `count === 0` → renderiza um traço discreto (`—`), sem hover/click.
- `count > 0` → bloco clicável com o número, hover destacado, cursor pointer.

Visual usa as mesmas variáveis de tema já existentes (`var(--c-...)`); sem cores semânticas de "dentro/fora da faixa".

### 4.4 `DayDetailModal`

Modal compacto, largura ~640px, aberto via state local em `DetectionsPage`. Conteúdo:

- **Header:** `StationAvatar` + nome formatado + `city / state`, e a data (`DD/MM/YYYY`) à direita ou centralizada.
- **Subtítulo:** `N veiculações`.
- **Lista:** uma linha por detecção, com `[▶] HH:MM:SS  NOME DO COMERCIAL`. O play usa o componente `AudioPlayer` já existente (mesmo `src=/v1/internal/detections/{id}/evidence` da tabela atual). Detecções sem evidência (`evidence_status !== 'available'`) mostram texto cinza no lugar do botão.
- **Sem** bloco "Resumo dos Status", **sem** "Faixas Horárias Programadas" — esses só fazem sentido com o programado.

Fechamento por clique no overlay, botão X, ou tecla ESC. Não há modal genérico reutilizável no projeto (o `ConfirmModal` existente está acoplado ao shim de `window.confirm`), então este componente nasce próprio, seguindo o mesmo padrão visual: `createPortal` para `document.body`, backdrop com classe local, card centralizado, listener de `keydown` para ESC.

## 5. Dados

Não há mudança de API. O frontend já tem o que precisa:

- `useCampaigns()` retorna `target_stations: UUID[]` por campanha.
- `useStations()` retorna o catálogo completo de emissoras.
- `useDetections({ campaign_id, start_date, end_date })` retorna a lista plana já filtrada.

No `DetectionsPage`, depois de selecionar a campanha:

1. Resolve a campanha selecionada → pega `target_stations`.
2. Cruza com `useStations()` para obter os objetos completos das emissoras (logo, nome, banda, frequência, cidade, UF).
3. Passa para `DetectionsCalendar` junto com `detections` e `period`.

Bucketização por dia usa o fuso `America/Sao_Paulo` (consistente com o resto da página: `formatDateTime` já usa esse timezone).

## 6. Layout e responsividade

- A grade tem **coluna esquerda fixa** (header da emissora, ~280px) e **área de dias com scroll horizontal** quando o número de colunas excede a largura disponível.
- Cada coluna de dia: largura mínima ~64px, header com `DD/MM` em cima e `SEX/SAB/...` embaixo (em pt-BR, abreviado).
- Linha tem altura fixa (~64px) para alinhar com o avatar.
- Empty (sem campanha) e loading (skeleton) reaproveitam os componentes já existentes na página.

## 7. Limite atual e implicações

O `useDetections` hoje passa `limit: 200` (linha 148 de `DetectionsPage.jsx`). Para um período de 30 dias com várias emissoras, isso pode truncar o resultado e fazer a contagem da grade ficar incorreta.

**Decisão V1:** subir o `limit` para `5000` no filtro deste contexto. Suficiente para a faixa de campanhas atuais sem mudança de backend. Se virar gargalo, futuro endpoint de "agregação por dia/emissora" entra em escopo separado.

## 8. Testes manuais (golden path)

1. Abrir `/detections` sem campanha → empty atual mantido.
2. Selecionar campanha com poucas emissoras e poucas detecções no preset "Hoje" → grade com 1 coluna, contagens batem com a lista hoje.
3. Trocar para "30 dias" → 30 colunas, scroll horizontal, emissoras-alvo zeradas aparecem como `—`.
4. Clicar em célula com contagem ≥ 1 → modal abre, lista correta de detecções, play funciona.
5. Clicar em célula com 0 → nada acontece.
6. Trocar de campanha → grade re-renderiza com as novas emissoras-alvo.

## 9. Arquivos esperados

- **Modificado:** [frontend/src/pages/DetectionsPage.jsx](../../../frontend/src/pages/DetectionsPage.jsx) — substitui o bloco da tabela pela grade; adiciona estado do modal.
- **Novo:** `frontend/src/components/DetectionsCalendar.jsx` — grade + DayCell + StationRowHeader.
- **Novo:** `frontend/src/components/DayDetailModal.jsx` — modal de detalhamento diário.
- **Modificado:** [frontend/src/index.css](../../../frontend/src/index.css) — estilos da grade e do modal, seguindo as variáveis de tema existentes.

Sem mudanças em `workers/`, migrations ou hooks da API além de eventualmente subir o `limit`.

## 10. Documentação

Após implementar, criar `docs/detections-calendar.md` descrevendo a página em modo operacional (conforme regra do `CLAUDE.md`: documentação de funcionalidade vai em `/docs`, nunca em `plano_implementacao.md`).
