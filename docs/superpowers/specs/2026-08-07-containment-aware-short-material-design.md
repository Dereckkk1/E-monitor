# Design — Discriminação de containment para material curto

**Data:** 2026-08-07
**Investigação de origem:** [incident-2026-07-24-pulso-milium-nao-detectado.md](../../incidents/incident-2026-07-24-pulso-milium-nao-detectado.md)
**Feature que este design supersede parcialmente:** [short-material-single-window.md](../../features/short-material-single-window.md)

---

## 1. Problema (1 parágrafo)

Material curto (<10s) que é **subset acústico** de um spot ≥10s do mesmo cliente vive num dilema
que o sistema hoje resolve por altura de score, não por evidência. Quando o curto toca sozinho,
ele precisa de **duas** janelas qualificadas para confirmar e frequentemente só consegue uma —
a veiculação some sem row nem log. Quando o **spot** toca, o curto co-dispara na cauda dele com
score legítimo e precisa ser descartado. Os dois cenários são **indistinguíveis pelo score**, e
hoje a única ferramenta é um limiar (`SHORT_SINGLE_WINDOW_FACTOR=2.5`) que escolhe qual dos dois
erros cometer. A informação que separa os casos **já é calculada** a cada janela e é descartada.

---

## 2. Evidência acumulada (medições 2026-08-06/07, produção)

Esta seção existe porque o caso foi diagnosticado errado três vezes antes de fechar. Cada linha
abaixo eliminou uma hipótese.

### 2.1 O que NÃO é

| hipótese | como morreu |
|---|---|
| Material duplicado / fingerprint ruim | 1 só material (`short_id 51`, 5,736s, `ready`, 10.925 hashes) |
| Vínculo / campanha / datas | campanha `117 Milium` **ativa** 01/08–28/08, as 7 emissoras no `target_stations` |
| Captura morta | as 2 piores detectam os spots de 30s normalmente; queda dentro da janela de veiculação = **6,6 min em 14 dias** |
| Dedup §18.2.2 matando tocada real | **7 supressões em 14 dias, todas corretas** — o corte mantido tinha `audit_coverage` 0,53–0,80 |
| False-confirm do spot roubando a tocada | 41 rows de spot com cobertura 0,48–0,82; distribuição **não é bimodal** |
| **Calibração** (`min_hashes`) | **`min_hashes = 19` e `noise_p99 = 13,0` em TODAS as 7 emissoras** — e a detecção varia de 32% a 100% |
| Densidade de catálogo | 2–4 materiais por emissora, **sem correlação** (Clube-youngtech: 2 mats → 100%; Clube-303: 2 mats → 32%) |
| Threshold alto demais | baixar `min_hashes` de 19 → 13 → 10 → **5** não recupera **nenhuma** tocada (37%/12% em todos) |

### 2.2 O que É

**Força do match correlaciona com a taxa de detecção** (30 dias, mesmo material, mesmos gates):

| emissora | provedor | `hash_count` médio | máximo | `audit_coverage` | detecção |
|---|---|---|---|---|---|
| Clube | cast.youngtech | 358 | 810 | 0,730 | **100%** |
| 96 FM | cast2.youngtech | 289 | 652 | 0,766 | 95% |
| 105 FM | servidoresbrasil | 228 | 497 | 0,739 | 98% |
| Menina (blu+cam) | painel.sintonizar | 145 | 312 | 0,615 | ~70% |
| Verde Vale | fabricahost | 132 | 215 | 0,523 | 89% |
| **Clube 303** | **loopert** | **84** | **131** | **0,339** | **32%** |

O **máximo** da pior (131) mal encosta no **mínimo** das outras (54–78): lá toda tocada acontece
na borda do detectável.

**A causa mecânica — o score desaba de forma não-linear com a sobreposição:**

| material dentro da janela (4s) | score |
|---|---|
| ~4,0s | 33–57 |
| ~3,0s | 18–31 |
| ~2,0s | **4–6** |

Um spot de 30s tem ~15 janelas, várias 100% internas — atravessa qualquer portão. Um material de
5,7s tem 1–2 janelas úteis, e as parciais desabam. Em stream comprimido sobra **uma**, a segunda
nunca vem, e a state machine (que confirma na 2ª janela qualificada) nunca dispara.

**Varredura de fase** (censuras reais, passos de 0,25s sobre a grade de janelas):

| censura | regra de 2 janelas | com janela única a 2,5× |
|---|---|---|
| 24/07 (aircheck 40kbps) | **37%** dos alinhamentos | **100%** |
| WhatsApp (320kbps) | **12%** | **87%** |

Os 12–37% reproduzem as taxas reais das emissoras ruins. O desfecho depende de onde a grade de
janelas cai em relação à tocada — e a margem de score decide se as janelas parciais sobrevivem.

**Piso de ruído do pulso, medido em produção:** 189 janelas, distribuição 5–12, p99 = 13.

### 2.3 O que a flag de janela única entregou (~2 dias em prod, fator 2.5)

| métrica | valor |
|---|---|
| armaram (`detecting started`) | 44 |
| confirmaram | 37 (**29** por janela única + **8** por 2 janelas) |
| perderam | 7 |
| rejeição de audit | **6 / 1.881 = 0,32%** |

### 2.4 A medição que motiva ESTE design

Cruzando cada morte com **a mesma emissora** e ±90s:

| morte (UTC) | emissora | score | spot no mesmo lugar/hora | veredito |
|---|---|---|---|---|
| 06/08 18:26:40 | meninablu | 46 | `18:26:16` mat 212 cov **0,78** | cauda — correto |
| 06/08 19:06:09 | Verde Vale | 39 | `19:05:43` mat 213 cov **0,78** | cauda — correto |
| 07/08 11:27:54 | Verde Vale | 43 | `11:27:29` mat 213 cov **0,77** | cauda — correto |
| **07/08 13:45:50** | **meninacam** | **40** | **nenhum** | **tocada real perdida** |
| 07/08 14:23:55 | meninacam | 36 | `14:23:29` mat 212 cov **0,82** | cauda — correto |
| 07/08 18:50:15 | Clube 303 | 32 | `18:49:51` mat 212 cov **0,76** | cauda — correto |

**5 de 6 mortes estão certas.** E o padrão temporal é inequívoco: o curto arma **24–26s depois**
da detecção do spot, num spot de 30,7s — exatamente onde ele mora, no final.

**Consequência direta:** baixar o fator para 2.0 (piso 38) recuperaria **1 tocada real** (score 40)
e criaria **3 falsos positivos** (46, 43, 39 — todos cauda). O limiar não consegue separar, porque
a altura do score não é o que distingue os dois casos.

### 2.5 Achados adjacentes (fora do escopo, mas registrados)

- **Dedup falhou uma vez.** UNIFIQUE em Jovem Pan, 07/08 16:28: citação `180` (6,4s) com
  `audit_coverage` **0,1667** (assinatura de false-confirm) contando **junto** com o spot `169`
  (30,8s) de cov 0,5316, 22s de diferença, mesmo cliente, janelas sobrepostas — e **sem retração**.
  A `confidence` 0,92 do 180 indica confirmação por 2 janelas (uma janela num material de 6,4s
  daria exatamente 32/50 = 0,64), então **não** foi causado pela flag de janela única.
- **Spots duplicados inflando contagem.** Verde Vale dispara `213` (cov 0,78, real) e `212`
  (cov 0,20–0,24, false-confirm) no mesmo segundo, repetidamente.
- **A métrica `suppressed_suspect` é ruidosa por construção.** Ela usa diferença de *confiança*,
  que é estruturalmente enviesada (curto ~1,0 pelo piso de 32 frames; longo ~0,2 por confirmar na
  2ª janela). Disparou 1 vez no período, e aquela supressão estava **correta**.
- **O contador `short_single_window_total` superestima o ganho.** Ele incrementa em `StateIdle`,
  então conta também as tocadas que confirmariam de qualquer jeito pelo caminho de 2 janelas.
- **Metade das rows do pulso eram manuais** (8 de 15 em 14 dias), mascarando o tamanho real do
  problema nas contagens semanais.
- **Log da api sem rotação** (`json-file` sem `max-size`), ~180 mil linhas/hora de `window scan`.

---

## 3. Decisão de design

> **Registrar a relação de containment e usá-la no momento da decisão, em vez de adivinhar por
> altura de score.**

O discriminador correto não é "o score é alto?", é **"o spot que contém este material está
tocando agora?"**. E a informação para responder isso já existe na hora:

1. O engine calcula o score de **todos** os materiais do índice **na mesma janela**
   ([worker.go:450-465](../../../workers/internal/ingestor/worker.go), `rawScores`).
2. O `OffsetFrames` de cada match diz **onde** dentro do master aquele trecho casou.

Faltam duas coisas: (a) o sistema não **persiste** a relação de containment para pares <10s, e
(b) a state machine não **consulta** o contexto da janela.

### 3.1 Por que a máquina que já existe não resolve

A §18.2.2-v2 (`DISAMBIG_BY_COVERAGE`, ligada em prod) arbitra **exatamente** por este critério —
cobertura de evidência pós-audit — e funciona: mediu-se `reattributed_by_coverage`,
`duplicate_cofire_retracted` e `restored_on_reject` atuando.

Mas ela opera **sobre rows**. O curto que morre na state machine nunca vira row.

> A arbitragem é pós-row. A perda é pré-row. Elas não se encontram.

### 3.2 Por que não simplesmente publicar-provisório e arbitrar depois

É a alternativa (o F-125 original: "publica-provisório em vez de suprimir"), e é mais simples.
Foi **rejeitada como primeiro passo** por uma razão empírica desta investigação: o dedup — o
componente em que essa estratégia deposita toda a confiança — **foi flagrado falhando** no caso
UNIFIQUE (§2.5). Jogar mais confirmações no colo dele antes de entender aquela falha multiplica
o erro em vez de resolver.

Fica como **Fase 2** deste design, depois que a falha do dedup for entendida.

---

## 4. Componentes

### 4.1 Persistir a relação de containment (`material_containments`)

Hoje o `sharing.go` **pula** pares em que qualquer lado tem <10s
(`MinShareableDurationSeconds`, [sharing.go:92](../../../workers/internal/sharing/sharing.go)),
descartando o cálculo antes de registrar qualquer coisa. O comentário do próprio código declara
que o caso "cai pra defesa de version-disambiguation" — que é justamente o que não funciona
pré-row.

**Mudança:** manter o skip do *flagging* de `is_shared` (essa defesa realmente não se aplica a
material curto), mas **gravar a relação medida**:

```
material_containments
  contained_id      UUID   -- o curto (ex.: pulso 51)
  container_id      UUID   -- o longo que o contém (ex.: spot 212)
  offset_seconds    REAL   -- onde dentro do container o curto começa
  match_score       INT    -- força da relação, para triagem
  measured_at       TIMESTAMPTZ
  PRIMARY KEY (contained_id, container_id)
```

Escopo: só pares do **mesmo cliente** (é a mesma condição que o dedup usa), com o curto <10s.

Backfill: rodar o scan sobre o catálogo ativo atual. São poucos pares — hoje **4 materiais <10s**
em 17 combinações material×emissora.

### 4.2 Consultar containment na decisão da janela

No ponto em que a state machine de um material curto S decide confirmar (hoje
`shortSingleWindowConfirms` em [statemachine.go](../../../workers/internal/match/statemachine.go)),
passar a considerar o contexto:

```
S quer confirmar em janela única
  │
  ├─ S tem container C registrado?
  │     não → confirma (nada mudou)
  │
  └─ sim → C está casando NESTA MESMA janela, acima do gate dele?
        │
        ├─ não  → S tocou sozinho          → CONFIRMA (e o piso pode ser mais baixo)
        │
        └─ sim  → o offset de C é consistente com a posição de S dentro dele?
                  (offset_C ≈ offset_seconds ± tolerância)
              │
              ├─ sim → é CAUDA de C         → não confirma (comportamento correto de hoje)
              └─ não → C está tocando outra parte → CONFIRMA
```

A checagem de offset é o que separa "o curto é a cauda do spot" de "o curto tocou logo depois do
spot no mesmo intervalo" — os dois têm C casando na janela, mas em posições diferentes do master.

**Dado necessário, todo já disponível:** `rawScores[C]` e `OffsetFrames` do match de C na janela
corrente, mais o `offset_seconds` da tabela.

### 4.3 Consequência: o limiar deixa de ser o árbitro

Com a discriminação funcionando, o `SHORT_SINGLE_WINDOW_FACTOR` pode cair (2.0, talvez menos)
**sem** produzir os falsos positivos de cauda — porque as caudas passam a ser identificadas pelo
que são, não barradas por altura.

Efeito estimado sobre as 6 mortes medidas: recupera a de score 40 (real) e as de 36 e 32 (se o
piso descer o suficiente), e continua descartando 46, 43 e 39 (cauda). Hoje nenhum limiar único
consegue esse corte.

---

## 5. Decisões tomadas (defaults revisáveis)

| decisão | default | alternativa |
|---|---|---|
| Onde discriminar | **na janela** (pré-row), usando `rawScores` | pós-audit (já existe, mas não alcança o caso) |
| Publicar-provisório + arbitrar depois | **não nesta fase** — o dedup foi flagrado falhando (§2.5) | Fase 2, depois de entender a falha |
| Escopo do containment | par do **mesmo cliente**, curto <10s | qualquer par |
| Tolerância de offset | a definir por medição (o padrão observado é 24–26s num spot de 30,7s) | fixa em ±2s |
| `is_shared` para pares <10s | **continua não flagando** (a defesa não se aplica) | flagar |
| Fator do limiar | mantém 2.5 até a discriminação existir; **só então** avaliar reduzir | reduzir agora |

---

## 6. Riscos

- **Falso "é cauda" quando os dois tocam de verdade** no mesmo intervalo (spot e vinheta em
  sequência). Mitigado pela checagem de offset; sem ela, o risco é real e é a razão de não usar a
  versão simples ("C casou na janela").
- **Containment não detectado** (relação existe mas o scan não registrou): o comportamento cai
  exatamente no de hoje — nada piora.
- **Custo de CPU:** desprezível. Não há fingerprint novo nem janela nova; é uma consulta a um map
  já preenchido na mesma iteração.
- **Catálogo cresce e a tabela envelhece:** a relação precisa ser recalculada quando entra
  material novo. Reusar o gatilho que já existe no shared-scan.

---

## 7. Fora de escopo (mas dependências ou vizinhos)

1. **A falha do dedup no caso UNIFIQUE** (§2.5) — é bug ativo e é **pré-requisito** da Fase 2.
2. **Corrigir `suppressed_suspect`** para usar `audit_coverage` da row mantida em vez de
   confiança. Hoje o alerta `DedupSuppressedSuspect` é ruído.
3. **Spots duplicados** (Verde Vale 212+213) inflando contagem — problema de cadastro.
4. **Instrumentação do ganho:** o contador atual superestima; medir por taxa/emissora contra
   baseline continua sendo o único número honesto.
5. **Rotação de log da api.**

---

## 8. Como validar

O método que fechou esta investigação, e que serve de gate:

1. **Varredura de fase offline** com as censuras de `audio-refs/`
   (`fingerprint/scripts/phase_sweep.py`) — mede recall por alinhamento antes de tocar em prod.
2. **Cruzamento morte × spot na mesma emissora** (§2.4) — com a discriminação ligada, o número de
   mortes classificadas como "cauda" deve permanecer, e as "tocada real perdida" devem ir a zero.
3. **Critério de parada:** a query de dupla contagem (curto + longo do mesmo cliente contando
   dentro de 90s). Baseline pré-mudança: **zero** ocorrências atribuíveis; qualquer aparecimento
   novo é regressão.
4. **`audit_attempts_total{result="rejected"}`** estável — hoje 0,32%.

---

## 9. Lição de método (por que esta spec existe)

O caso foi diagnosticado errado três vezes, sempre pelo mesmo motivo: **inferir de estrutura em
vez de medir**, e **generalizar de amostra insuficiente**.

- "É o dedup" — era o mecanismo certo, mas as supressões estavam todas corretas.
- "É a calibração" — `min_hashes` idêntico nas emissoras com 32% e com 100%.
- "É o gate dos 2%" — descartado a partir de **uma** fase de alinhamento; a varredura completa
  mostrou o oposto, e depois o cruzamento por emissora mostrou que nem isso era o ponto.
- "Baixar o fator recupera tocadas" — a distribuição de scores dizia que sim; o cruzamento com a
  emissora mostrou que 3 das 4 recuperações seriam falso positivo.

Em todos os casos, a medição certa custou minutos e existia desde o começo. **O controle que
faltava era comparar as emissoras que funcionam com as que não funcionam** — o grupo de controle,
que só foi construído no fim.
