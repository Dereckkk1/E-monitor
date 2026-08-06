---
status: implementado
ultima-verificacao: 2026-08-06
codigo-relacionado:
  - workers/internal/match/statemachine.go
  - workers/internal/match/statemachine_shortwindow_test.go
  - workers/internal/ingestor/worker.go
  - workers/internal/supervisor/supervisor.go
  - workers/cmd/api/main.go
  - infra/docker/docker-compose.yml
---

# Confirmação em janela única para material curto (<10s)

**Flag:** `SHORT_SINGLE_WINDOW_FACTOR` (float; vazio ou 0 = **desligado**, que é o default)
**Origem:** [incident-2026-07-24-pulso-milium-nao-detectado.md](../incidents/incident-2026-07-24-pulso-milium-nao-detectado.md)

## O problema

A state machine confirma uma detecção na **segunda** janela qualificada
([statemachine.go](../../workers/internal/match/statemachine.go), `case StateDetecting`). Para
um spot de 30s isso é trivial: ele tem ~15 janelas de análise (janela 4s, hop 2s) e várias são
100% internas ao material.

Um material de **5,7s não tem essa folga**: no máximo 1-2 janelas caem substancialmente dentro
dele, e as parciais só pontuam bem se o áudio estiver limpo. O score cai de forma brutalmente
não-linear com o quanto do material está dentro da janela — medido nas censuras reais:

| quanto do pulso está na janela | score |
|---|---|
| ~4,0s | 33–57 |
| ~2,0s | **4** |
| ~1,7s | **6** |

Em stream comprimido (HE-AACv2 64k), a janela central ainda passa mas as parciais desabam
abaixo do gate. Sobra **uma** janela qualificada, a segunda nunca chega, e a veiculação
desaparece: sem row em `detections`, sem log de match, sem nada. Só um `window scan` com o
score, que rotaciona em ~1 hora.

## A medição que motivou a regra

Varredura de fase sobre as duas censuras reais (`audio-refs/`, 2026-08-06): deslocando o
alinhamento da grade de janelas em passos de 0,25s, contamos em quantas fases a tocada
sobrevive.

| censura | regra atual (2 janelas) | com janela única (fator 2.5) |
|---|---|---|
| 24/07 (aircheck 40kbps) | **37%** das fases | **87%** |
| WhatsApp (320kbps) | **12%** das fases | **75%** |

Isso reproduz o comportamento de prod: nas emissoras onde o match é forte (média `hash_count`
228–358) as janelas parciais também passam e a detecção fica em 95–100%; onde o match é fraco
(Clube 303, média **84**, máximo 131) a taxa cai para **32%**.

## A regra

Em `StateIdle`, quando a primeira janela qualifica, o material confirma imediatamente se:

1. o material tem **menos de 10 segundos** (`shortMaterialMaxSeconds`, alinhado com o
   `MinShareableDurationSeconds` do sharing — é a mesma classe estruturalmente frágil); **e**
2. `UniqueScore >= fator × min_hashes` da emissora.

Com `min_hashes = 19` (o valor calibrado em todas as 7 emissoras da campanha Milium) e fator
2.5, o piso fica em **~48**. O ruído máximo do pulso medido em prod é **12–13** — margem de
~3,7×.

Material ≥10s e score abaixo do piso seguem exatamente o comportamento antigo.

## Defesa contra falso positivo

A regra **troca precisão por recall de propósito**: para material curto, faltar veiculação
custa mais que sobrar, porque a que falta vira contagem manual e a que sobra é filtrada. Duas
barreiras seguem de pé:

1. **O piso alto** — 2,5× o threshold já calibrado por emissora, ~3,7× acima do ruído medido.
2. **O audit §9.9** — re-fingerprinta o clipe salvo contra o master antes de publicar. Um falso
   positivo vira `evidence_status='audit_rejected'` e **não conta** em nenhuma tela nem
   cobrança (`catalog.ApprovedDetectionsFilter`).

## Operação

```bash
# .env da VM
SHORT_SINGLE_WINDOW_FACTOR=2.5

DC="docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env"
$DC build api && $DC up -d --force-recreate --no-deps api   # --no-deps OBRIGATÓRIO (CLAUDE.md 4.1)
docker logs docker-api-1 2>&1 | grep "janela única para material <10s ENABLED"
```

Se a linha de log não aparecer, a env não chegou no container — conferir o passthrough no
compose (armadilha do `SHARING_MIN_SCORE`, incidente 2026-06-15).

**Não é hot-reload.** Workers já rodando só pegam a mudança no próximo restart: trocar o
critério de confirmação no meio de uma janela de detecção deixaria estado inconsistente.

### O que olhar na sombra

```bash
curl -s localhost:8080/metrics | grep -E 'radiocheck_match_short_single_window_total|radiocheck_audit_attempts_total'
```

- `radiocheck_match_short_single_window_total` — o ganho bruto: cada uma seria uma veiculação
  perdida sem a regra.
- **Cruzar com `audit_attempts_total{result="rejected"}`**: se as rejeições subirem na mesma
  proporção, o piso está baixo demais e o fator deve subir (3.0, 3.5). Se subir o contador e as
  rejeições não, a regra está recuperando tocada real.

No log, o campo `path` do `detection confirmed` separa os dois caminhos
(`two_windows` | `short_single_window`).

### Rollback

Remover a env (ou pôr 0) e recriar o api. Não desfaz detecções já gravadas — só para de
confirmar em janela única.

## Limites conhecidos

- **Não resolve tudo.** Nas fases em que nem a janela central atinge o piso (medido: melhor
  score 45–52 em 3 de 16 fases) a tocada continua se perdendo. O passo seguinte, se necessário,
  é o **hop de 1s para material ≤10s** (#7 do §6.B do relatório), que custa ~12 pontos de CPU.
- A medição vem de **2 censuras**. O mecanismo é claro e bate com prod, mas a validação real é
  a sombra: comparar a taxa de detecção da Clube 303 e da meninablu antes/depois.
- **Não** ajuda material ≥10s, por construção.
