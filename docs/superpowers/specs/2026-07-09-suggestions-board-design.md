# Sugestões — Design (Central de demandas interna)

> Spec de brainstorming. Fonte da verdade do design da feature "Sugestões".
> Documentação operacional definitiva vai em `docs/features/suggestions-board.md` após implementar.

- **Data:** 2026-07-09
- **Autor:** Dereck (via Claude)
- **Branch:** `feat/suggestions-board`
- **Status:** aprovado no design, aguardando plano de implementação

---

## 1. Problema e objetivo

O dev da plataforma (Dereck, email `tatico3@hubradios.com`) precisa **coletar e gerenciar as demandas dos usuários dentro da própria plataforma**, sem depender de ferramenta externa (Trello etc.). O incômodo não é o kanban — é ter que **abrir outro software**. A régua: uma central tão boa que ele nunca queira sair da plataforma pra gerenciar demanda.

**Objetivo:** uma tela "Sugestões" na sidebar onde admins mandam pedidos detalhados (com tela-alvo, imagens, prioridade) e o dev gerencia tudo (triage, status, feedback, conversa), inclusive as próprias demandas.

## 2. Não-objetivos (YAGNI)

- **Sem acesso de cliente (viewer)** nesta versão. Admin-only. (Decisão: "só admin e ponto" — sem fundação pra cliente agora.)
- Sem integração com git/PR/issues externos.
- Sem SLA/automação de prazo.
- Sem e-mail no v1 (fica plugável pra depois, reusando o SMTP existente).
- Sem votação/curtida entre usuários (é ferramenta de dev, não fórum público).

## 3. Personas e gating

Um **único item "Sugestões"** na sidebar (bloco Administração, admin-only via `RequireRole roles={['admin']}`). O conteúdo ramifica por identidade:

| Persona | Quem | O que vê |
|---|---|---|
| **Autor** | qualquer admin/operator que **não** é o dev-email | *Minhas Sugestões*: cria, acompanha status, lê feedback do dev, conversa na thread — só as **próprias**. |
| **Dev (Central de Comando)** | usuário cujo email == `SUGGESTIONS_DEV_EMAIL` | Tudo de todos: triage, status, prioridade real, esforço, feedback, notas privadas, cria demandas próprias. |

**Gating imposto no servidor (não confiar no front):**
- Todas as rotas ficam sob `RequireRole("admin","operator")` (nenhum viewer entra).
- Dentro do handler, `isDev(ctx)` carrega o email do usuário (`users.Get(claims.UserID).Email`, porque o **JWT não carrega email** — só UserID/Role/ClientID) e compara com `SUGGESTIONS_DEV_EMAIL` (default `tatico3@hubradios.com`, configurável por env — mesmo efeito de "chumbado" mas testável e sem recompilar).
- `GET /suggestions`: dev → todas; autor → filtra `created_by = claims.UserID` **no servidor**.
- `GET/PATCH /suggestions/{id}`: autor só acessa as próprias; campos de gestão (`dev_priority`, `esforco`, `dev_notes`) e o PATCH de gestão são **dev-only** (403 caso contrário).
- `dev_notes` **nunca** vai no JSON de resposta pra um autor.

## 4. Modelo de dados (migration `0051_suggestions`)

Plano de tabelas — plain tables (não particionadas), estilo da casa (`uuid_generate_v4()`, `TIMESTAMPTZ DEFAULT NOW()`, FKs pra `users(id)`, `CHECK` pra enums).

### 4.1 `suggestions` (núcleo)
| coluna | tipo | nota |
|---|---|---|
| `id` | UUID PK | `DEFAULT uuid_generate_v4()` |
| `ref_num` | BIGINT | sequência humana (#42) — `GENERATED ALWAYS AS IDENTITY` ou sequence dedicada; **UNIQUE** |
| `created_by` | UUID | `REFERENCES users(id) ON DELETE SET NULL` |
| `title` | TEXT NOT NULL | |
| `description` | TEXT NOT NULL | markdown leve permitido |
| `type` | TEXT NOT NULL | `CHECK (type IN ('bug','melhoria','feature','duvida'))` |
| `target_screen` | TEXT | tela/área alvo (rota do app ou texto livre) |
| `requester_priority` | TEXT NOT NULL | `CHECK (... IN ('baixa','media','alta'))` |
| `status` | TEXT NOT NULL | `DEFAULT 'nova'`, `CHECK (... IN ('nova','em_analise','aceita','em_progresso','concluida','recusada'))` |
| `dev_priority` | TEXT | nullable até triar; `CHECK (... IN ('urgente','alta','media','baixa'))` |
| `effort` | TEXT | nullable; `CHECK (... IN ('P','M','G'))` (pequeno/médio/grande) |
| `dev_feedback` | TEXT | o retorno que o **autor lê** em destaque |
| `dev_notes` | TEXT | privado do dev; nunca serializado pro autor |
| `awaiting_author` | BOOLEAN NOT NULL `DEFAULT false` | flag ortogonal "bola com o autor" |
| `resolved_at` | TIMESTAMPTZ | preenchido ao virar concluída/recusada |
| `created_at` / `updated_at` | TIMESTAMPTZ NOT NULL `DEFAULT NOW()` | |

Índices: `(created_by)`, `(status)`, `(updated_at DESC)`.

### 4.2 `suggestion_comments` (thread)
`id` UUID PK · `suggestion_id` UUID `REFERENCES suggestions(id) ON DELETE CASCADE` · `author_id` UUID `REFERENCES users(id) ON DELETE SET NULL` · `body` TEXT NOT NULL · `created_at` TIMESTAMPTZ. Índice `(suggestion_id, created_at)`.

### 4.3 `suggestion_attachments` (imagens S3)
`id` UUID PK · `suggestion_id` UUID `REFERENCES suggestions(id) ON DELETE CASCADE` · `comment_id` UUID NULL `REFERENCES suggestion_comments(id) ON DELETE CASCADE` (NULL = anexo da raiz) · `storage_key` TEXT NOT NULL · `content_type` TEXT NOT NULL · `size_bytes` BIGINT NOT NULL · `uploaded_by` UUID · `created_at`. Índice `(suggestion_id)`.

### 4.4 `suggestion_events` (timeline de atividade)
`id` UUID PK · `suggestion_id` UUID CASCADE · `actor_id` UUID · `event_type` TEXT `CHECK (... IN ('created','status_changed','priority_changed','feedback_given','commented','attachment_added','reopened'))` · `from_value` TEXT NULL · `to_value` TEXT NULL · `created_at`. Alimenta o histórico "contado" no detalhe.

### 4.5 `suggestion_reads` (não-lido por usuário)
`user_id` UUID · `suggestion_id` UUID CASCADE · `last_read_at` TIMESTAMPTZ NOT NULL · PK `(user_id, suggestion_id)`. Não-lido = `max(último comment/event) > last_read_at`.

## 5. Ciclo de status e prioridade

```
nova ──▶ em_analise ──▶ aceita ──▶ em_progresso ──▶ concluida
  └──────────────┴────────────┴─────────────┴──────▶ recusada (com motivo em dev_feedback)
concluida/recusada ──▶ (reopened) em_analise
```

- **`awaiting_author`** é flag independente do status (pílula "Aguardando você/autor"), setada quando o dev faz uma pergunta e a bola vira pro autor; some quando o autor responde.
- **Prioridade dupla:** `requester_priority` (autor pede) vs `dev_priority` (dev define na triage). São exibidas lado a lado — o autor vê a sua; o dev vê ambas.
- **Board (dev):** colunas = `nova · em_analise · aceita · em_progresso · concluida`. `recusada` fora do board (acessível por filtro), pra não poluir.

## 6. Contratos de API (`/v1/internal`, chi + pgx, padrão materials/users)

Todas sob `RequireRole("admin","operator")`. Gating fino (dev vs autor) dentro do handler.

| Método | Rota | Quem | Descrição |
|---|---|---|---|
| `POST` | `/suggestions` | admin | cria (`title,type,target_screen,description,requester_priority`) → retorna a sugestão + `ref_num` |
| `GET` | `/suggestions` | admin | lista. dev=todas (filtros `status,type,priority,author_id,q,unread,sort,page`); autor=próprias |
| `GET` | `/suggestions/{id}` | autor/dev | detalhe: comments + attachments (com presigned URL) + events + campos. `dev_notes` só se dev |
| `PATCH` | `/suggestions/{id}` | **dev** | `status,dev_priority,effort,dev_notes,dev_feedback,awaiting_author` — gera events, seta `resolved_at` |
| `POST` | `/suggestions/{id}/comments` | autor/dev | adiciona comentário (limpa `awaiting_author` se o autor responde) |
| `POST` | `/suggestions/{id}/attachments` | autor(own)/dev | multipart → `Storage.Put` (allow-list de MIME de imagem) → grava key/size |
| `POST` | `/suggestions/{id}/read` | autor/dev | upsert `last_read_at` |
| `GET` | `/suggestions/attachments/{id}/url` | autor/dev | presigned GET (5 min), igual evidências |
| `GET` | `/suggestions/summary` | **dev** | KPIs do header (contagem por status, mais-velha-em-aberto) |
| `GET` | `/suggestions/unread-count` | admin | inteiro pro badge da sidebar (escopo por persona) |

- Upload de imagem reusa `storage.Client.Put` + `PresignGet` (S3/MinIO), com allow-list de MIME (`image/png,image/jpeg,image/webp,image/gif`), limite ~10MB/arquivo, key `suggestions/{suggestion_id}/{uuid}.{ext}`.
- Respostas: `writeJSON`; listas embrulhadas em `{"data": [...]}` com slices nil normalizados pra `[]`.
- Repo `workers/internal/catalog/suggestions.go`; handler `workers/internal/api/handlers/suggestions.go`; wiring em `router.go` (`Deps` + grupo) e `cmd/api/main.go`. **Sem CLI novo** → sem alteração no `workers.Dockerfile`.

## 7. Frontend

- Rota `/admin/suggestions` em `App.jsx` sob `RequireRole roles={['admin']}` (antes do catch-all).
- Item + `IconSuggestions()` em `AdminNav` (`Sidebar.jsx`), com **badge de não-lido**.
- Página `frontend/src/pages/AdminSuggestionsPage.jsx` (+ `.css` colocado) ramifica por `useAuth().user.email === devEmail`:
  - `<CentralDeComando/>` (dev): lista densa (padrão) + toggle **Board**; header com KPIs (Inbox, Em progresso, Concluídas no mês, aging); filtros/busca; triage inline (status/prioridade sem abrir) + ação em lote; drawer de detalhe (descrição, imagens/lightbox, thread, timeline, campos de gestão, feedback); botão "Nova demanda".
  - `<MinhasSugestoes/>` (autor): botão "Nova sugestão"; cards (tipo/status/prioridade/última atividade + bolinha não-lido); detalhe com feedback do dev **em destaque** + thread pra responder; empty state convidativo.
  - Componentes compartilhados: `SuggestionDetail`, `SuggestionCreateModal`, `SuggestionThread`, pills (`StatusPill`, `TypePill`, `PriorityPill`), `AttachmentLightbox`, `ClipboardPasteZone`.
- `devEmail` no front vem de `import.meta.env.VITE_SUGGESTIONS_DEV_EMAIL` (default `tatico3@hubradios.com`) — espelha o env do backend.
- Hooks novos em `api/hooks.js`: `useSuggestions`, `useSuggestion`, `useCreateSuggestion`, `useUpdateSuggestion`, `useAddComment`, `useUploadSuggestionAttachment`, `useMarkSuggestionRead`, `useSuggestionsSummary`, `useSuggestionsUnread`. react-query, invalidando as queryKeys certas.
- **Toque "não-é-outro-software":** colar print do clipboard (Ctrl+V) anexa screenshot direto no form/thread.

## 8. Notificações (v1 enxuto)

- Badge de não-lido no item "Sugestões" da sidebar via `useSuggestionsUnread` (autor: atividade do dev nas minhas; dev: novas + respostas de autores).
- Bolinhas de não-lido por card/linha dentro da tela.
- E-mail e integração com o sininho admin: **fora do v1**, plugável depois (reusa SMTP + tabela de notificação existentes).

## 9. Intenção visual/UX (para /impeccable + /pro-system-ui)

O esqueleto acima é a lógica; a alma vem no passo de design:
- Hierarquia que respira — nada de campos empilhados/agrupados sem ar; densidade com ritmo.
- Board e lista **ambos first-class**, coexistindo sem parecer dois apps colados.
- Timeline de atividade que **se lê como história**, não como log cru.
- Colar-print, micro-motion na marca `#E81E75`, transições de status suaves.
- Empty states com personalidade. Zero "AI slop" (sem card genérico, sem cinza morto).
- Respeitar o design system: `.btn`, `RSelect`, `.field`, modais via portal, tokens do `index.css`.

## 10. Testes e deploy (checklist CLAUDE.md)

- **Migration (§4.8):** `0051` é `CREATE TABLE` puro (sem backfill dependente de dados) → baixo risco; ainda assim o `shadow_migration_test` do `deploy.sh` roda sobre cópia de prod. Testar `up`/`down` local.
- **Cross-compile (§6.1):** `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` tem que passar.
- **Boot da API (§6.5):** métricas Prometheus (se houver) com nome único; maps inicializados no construtor do repo.
- **Sem CLI novo (§6.7):** nada a adicionar no Dockerfile.
- **Frontend (§5):** só adiciona código, sem tocar `package*.json` → sem risco de lockfile podado. (Se precisar de lib nova, seguir regra 5.)
- **Testes:** `go test ./...` no pacote novo; testes de gating (autor não vê alheio; `dev_notes` não vaza; viewer 403).

## 11. Documentação (para Claudes do futuro)

- `docs/features/suggestions-board.md` com header YAML (`status: implementado`, `codigo-relacionado`), personas, gating por email, modelo de dados, endpoints, ciclo de status, decisões.
- Entrada no **mapa de consulta do `CLAUDE.md`** ("Sugestões / central de demandas do dev → docs/features/suggestions-board.md") pra quando o Dereck disser "sugestões" o Claude já saber tudo.

## 12. Riscos / decisões em aberto

- **Email do dev por env vs chumbado:** decidido por env (`SUGGESTIONS_DEV_EMAIL`), default `tatico3@hubradios.com`. Se o env sumir em prod, cai no default — comportamento seguro.
- **`ref_num` humano:** sequence dedicada pra sobreviver a deletes; cosmético mas ajuda o dev a referenciar ("sugestão #42").
- **Markdown na descrição:** render leve (negrito/lista/link/código). Sanitizar no front pra evitar XSS.
