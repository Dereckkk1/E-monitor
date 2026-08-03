# Cópia interna do pós-venda para admins — design

Data: 2026-08-03
Feature: checkbox em `/admin/users` que faz um admin receber, por email, **todo**
pós-venda enviado — de qualquer cliente.

## Problema

Hoje só quem é usuário do cliente recebe o pós-venda
([`ActiveClientUsers`](../../../workers/internal/postsale/repo_crud.go)):

```sql
WHERE client_id = $1 AND is_active = TRUE AND deleted_at IS NULL
```

Admin tem `client_id NULL` por força do CHECK `users_client_role_consistency`
(migration 0027), então é estruturalmente impossível um admin entrar na lista.
O time comercial quer acompanhar o que sai sem depender de encaminhamento manual.

## Decisões

| Questão | Decisão | Por quê |
|---|---|---|
| Como o admin entra | Destinatário **igual ao cliente**, sem coluna de distinção | Escolha do dono. Trade-off registrado abaixo |
| Default do check | **FALSE**, sem backfill | Opt-in: pós-venda de todo cliente é volume alto; ligar sozinho surpreende |
| Bloqueio do passo 1 | **Continua** exigindo ≥1 usuário ativo do cliente | O documento é lido por link pessoal do cliente; fechamento que só o admin vê não é fechamento |
| Email do admin | **Idêntico** ao do cliente | Sem variante de template |
| Trilho do wizard | Mostra os dois grupos, contagem honesta | O trilho responde "o que vai no email" |

### Trade-off aceito: a métrica de abertura passa a misturar

Sem `is_internal` em `post_sale_report_recipients`, depois do envio o admin é um
destinatário como outro qualquer. Consequência real: ele entra em
`recipients_count` e, se abrir o link, em `opened_count` — o "X de Y abriram" do
`/admin/pos-venda` deixa de falar só do cliente.

Foi decisão consciente do dono. **É reversível sem migration**: `user_id` aponta
pra `users`, então dá pra derivar `role` num join se a métrica virar ruído.

## Modelo de dados

Migration `0061_user_post_sale_emails`:

```sql
ALTER TABLE users ADD COLUMN IF NOT EXISTS
  receive_post_sale_emails BOOLEAN NOT NULL DEFAULT FALSE;
```

Irmã de `receive_alert_emails` (0037), com o default invertido — aquela nasceu
preservando um comportamento existente, esta cria um comportamento novo.

Estrutural pura (`ADD COLUMN` com default constante, sem `INSERT ... SELECT`,
sem constraint sobre dado existente): **fora** do risco da regra 4.8 do CLAUDE.md.
Sem índice — o conjunto de admins é da ordem de dezenas, como no `ActiveInternal`.

## Backend

### `postsale.Repo.InternalRecipients`

Irmão do `ActiveClientUsers`, no mesmo arquivo:

```sql
SELECT id, email, COALESCE(name, '')
  FROM users
 WHERE role IN ('admin','operator')
   AND is_active = TRUE AND deleted_at IS NULL
   AND receive_post_sale_emails = TRUE
 ORDER BY email
```

`role IN ('admin','operator')` espelha o `ActiveInternal` do pacote `users` —
'operator' é sinônimo de admin no middleware de autorização.

**Não há dedup e isso é proposital:** o CHECK `users_client_role_consistency`
garante `admin/operator ⇒ client_id IS NULL`, logo os dois conjuntos são
disjuntos por construção. Um guard aqui seria código morto defendendo estado
impossível; o teste `TestPublish_AdminNaoDuplicaDestinatario` trava o invariante.

### `Publish`

Um `append` antes do loop que gera token. Tudo a jusante — `CreateRecipients`,
`sendOne`, `MarkEmail`, revogação e reenvio — já opera por destinatário e não
muda.

Posição na ordem: **depois** do congelamento do payload, junto com os
destinatários do cliente. A garantia de "artefato antes de email" (§Fluxo do
publish) segue intacta.

### Endpoint de destinatários — fonte única

`GET /post-sale/reports/{id}/recipients` existe, é admin-only, e está **morto**:
`usePostSaleRecipients` foi definido no `hooks.js` e nunca importado. O wizard
reimplementou a regra no frontend com `useUsersPaged({client_id, status:'active'})`.

Isso é uma segunda fonte de verdade pra "quem recebe" — o tipo de duplicação que
neste repo já custou caro (a divergência `/campaigns` × `/insights` está
documentada como cicatriz). Como o admin entra na conta agora, manter a
duplicação faria o trilho mentir sobre quantos emails saem.

Solução: o endpoint passa a aceitar `?client_id=` (o wizard não tem `report_id`
no passo 1 — o rascunho só nasce ao avançar) e devolve os dois grupos calculados
pelos **mesmos métodos que o publish usa**:

```json
{ "client": [{"email":"...","name":"..."}], "internal": [{"email":"...","name":"..."}] }
```

Rota nova `GET /post-sale/recipients?client_id=` sob `RequireRole("admin")`,
coberta pelo `router_postsale_authz_test.go`. A rota antiga por `{id}` continua
válida e passa a devolver o mesmo objeto.

## Frontend

### `UserFormModal`

- Checkbox **"Receber pós-venda dos clientes"** — só com `role === 'admin'`, em
  **criar e editar**.
- A checkbox **"Receber emails de alerta"**, hoje só no editar, passa a aparecer
  também no criar: o default dela é TRUE, e um formulário que esconde um opt-in
  ligado é o mesmo problema em menor escala. Exige aceitar o campo no `Create`
  do backend.

### Scroll da modal

Hoje `.ufm-backdrop` rola a página inteira (`align-items: flex-start`,
`padding: 6vh`, `overflow-y: auto`) e `.ufm-card` não tem teto de altura — com
role client + senha + três toggles o rodapé sai da tela.

Passa a: card com `max-height` do viewport, `<form>` como container de rolagem
(`overflow-y:auto; min-height:0`), header fixo e `.ufm-actions` `sticky` no
rodapé com fundo próprio. Mesma família do `.day-detail-card` que já existe.

`RSelect` já usa `menuPortalTarget={document.body}` + `menuPosition="fixed"`
(zIndex 9999 > 9000 do backdrop): o dropdown de cliente **não** é cortado pelo
novo overflow.

### Wizard

`useUsersPaged` sai; entra o hook do endpoint acima. O trilho ganha um segundo
bloco "Cópia interna", a barra soma os dois na frase de envio e a confirmação do
`ReviewStep` também. `canAdvance` do passo 1 continua olhando **só** o grupo
`client`.

## Testes

| Teste | Trava |
|---|---|
| `TestRepo_InternalRecipients_SoAtivosComFlag` | flag on entra; flag off, inativo, deletado e viewer ficam fora |
| `TestPublish_IncluiAdminsComFlag` | admin marcado vira destinatário com token próprio |
| `TestPublish_AdminNaoDuplicaDestinatario` | um email por pessoa (invariante do CHECK) |
| handler de users | `receive_post_sale_emails` sobrevive ao create e ao patch |

## Fora de escopo

- Coluna `is_internal` / separação da métrica de abertura (ver trade-off).
- Variante de assunto ou template pro admin.
- Escolher *quais* clientes o admin acompanha — é tudo ou nada, como pedido.
