---
status: implementado
ultima-verificacao: 2026-07-24
codigo-relacionado:
  - frontend/src/pages/MaterialsPage.jsx
  - frontend/src/components/MaterialPlaybackList.jsx
  - frontend/src/components/DistributionGrid.jsx
  - frontend/src/utils/dates.js
  - frontend/src/api/hooks.js
  - workers/internal/api/handlers/materials.go
  - workers/internal/catalog/materials.go
---

# Tela "Materiais" (`/materials`)

Tela da seção **Veiculação**, acessível a **admin e cliente** (cliente só com
as próprias campanhas/materiais — escopo aplicado pelo backend). Mostra, por
campanha: **todos os materiais** que ela tem (programados ou não, com play +
download) e **quanto está programado pra rodar em cada emissora**.

> Spec: [`docs/superpowers/specs/2026-05-27-materials-page-design.md`](../superpowers/specs/2026-05-27-materials-page-design.md)
> Plano: [`docs/superpowers/plans/2026-05-27-materials-page.md`](../superpowers/plans/2026-05-27-materials-page.md)

## Diferença pra /detections

A [`/detections`](detections-view.md) mostra o **veiculado** (verde/vermelho/
bônus) contra o plano, com R$ e impactos. A `/materials` mostra **só o
programado** (células cinza), **sem** veiculados, **sem** R$, **sem** impactos.
O foco é o material e o volume programado por emissora.

## Filtros (igual /detections)

Competência (mês) → Campanha → Período (início→fim). A campanha lista só as
que cruzam a competência. O período faz o narrow client-side da grade. Os
helpers de data desse fluxo vivem em `utils/dates.js` (compartilhados com a
/detections).

Os bounds dos seletores De/Até são a **campanha inteira** (`campaignRangeISO`),
não o mês — dá pra estender o range pra outros meses da campanha e a grade
renderiza o span via o override `visibleStart`/`visibleEnd` do `DistributionGrid`
(mesma mecânica da [/detections](detections-view.md)). O default segue mensal.

## Resumo do plano

Faixa no topo (família do `CoverageSummary`, sem cobertura/R$): figura-líder
**Σ inserções programadas** no período + proporção **materiais programados /
total** (barra) + **emissoras com plano**.

## Painel de materiais

Componente `MaterialPlaybackList`: lista **todos** os materiais vinculados à
campanha, agrupados por tipo. Cada item: duração, status de fingerprint,
**selo "programado" / "sem programação"** (programado = o tipo do material
aparece em alguma regra de distribuição), play/pause e download. O áudio é
buscado via `GET /materials/{id}/audio` (blob autenticado → object URL). Só um
material toca por vez.

### Renomear material (inline)

Único campo editável nesta tela: o **nome** do material. Botão de lápis na
linha (só pra **admin/operator** — `canEdit={isAdmin}`) troca o título por um
input: **Enter** salva, **Esc** cancela, nome vazio-após-trim não salva, nome
inalterado só fecha (sem chamada à API). Existe pra corrigir material subido
com título errado sem ter que re-uploadar o áudio.

Todo o resto (tipo, texto do comercial, emissoras-alvo, vínculo com campanha)
continua editável **só no wizard de campanha** — esta tela não virou um CRUD.

**Por que renomear é seguro:** `materials.title` é rótulo puro. Nada em
matching, fingerprint, categorização ou atribuição lê esse campo, e todas as
telas que mostram o nome do material resolvem por `JOIN` em `materials`
(nenhuma cópia denormalizada). Logo: **não** dispara recategorização (ao
contrário de trocar o tipo, que chama `RecategorizeForMaterial`) e **não**
exige regerar fingerprint. Renomear muda só o que o usuário lê.

| | |
|---|---|
| Endpoint | `PATCH /v1/internal/materials/{id}/title` |
| Body | `{"title": "<novo nome>"}` |
| Respostas | `204` ok · `400` id inválido / JSON inválido / título vazio-após-trim · `404` id inexistente |
| Role | admin/operator (grupo do router; viewer/cliente leva 403 — por isso o lápis nem aparece pra ele) |
| Hook | `useUpdateMaterialTitle` — invalida `materials`, `detections`, `detection`, `material-aggregate` (as queries que **exibem** o nome) |

## Grade de plano

`DistributionGrid` com `summary="plan"` e `capAtToday={false}` (mostra o
período inteiro, inclusive dias futuros — é um plano). `cellData` carrega só
`expected` → células cinza. A coluna de total por emissora vira "N programados"
(Σ expected da emissora), sem R$/impactos. Paginada por emissora (5/10/15).

## DistributionGrid — prop `summary`

| Valor | Resumo por linha | Total por emissora |
|-------|------------------|--------------------|
| `'full'` (default) | 6 pílulas (programada/veiculada/bônus/déficit/fora-faixa/fora-data) | impactos + R$ |
| `'plan'` | só pílula cinza (programado) | "N programados" (Σ expected) |

Default `'full'` preserva /detections e o wizard sem mudança. Layout de colunas
idêntico nos dois modos — só o conteúdo das células de resumo muda.

## Role gating

Rota sem `RequireRole` (igual /detections). Backend escopa: cliente recebe só
as próprias campanhas (`useCampaigns`) e materiais (`useMaterials`). O CTA
"Editar campanha" (empty state sem materiais/regras) e o lápis de renomear só
aparecem pra admin/operator.
Diferente da /detections, as emissoras (`useStations`) são buscadas pra todos
os papéis — a grade precisa dos objetos de emissora pra renderizar linhas
também pro cliente.

## Estados

- **Empty** (sem competência / sem campanha / campanha sem materiais nem
  regras): card central com `FlowStepper` + ghost backdrop (shadow UI).
- **Loading**: skeleton que replica resumo + painel + grade (sem spinner).
- **Materiais sem programação**: o painel aparece normal; a grade mostra nota
  neutra "nada programado".

## Limitações

- Sem export CSV/PDF (isso é da /detections via `CampaignReportsMenu`).
- Busca é substring case-insensitive (mesma da /detections), não fuzzy.
