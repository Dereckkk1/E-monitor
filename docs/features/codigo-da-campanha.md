---
status: implementado
ultima-verificacao: 2026-09-21
codigo-relacionado:
  - migrations/0069_campaign_hub_code.up.sql
  - workers/internal/catalog/campaigns.go
  - workers/internal/hub/hub.go
  - workers/internal/api/handlers/campaigns.go
  - workers/internal/api/handlers/hubcodes.go
  - workers/internal/hubnotify/hubnotify.go
  - frontend/src/pages/CampaignWizardSteps/BasicDataStep.jsx
  - frontend/src/pages/CampaignWizardPage.jsx
---

# Código da campanha (o vínculo com o E-Hub)

**⚠️ Implementado e NÃO publicado.** O código está na branch `feat/codigo-do-hub`,
sem PR. Ver "O que falta para subir".

## O que mudou, e por quê

Antes, a campanha nascia aqui e alguém tinha de juntá-la à mão à campanha
correspondente no E-Hub. O pareamento era trabalho manual, e quando ninguém o
fazia a campanha simplesmente não aparecia para o cliente.

Agora a campanha **nasce no hub com um código** (`EH-7K4M2X`) e quem cadastra
aqui **cola esse código**. O hub usa o código para saber dentro de qual campanha
dele esta aqui vira uma *proposta*. O vínculo deixou de depender de memória.

Spec (fonte da verdade): `hub/docs/superpowers/specs/2026-09-18-codigo-do-hub-design.md`.

## O caminho, de ponta a ponta

1. **A tela** (`BasicDataStep.jsx`) mostra um campo "Código do hub", obrigatório
   na criação e opcional na edição de campanha antiga. Ao sair do campo ele
   pergunta ao próprio backend quem é o dono daquele código.
2. **A rota de repasse** (`GET /v1/internal/hub-codes/{code}`) pergunta ao hub e
   devolve só o que a tela precisa. Ela existe para a chave da plataforma **não
   descer para o navegador** — a mesma chave abre a porta de eventos do hub.
3. **A barreira** (`conferirCodigoOuRecusar`, usada pelo `POST` e pelo `PUT`)
   recusa com 422 o código que não existe ou é de outro cliente. A conferência
   da tela é conveniência; esta é a barreira de verdade, porque quem chama a API
   direto pula a tela.
4. **O emissor** manda `campanha.upsert` com o código, fora do caminho da
   resposta, e marca `hub_notified_at` quando o hub confirma.
5. **O job** (`internal/hubnotify`) varre a cada 15 minutos quem tem código e não
   tem confirmação, e reemite. Em regime ele devolve zero linhas.

## As colunas

| coluna | o que é |
|---|---|
| `hub_code` | o código colado, na forma canônica (`EH-7K4M2X`). NULL = sem vínculo |
| `hub_notified_at` | quando o hub CONFIRMOU. NULL = ele ainda não sabe desta campanha |
| `hub_notify_tentado_em` | quando o job tentou pela última vez, tenha dado certo ou não |

Sem índice único em `hub_code`, e isso é decisão de produto: dois PIs da mesma
ação de marketing compartilham um código de propósito.

## As quatro caras da tela

| situação | o que aparece | dá para salvar? |
|---|---|---|
| código do cliente escolhido | verde, com nome e período da campanha do hub | sim |
| código de **outro** cliente | vermelho, dizendo de quem é | sim, mas o servidor recusa com 422 |
| código inexistente (404) | vermelho | idem |
| não deu para conferir (503/502/500) | âmbar, "conferido ao salvar" | **sim** — decisão 2 da spec |

⚠️ **O contrato é "404 é vermelho; QUALQUER outro não-200 é âmbar".** A rota
devolve quatro status, não dois. Tratar só o 503 como âmbar pinta de vermelho um
código perfeito no dia em que o hub trocar o envelope da resposta.

## Diagnóstico

**"A campanha não aparece no hub."** Nesta ordem:

1. `SELECT hub_code, hub_notified_at, hub_notify_tentado_em FROM campaigns WHERE id = …`
   - `hub_code` NULL → ninguém colou código. Campanha antiga ou criada por carga;
   - `hub_notified_at` preenchido → daqui saiu e o hub confirmou. O problema é lá;
   - `hub_code` preenchido e `hub_notified_at` NULL → está na fila do job. Veja o log.
2. No log da API, `hubnotify:` diz o que aconteceu. A linha que importa é
   **`campanha.upsert foi IGNORADO pelo hub`** com o `motivo`: o hub responde
   **200** quando descarta o evento pela regra dele, e o motivo (`codigo-inexistente`,
   `cliente-divergente`, `codigo-malformado`) é o diagnóstico inteiro.
3. `cliente-divergente` quase sempre é a ponte: `clients.hub_id` aqui e
   `Client.emonitorClientId` lá têm de apontar um para o outro.

**"O cadastro ficou lento."** A conferência tem teto de 2s (`prazoDeConferencia`),
e o cadastro segue mesmo sem resposta. Se travar mais que isso, não é ela.

⚠️ **A porta do hub devolve 401 idêntico para chave errada e para produto
desativado.** Só o `AuditLog` do hub separa: `platform.chave_recusada` contra
`platform.produto_inativo`.

## O que falta para subir

1. `HUB_URL` e `HUB_PLATFORM_KEY` no ambiente do E-monitor;
2. produto E-monitor com **"Visível no portal"** marcado no `/admin/catalogo` do hub;
3. a migração `0069` aplicada **antes** do deploy (ver `docs/operations/`);
4. deploy manual na VM — **este repositório não tem CI**: `./scripts/deploy.sh`.

⚠️ **Tudo ou nada.** Com as colunas e a barreira no ar mas sem o emissor e o job,
as campanhas nascem certas aqui e nunca chegam ao hub, sem erro em tela nenhuma.

## Lacunas conhecidas (decisão do Dereck, não defeito de código)

1. **Renomear a campanha ou mudar o status NÃO chega ao hub** depois da criação —
   o emissor só dispara quando o código muda. A §6.2 quer o nome da proposta
   igual ao daqui, e a §6.4 define "campanha cancelada ⟺ todas as propostas
   canceladas": essa regra não dispara hoje. Emitir a cada "salvar" é decisão de
   produto (uma chamada de rede por edição).
2. **Apagar o código com o hub fora do ar perde o congelamento.** O aviso é
   dispara-e-esquece e a campanha some da fila (o filtro é `hub_code IS NOT NULL`),
   então a proposta do hub segue coletando. O §6.5 não define durabilidade para
   essa operação.
3. **A ponte ilegível é aceita aqui e recusada lá.** Um `emonitorClientId` que não
   é UUID passa nesta barreira e vira `cliente-divergente` no hub. Desde o
   conserto de 2026-09-21 isso é uma falha *ruidosa e que repete* (log de ERROR,
   campanha fica na fila) em vez de perda silenciosa, mas o conserto durável é
   normalizar o campo na escrita, no hub.
