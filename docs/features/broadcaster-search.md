# Padrão de busca de broadcasters

Padrão único de busca de emissoras usado em todas as telas que filtram broadcasters. Importado do `/marketplace` do E-radios (referência: `signalads-frontend/src/pages/Marketplace` + `productController.ts:668`) para garantir que o usuário tenha o mesmo comportamento em qualquer lugar do produto que envolva pesquisa de emissora.

## Comportamento

- **Multi-token AND, multi-field OR.** A query é dividida por whitespace. Cada token precisa dar match em pelo menos um dos campos pesquisados — tokens diferentes podem casar com campos diferentes. Exemplo: `joinville fm` retorna emissoras FM de Joinville (token `joinville` casa com `city`, token `fm` casa com `band`), não emissoras AM que tenham "Joinville" no nome.
- **Case-insensitive.** Backend via `ILIKE`; frontend via `toLowerCase`.
- **Accent-insensitive no client-side.** `normalize('NFD')` + remoção de combining marks (`̀-ͯ`). `sao paulo` casa com `São Paulo`. No backend ainda é só case-insensitive — ver [Pendências](#pendências).
- **Tokens curtos descartados.** Tokens com menos de 2 caracteres são ignorados, exceto se contiverem dígito (mantém `5`, `7.5`, `91.3`).
- **Cobertura ≠ localização.** O campo `metadata` (que contém `coverage_cities`/`coverage_states`) **nunca** entra no haystack de busca. Buscar por uma cidade traz emissoras *daquela* cidade, não emissoras que *cobrem* aquela cidade. Esse foi o bug original — ver [Histórico](#histórico).

## Campos pesquisados

| Campo | Notas |
|-------|-------|
| `name` | Nome da emissora |
| `city` | Cidade física da emissora |
| `state` | UF |
| `band` | `FM` / `AM` |
| `frequency_mhz` | Convertido para texto, NULL vira `''` |

`metadata`, `coverage_cities`, `coverage_states`, `categories`, etc. **não** são pesquisados.

## Onde aplicar

Toda nova tela que listar/filtrar emissoras deve usar este padrão. Hoje:

| Tela | Onde mora o filtro | Tipo |
|------|-------|------|
| `/stations` | Backend (`q` query param) | Server-side |
| `/campaigns` (autocomplete de emissoras) | Backend (`q` query param) | Server-side |
| `/monitoring` | Client-side em [`MonitoringPage.jsx`](../frontend/src/pages/MonitoringPage.jsx) | `useStreamHealth` retorna lista completa |
| `/detections` | Client-side em [`DetectionsPage.jsx`](../frontend/src/pages/DetectionsPage.jsx) | filtra `target_stations` da campanha selecionada |

## Como reusar

### Frontend (client-side)

Helper em [`frontend/src/utils/search.js`](../frontend/src/utils/search.js):

```js
import { tokenize, matchesAllTokens } from '../utils/search'

const tokens = tokenize(searchInput)
const fields = [
  'name',
  'city',
  'state',
  'band',
  s => s.frequency_mhz != null ? String(s.frequency_mhz) : '',
]
const filtered = stations.filter(s => matchesAllTokens(s, fields, tokens))
```

`fields` aceita string (lookup direto em `item[field]`) ou função (extractor — útil para campos numéricos ou aninhados). Quando `tokens.length === 0`, `matchesAllTokens` devolve `true` (sem busca = não filtra).

### Backend (server-side)

Implementado em [`workers/internal/catalog/stations.go`](../workers/internal/catalog/stations.go) na função `List`. Cada token vira um `WHERE (name ILIKE … OR city ILIKE … OR …)` separado, todos combinados por `AND`. Para outras tabelas que precisem do mesmo padrão, replicar a estrutura — não há helper genérico hoje.

## Histórico

- **2026-05-08** — Bug reportado: busca em `/campaigns` retornava emissoras erradas. Causa raiz: backend incluía `metadata::text ILIKE` no WHERE, e como `metadata` guarda `coverage_cities`/`coverage_states`, buscar por `joinville` casava com qualquer emissora que cobrisse Joinville. Removido. `/monitoring` foi expandido de 3 campos para 5 com multi-token. `/detections` ganhou input de busca (não tinha).

## Pendências

- **Acento-insensibilidade no backend.** Hoje só client-side normaliza acentos. Para paridade total com E-radios (que usa `unaccent`-like regex no Mongo), precisaria habilitar `CREATE EXTENSION unaccent` e trocar `ILIKE` por `unaccent(field) ILIKE unaccent($n)`. Não é bloqueante: dados de cidade no Brasil são razoavelmente canônicos no banco, então `joinville` (sem acento) já casa com `Joinville` (sem acento) no `city`.
