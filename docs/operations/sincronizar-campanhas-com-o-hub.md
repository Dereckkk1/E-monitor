---
status: implementado
ultima-verificacao: 2026-09-22
codigo-relacionado:
  - workers/internal/api/handlers/campaigns.go
  - workers/internal/hubnotify/hubnotify.go
  - frontend/src/pages/CampaignWizardSteps/BasicDataStep.jsx
  - migrations/0069_campaign_hub_code.up.sql
---

# Sincronizar campanhas com o E-Hub

**No ar desde 2026-09-22.** Documento operacional: como o vínculo funciona, o que
mudou, como está hoje, e o que fazer com as campanhas que já existiam.

---

## 1. Como funciona

Antes, a campanha nascia no E-monitor e alguém tinha de juntá-la à mão à campanha
correspondente do hub. Quando ninguém fazia, ela não aparecia para o cliente.

Agora a campanha **nasce no hub com um código** (`EH-7K4M2X`) e quem cadastra no
E-monitor **cola esse código**. O hub usa o código para saber dentro de qual
campanha dele esta aqui vira uma **proposta**.

```
  HUB                                    E-MONITOR
  ---                                    ---------
  cria a campanha comercial
  gera EH-7K4M2X          ---------->    cola o codigo no Step 1 do wizard
                                         |
                          <--------------+ confere: "de quem e este codigo?"
  responde nome/periodo/dono             |
                                         v
                                         salva a campanha
                          <--------------+ emite `campanha.upsert`
  acha a campanha do codigo              |
  cria a PROPOSTA dentro dela            |
  chama de volta o E-monitor ----------->| (le nome, datas, status)
  grava e confirma        --------------->  marca `hub_notified_at`
```

**Dois PIs podem dividir o mesmo código de propósito** — viram duas propostas
dentro da mesma campanha do hub. É o caso que a mudança existe para atender.

### As quatro caras da tela

| o que aparece | o que significa | dá para salvar? |
|---|---|---|
| verde, com nome e período | o código é do cliente escolhido | sim |
| vermelho, "é da campanha X, do cliente Y" | o código é de **outro** cliente | o servidor recusa |
| vermelho, "não encontrado" | o código não existe no hub | o servidor recusa |
| âmbar, "não deu para conferir agora" | o hub não respondeu | **sim** — ver abaixo |

⚠️ **O âmbar não trava o cadastro, e isso é decisão de produto.** Uma queda do hub
não pode parar a operação: o código é gravado, o servidor confere de novo ao
salvar, e se ainda assim não der, um job de 15 minutos leva a campanha quando o
hub voltar.

### A rede de segurança

`hub_notified_at` guarda quando o hub **confirmou**. Enquanto for nulo, o job
`internal/hubnotify` reemite a cada 15 minutos, até 50 por ciclo. Em regime ele
não faz nada.

⚠️ **200 do hub não é entrega.** Ele responde 200 com
`{"acao":"ignorado","motivo":"..."}` quando descarta o evento pela regra dele. O
`motivo` é o diagnóstico inteiro — ver §5.

---

## 2. O que foi feito

| | |
|---|---|
| `migrations/0069` | colunas `hub_code`, `hub_notified_at`, `hub_notify_tentado_em` |
| barreira de 422 | no `POST` **e** no `PUT` de `/campaigns` |
| `GET /v1/internal/hub-codes/{code}` | a tela confere sem a chave descer para o navegador |
| `internal/hubnotify` | o job de 15 minutos |
| campo no Step 1 do wizard | obrigatório na criação, opcional na edição |

Do lado do hub: o código nasce na campanha, o evento vira proposta, trocar o
código move a proposta, apagar congela a coleta.

---

## 3. Como está hoje

- **E-monitor:** no ar, com as três colunas e o job ligado. **1093 campanhas,
  todas com `hub_code` vazio** — a `0069` não faz backfill de código, porque
  código não se inventa: ele é escolhido por quem cadastra.
- **Hub:** no ar. **216 campanhas com código** (backfill de 2026-09-22), índice
  único `hubCode_1` criado e conferido.
- **As 180 campanhas antigas continuam lá**, com código, aparecendo para o cliente
  como sempre apareceram. O expurgo **não rodou** — 24 delas estão no ar hoje,
  para 42 clientes.

---

## 4. O que fazer para sincronizar

### 4.1 Campanha NOVA — não precisa fazer nada

Crie no hub, copie o código, cole no E-monitor. Pronto.

### 4.2 Campanha que JÁ existe — só se você quiser agrupar

⚠️ **Não há urgência, e na maioria dos casos não há o que fazer.** As 180
campanhas antigas já estão no hub e já aparecem para o cliente. Colar nelas o
código da própria campanha auto-criada é um **no-op**.

O único motivo para mexer é **agrupar**: quando várias campanhas do E-monitor
deviam ser propostas dentro de **uma** campanha comercial do hub, em vez de 180
campanhas soltas de uma linha cada.

**Como agrupar, uma de cada vez:**

1. no hub, crie (ou escolha) a campanha comercial que vai receber, e copie o código;
2. no E-monitor, abra a campanha, Step 1, cole o código, salve;
3. a proposta **muda de campanha** no hub — continua sendo **uma** proposta, com
   pastas, arquivos e métricas intactos (eles penduram na proposta, não na campanha);
4. a campanha auto-criada de onde ela saiu fica **vazia**.

**A ordem importa: mova primeiro, apague depois.** Campanha auto-criada vazia é
segura de expurgar; com proposta dentro, não.

### 4.3 Ver quem ainda não tem código

```bash
cd ~/radiocheck
alias dc="docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env"

# quantas campanhas VIGENTES ainda estao sem codigo
dc exec postgres psql -U radiocheck -d radiocheck -Atc "
  select count(*) from campaigns
   where hub_code is null and start_date <= now() and end_date >= now()"

# a lista, por cliente
dc exec postgres psql -U radiocheck -d radiocheck -c "
  select c.name as cliente, ca.name as campanha, ca.start_date::date, ca.end_date::date
    from campaigns ca join clients c on c.id = ca.client_id
   where ca.hub_code is null and ca.start_date <= now() and ca.end_date >= now()
   order by c.name, ca.start_date desc"
```

### 4.4 O expurgo das antigas

**Não rode ainda.** Medido em 2026-09-22: 180 campanhas, **24 no ar hoje**, 42
clientes. Elas não voltam sozinhas — só conforme alguém for colando código.

Quando fizer sentido (depois de agrupar o que tinha de ser agrupado):

```bash
cd ~/var/www/html/ehub/backend
set -a && . ./.env && set +a
npx ts-node scripts/expurgar-campanhas-emonitor.ts          # modo seco, nao apaga
# leia a lista, e so entao:
npx ts-node scripts/expurgar-campanhas-emonitor.ts --executar \
  --como <id-de-usuario-interno-ativo> --confirmar <campanhas>:<arquivos>
```

⚠️ O bucket `ehub-campanhas` **não tem versionamento**. Arquivo apagado não volta.

---

## 5. Quando algo não chega

```bash
# a fila: tem de ser 0
dc exec postgres psql -U radiocheck -d radiocheck -Atc "
  select count(*) from campaigns where hub_code is not null and hub_notified_at is null"
```

Se passar de 15 minutos acima de zero:

```bash
dc logs api | grep hubnotify
```

| motivo no log | o que é |
|---|---|
| `cliente-divergente` | `clients.hub_id` aqui e `emonitorClientId` lá não apontam um para o outro |
| `codigo-inexistente` | o código foi apagado no hub, ou digitado errado |
| `sem-codigo` | evento sem código — campanha que nunca recebeu um |
| `plataforma_indisponivel` | o hub chamou de volta o E-monitor e não alcançou |

**"A campanha não aparece no hub"**, nesta ordem:

```bash
dc exec postgres psql -U radiocheck -d radiocheck -Atc "
  select hub_code, hub_notified_at, hub_notify_tentado_em from campaigns where id = 'UUID'"
```

- `hub_code` nulo → ninguém colou código;
- `hub_notified_at` preenchido → saiu daqui e o hub confirmou; o problema é lá;
- código preenchido e confirmação nula → está na fila; veja o log.

⚠️ A porta do hub devolve **401 idêntico** para chave errada e produto desativado.
Só o `AuditLog` do hub separa.

---

## 6. Os endereços, para não confundir

| | |
|---|---|
| portal do cliente (Cloudflare Pages) | `https://clientes.emidiastec.com.br` |
| **API do hub** | `https://api-clientes.emidiastec.com.br` |

⚠️ `HUB_URL` aponta para a **API**. Apontado para o portal, o caminho
`/api/platform/...` devolve **200 com o HTML da SPA** — e aí a tela mostra âmbar e
deixa salvar, sem nada denunciando. O teste que vale:

```bash
curl -s -i -H "X-Hub-Platform-Key: $HUB_PLATFORM_KEY" \
  "$HUB_URL/api/platform/campaigns/by-code/EH-ZZZZZZ" | head -4
```

**404 + `application/json`** = porta viva, chave aceita, produto visível.

---

## 7. O que depende de decisão, não de código

1. **Renomear a campanha ou mudar o status NÃO chega ao hub** depois da criação.
   A regra "campanha cancelada = todas as propostas canceladas" nunca dispara.
   Emitir a cada "salvar" é decisão de produto.
2. **Apagar o código com o hub fora do ar perde o congelamento** — o aviso é
   dispara-e-esquece e a campanha sai da fila.
3. **Ponte ilegível é aceita aqui e recusada lá** — o conserto durável é
   normalizar `emonitorClientId` na escrita, no hub, e isso mexe em dado existente.
