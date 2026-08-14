---
status: implementado
ultima-verificacao: 2026-08-14
codigo-relacionado:
  - frontend/src/components/StationDetailModal.jsx
  - frontend/src/components/StationDetailModal.css
  - frontend/src/pages/StationsPage.jsx
  - frontend/src/utils/stationStatus.js
  - workers/internal/api/router.go
---

# Ficha da emissora (read-only) em `/stations`

Painel read-only com os dados cadastrais de uma emissora, aberto ao clicar em
qualquer linha da listagem `/stations`. Disponível para **admin e cliente**.

## Problema que resolve

Antes, `/stations` listava as emissoras mas não expunha nenhum dado além do que
cabia na linha (nome, banda/frequência, cidade, 3 categorias, PMM, status). O
único lugar onde as informações completas apareciam era `/stations/:id/edit` —
rota **admin-only**. Na prática o usuário Cliente enxergava o catálogo mas não
conseguia ver nada sobre cada emissora.

## Comportamento

Clique (ou `Enter`/`Espaço` com foco no teclado) em qualquer linha abre a ficha.
Os botões dentro da linha — ouvir ao vivo e editar — chamam `stopPropagation()`,
então continuam funcionando sem abrir o modal.

A ficha renderiza a partir do objeto que a listagem **já carregou**: `GET
/stations` devolve `metadata` inteiro (ver `stationSelectCols` em
`workers/internal/catalog/stations.go`), então nenhuma request adicional é
disparada ao abrir.

### Seções

| Seção | Conteúdo | Condição |
|---|---|---|
| Header | avatar, nome, banda/frequência, cidade/UF, selo de status, categorias | sempre |
| KPIs | PMM, população alcançada, nº de estados, nº de cidades, ano de fundação | cada card só aparece se o dado existir |
| Categorias | todas as categorias em chips | `meta.categories` não vazio |
| Perfil de audiência | gênero, faixa etária e classe social em barras percentuais | algum percentual > 0 |
| Cobertura | estados + cidades em chips (12 primeiras, com "mostrar todas") | `coverage_states`/`coverage_cities` não vazios |
| Contato | website (todos), e-mail comercial e domínio do stream (admin) | ver gating abaixo |
| Dados cadastrais | razão social, nome fantasia, CNPJ, potência, classe de antena | **admin apenas** |

Sem nenhum desses dados no cadastro, a ficha mostra "Sem informações adicionais
cadastradas para esta emissora."

### Gating por papel

| Elemento | Admin | Cliente |
|---|---|---|
| Ficha (abrir pela linha) | ✅ | ✅ |
| Perfil de audiência, cobertura, PMM, população, website | ✅ | ✅ |
| E-mail comercial, domínio do stream | ✅ | ❌ |
| Razão social / nome fantasia / CNPJ / potência / classe de antena | ✅ | ❌ |
| Botão "Editar" (leva a `/stations/:id/edit`) | ✅ | ❌ |

O gating é de **apresentação**, não de autorização: `GET /stations` e
`GET /stations/{id}` já são rotas viewer-friendly no `router.go` (subgrupo A) e
devolvem o registro completo para qualquer papel autenticado. Nada de novo foi
exposto no backend — nenhuma alteração de API foi necessária para esta feature.
Se algum desses campos passar a ser sigiloso de verdade, o corte precisa ser
feito no handler, não aqui.

## Notas de implementação

- `STATUS_META` (rótulos de `monitoring_status`) saiu de `StationsPage.jsx` para
  `frontend/src/utils/stationStatus.js`, já que listagem e ficha renderizam o
  mesmo selo. Use `statusMetaFor(status)` — ele tem o fallback para status
  desconhecido embutido.
- O modal fecha por `Esc`, clique no overlay ou no botão ✕.
- Estilos em `StationDetailModal.css`, todos sobre os design tokens do
  [design system](../architecture/frontend-design-system.md).

## Ajustes de largura na barra de filtros

No mesmo trabalho, `/stations` teve as larguras corrigidas:

- `.stations-search-dropdown` tinha `max-width: 420px`, deixando o campo de
  busca curto e um vão morto à direita em telas largas. Agora ocupa toda a
  largura sobrando da barra (o grupo AM/FM é o único elemento de largura fixa).
- O `flex-basis` é mantido em `auto` **de propósito**: abaixo de 640px a barra
  vira `flex-direction: column` e um basis em px seria interpretado como
  **altura**, abrindo um vão de ~260px entre o campo e as abas.
- Abaixo de 640px o grupo AM/FM passa a ocupar a linha inteira, com as três abas
  dividindo o espaço igualmente.
- `.modal-body .cluster > .field` ganhou `min-width: 0` para que campos lado a
  lado (Banda+Frequência, Cidade+UF no modal "Nova emissora") preencham a fatia
  inteira em vez de parar no `min-content` do input.

Larguras medidas após o ajuste (barra × campo × abas, em px):
`1440 → 1136 × 942 × 182` · `1024 → 720 × 526 × 182` · `700 → 668 × 474 × 182` ·
`420 → 388 × 388 × 388` (empilhado).
