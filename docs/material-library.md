# Material Library — Gestao de Materiais e Tipos

Documenta a biblioteca de materiais por cliente e o registro global de tipos. Introduzido pelo Plano 1 — Foundations.

> Spec arquitetural: [`docs/superpowers/specs/2026-05-11-campaign-wizard-design.md`](superpowers/specs/2026-05-11-campaign-wizard-design.md)

## Modelo

- **`material_types`** — registro global de tipos (Spot 30s, Testemunhal, etc). Gerenciado pelo admin via tela `/material-types` (futura)
- **`materials`** — catalogo de audios. Cada material pertence a UM cliente (`client_id`). Reusavel entre campanhas desse cliente
- **`campaign_materials`** — link N:N. Define que um material `M` esta vinculado a campanha `C` com emissoras `[s1, s2, ...]`

## Por que decuplado da campanha?

No modelo antigo (`commercials`), cada upload de audio criava um registro tied a UMA campanha. Reutilizar o mesmo MP3 em outra campanha exigia upload duplicado, gerando fingerprints duplicados e poluindo o indice.

No modelo novo:
- Subir um material vai pra biblioteca do cliente
- Vincular a uma campanha cria row em `campaign_materials`
- Vincular a outra campanha cria outra row, mesmo `material_id`, mesmo fingerprint

## Backward compat com `commercials`

A tabela `commercials` permanece. Cada commercial existente foi migrado pra `materials` com o MESMO UUID (migration 0016). Isso preserva:
- `detections.commercial_id` continua valido (aponta pro mesmo UUID que agora tambem existe em `materials`)
- Codigo antigo que le `commercials` continua funcionando

Plano futuro: deprecar `commercials.target_stations` e `commercials.campaign_id` em migration nova (apos frontend novo estar 100% em uso). Ver follow-up F-90.

## Tipos de material

Os 6 seeds da migration 0016:

| Tipo         | Cor       |
|--------------|-----------|
| Spot 30s     | `#3b82f6` |
| Spot 60s     | `#0ea5e9` |
| Testemunhal  | `#8b5cf6` |
| Citacao      | `#14b8a6` |
| Vinheta      | `#f59e0b` |
| Jingle       | `#ec4899` |

Operador pode criar tipos customizados via `POST /v1/internal/material-types`.

## Endpoints

| Metodo | Rota | Descricao |
|--------|------|-----------|
| GET    | `/v1/internal/material-types` | Lista tipos |
| POST   | `/v1/internal/material-types` | Cria tipo |
| PUT    | `/v1/internal/material-types/{id}` | Atualiza tipo |
| DELETE | `/v1/internal/material-types/{id}` | Remove tipo |
| GET    | `/v1/internal/clients/{id}/materials?q=` | Biblioteca do cliente (busca por titulo) |
| POST   | `/v1/internal/materials` | Upload (multipart: client_id, title, type_id, audio) |
| GET    | `/v1/internal/materials/{id}` | Detalhe |
| PATCH  | `/v1/internal/materials/{id}/type` | Muda o tipo |
| DELETE | `/v1/internal/materials/{id}` | Remove |
| POST   | `/v1/internal/campaigns/{id}/materials` | Vincula material a campanha |
| GET    | `/v1/internal/campaigns/{id}/materials` | Lista materiais vinculados |
| PUT    | `/v1/internal/campaigns/{id}/materials/{mid}/stations` | Atualiza emissoras do vinculo |
| DELETE | `/v1/internal/campaigns/{id}/materials/{mid}` | Desvincula |

## Dedup

A migration 0016 **nao** forca `UNIQUE(client_id, master_sha256)`. Isso e intencional — duplicatas existentes em `commercials` foram migradas como materiais separados. A interface do operador deve permitir mesclar duplicatas manualmente (funcionalidade futura). Em algum momento, constraint pode ser adicionada via migration nova apos limpeza manual. Ver follow-up F-87.
