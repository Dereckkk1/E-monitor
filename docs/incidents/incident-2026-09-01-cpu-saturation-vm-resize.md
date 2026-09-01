---
status: implementado
ultima-verificacao: 2026-09-01
codigo-relacionado:
  - workers/internal/ingestor/worker.go
  - workers/internal/ingestor/ffmpeg.go
  - workers/internal/match/engine.go
  - workers/internal/index/loader.go
  - workers/pkg/audio/peaks.go
  - infra/docker/docker-compose.yml
  - scripts/deploy.sh
---

# Incidente 2026-09-01 — saturação de CPU a 268 emissoras e migração para `c2d-standard-16`

**Resumo.** Com 268 emissoras monitoradas, a VM de produção (`c3-highcpu-8`,
8 vCPU / 16 GB) chegou a **0% de CPU ociosa** e o p99 da janela de matching
subiu para **2,39 s — acima da própria cadência de 2 s**. A vazão agregada se
manteve, então não houve perda em massa de detecção, mas a latência estourou e
a máquina passou a operar sem margem de rajada. Diagnóstico, dimensionamento e
troca de máquina foram feitos no mesmo dia. Após migrar para
**`c2d-standard-16`** (16 vCPU / 64 GB), o p99 caiu para **34,4 ms (69×)** e a
pressão de CPU (PSI) caiu de **76% para 8,5%**.

Nenhum dado foi perdido. A janela de indisponibilidade foi a do resize.

---

## 1. Linha do tempo

| Quando | O quê |
|---|---|
| ~ago/2026 | Operação cresce de 173 (medição de 21/07) para 268 emissoras |
| ~19/ago | Flex CUD de 1 ano é assinado (descoberto só agora — ver §5) |
| 01/set, início do mês | Bloco de campanhas transiciona para `ativa`; índice de matching incha |
| 01/set manhã | Operação reporta "uso de servidor beirando 100%" |
| 01/set | Diagnóstico: `id 0%`, PSI 76%, p99 de janela em 2,39 s |
| 01/set | Dimensionamento: demanda medida em **7,84 cores numa máquina de 8** |
| 01/set | Escolha de máquina (duas tentativas frustradas — ver §4) |
| 01/set ~12:00 | Backup + snapshots + resize para `c2d-standard-16` |
| 01/set ~12:10 | Stack no ar; p99 em 34,4 ms; PSI em 8,5% |

---

## 2. Sintoma e diagnóstico

### O que estava visível

```
procs -----------memory---------- -----io---- -system-- -------cpu-------
 r  b   swpd   free   buff  cache   bi    bo   in   cs us sy id wa st
19  0      0 413860 454592 4995484   0   168 16296 89563 94  6  0  0  0
28  0      0 381428 454600 5030844   0   468 16397 94499 93  7  0  0  0
21  0      0 381176 454616 5032560   0  2324 17728 76356 95  5  0  0  0
38  0      0 393324 454616 5033332   0   256 17011 104509 95  5  0  0  0
```

`id 0` em todas as amostras, `wa 0` (não é disco), `st 0` (não é vizinho
barulhento). Fila de execução (`r`) entre 19 e 38 contra 8 vCPU — 2,4× a 4,75×
de oversubscription.

```
$ cat /proc/pressure/cpu
some avg10=76.13 avg60=76.36 avg300=76.17
full avg10=0.00  avg60=0.00  avg300=0.00
```

**76% do tempo com tarefa pronta esperando CPU**, estável nos três horizontes —
regime, não pico. `full=0` significa que nunca *todas* as tarefas travaram ao
mesmo tempo: não houve wedge total.

```
$ free -m
              total     used     free   shared  buff/cache  available
Mem:          15982    12887      383     2304        5359        3094
Swap:             0        0        0
```

**Swap zerado**: pressão de memória viraria OOM kill, não lentidão.

### O número que fechou o diagnóstico

```
p99 de radiocheck_match_window_duration_seconds = 2,3917 s
```

A cadência de janela é **2 segundos** (`tickEvery = 32000` amostras a 16 kHz,
[worker.go](../../workers/internal/ingestor/worker.go)). O p99 passou da própria
cadência.

### Por que isso não virou perda em massa de detecção

A vazão agregada medida foi **137,6 janelas/s**, contra 134 requeridas
(268 emissoras × 1 janela / 2 s). Ou seja: **throughput mantido, latência
estourada.** Não houve backlog acumulando indefinidamente.

O risco real era mais estreito e vale registrar: quem faz o matching é a
**mesma goroutine que drena o pipe do ffmpeg** (`runPCMReader`). O pipe tem
64 KB ≈ 1,02 s de áudio a 16 kHz f32. No p99, aquela goroutine trava 2,39 s → o
pipe transborda → **aquele** ffmpeg bloqueia no write → para de ler o stream →
a janela TCP enche → servidores Icecast/Shoutcast derrubam clientes parados.
A ~1% de 137,6 janelas/s, isso são ~1,4 eventos de backpressure por segundo na
frota. Reconexões elevadas eram esperadas, mas o vínculo causal **não foi
comprovado** (ver §8, aberto).

---

## 3. Dimensionamento: como medir uma máquina saturada

`us 93–95%` não diz quanta CPU o sistema **quer** — diz que quer ≥ 100%. O
número está clipado. Dimensionar exige métricas de **demanda reprimida**, não
de uso.

O método usado (reprodutível, ver [capacity-and-unit-cost.md](../operations/capacity-and-unit-cost.md)):

**Passo 1 — repartir por componente.**

| componente | cores | % |
|---|---|---|
| ffmpeg (268 processos, via `ps`) | 4,10 | 52% |
| Go / matcher (`docker stats` da api − ffmpeg) | 3,65 | 46% |
| postgres | 0,04 | 0,5% |
| minio | 0,09 | 1,2% |
| redis + grafana + prometheus + jaeger + resto | 0,06 | 0,8% |
| **total** | **7,94 de 8** | **99%** |

> **Toda a stack de observabilidade junta consumia 0,06 core.** Prometheus,
> Jaeger, Grafana e node-exporter foram descartados como suspeitos com uma
> única leitura de `docker stats`.

**Passo 2 — custo unitário, que não satura.** CPU-segundos *por janela* é custo
por unidade de trabalho, então a saturação não o mascara:

```
26,5 ms de CPU/janela  =  3,65 cores ÷ 137,6 janelas/s
```

**Passo 3 — demanda, a partir da taxa requerida pela realidade.** As 134
janelas/s são fixadas pelo número de emissoras, não pela CPU disponível:

```
demanda = 134 × 26,5 ms + 4,10 (ffmpeg) + 0,19 (resto) = 7,84 cores
```

**Demanda 7,84 numa máquina de 8 — utilização de 98%.** O p99 de 2,39 s não é
um bug: é a consequência aritmética de teoria de filas com ρ→1.

A previsão se confirmou no pós-resize com **erro de 4%** (§6).

---

## 4. Causa raiz: a carga cresce em DUAS dimensões, e o modelo antigo só via uma

O [capacity-and-unit-cost.md](../operations/capacity-and-unit-cost.md) de
2026-07-21 modelava **0,022 vCPU por emissora**. Extrapolando para 268:

```
0,4 (baseline) + 268 × 0,022 = 6,35 vCPU de 8 = 79%
```

Observado: **99%**. Faltavam ~1,2 vCPU que o modelo por emissora não explicava.

A causa: **o índice de matching é global e carregado por status de campanha.**
O [loader.go](../../workers/internal/index/loader.go) inclui todo material de
campanha `ativa` **ou** `programada`. No início do mês um bloco de campanhas
transiciona para `ativa` de uma vez, e o custo da janela cresce com o tamanho do
índice — não só com o número de emissoras.

Evidência do benchmark `BenchmarkRealIndexPass` (`internal/match/engine_realindex_bench_test.go`):

| comerciais no índice | ms/janela | vs. base |
|---|---|---|
| 50 | 10,54 | — |
| 150 | 12,76 | **+21%** |
| 300 | 15,64 | **+48%** |

Os ~21% que faltavam na conta caem exatamente nessa faixa.

**Tamanho real do índice em prod (medido em 01/09, primeira medição registrada):
917.349 hashes distintos.** Para referência, 300 comerciais sintéticos do
benchmark produzem 538.009.

**Lição de modelagem:** custo por emissora **não é constante** — é
`f(emissoras × tamanho_do_índice)`. Um modelo linear só em emissoras
subestima sistematicamente, e o erro cresce junto com o catálogo. Toda projeção
de capacidade deste sistema precisa declarar contra qual tamanho de índice foi
medida.

### Fator secundário: overhead auto-infligido pela saturação

Com `cs` em 76–104 mil trocas de contexto/s, parte do custo por janela era
thrash de cache e de scheduler. Medido depois: a api saiu de **7,75 para 6,72
cores fazendo o mesmo trabalho** — **13% do custo era a própria saturação.**

---

## 5. Escolha da máquina — duas tentativas frustradas

O caminho até a máquina final tem valor documental, porque as duas primeiras
opções pareciam óbvias e nenhuma funcionou.

### 5.1. `c3-highcpu-16` não existe

```
ERROR: The resource '.../machineTypes/c3-highcpu-16' was not found
```

Não é cota: **a família C3 não tem shape de 16 vCPU.** Os tamanhos são
4 → 8 → **22** → 44 → 88 → 176 (layout NUMA do Sapphire Rapids + Titanium). O
degrau real acima do `c3-highcpu-8` é o `c3-highcpu-22`, um salto de 2,75× —
R$5.437,72/mês, capacidade para ~530 emissoras. Capacidade parada demais para
uma operação de 268.

### 5.2. `c4-highcpu-16` existe, é melhor e mais barato — e está bloqueado

O C4 (Intel Emerald Rapids) tem o shape 16/32, custa **R$200/mês menos** que a
opção escolhida e é mais rápido (Geekbench single 2.314 contra 1.666 do C2D).
Mas:

```
ERROR: pd-balanced disk type cannot be used by c4-highcpu-16 machine type
```

**C4 exige Hyperdisk.** Os três discos da VM são Persistent Disk
(`vm-e-monitor` boot, `radiocheck-db-ssd`, `radiocheck-data-hdd`). Converter
discos para Hyperdisk no meio de uma janela de manutenção não é aceitável —
virou follow-up (F-CAP-13).

> O erro veio **antes** da validação de "instância precisa estar parada", então
> o comando falhou sem efeito colateral: a VM seguiu `RUNNING` e intocada.

### 5.3. Decisão: `c2d-standard-16`

| máquina | vCPU / físicos | RAM | compute/mês | c/ flex 28% | **total/mês** |
|---|---|---|---|---|---|
| `c3-highcpu-8` *(anterior)* | 8 / 4 | 16 GiB | R$2.323,56 | R$1.672,96 | R$2.509,89 |
| `c4-highcpu-16` | 16 / 8 | 32 GiB | R$4.683,20 | R$3.371,90 | R$4.208,83 ❌ Hyperdisk |
| **`c2d-standard-16`** ✅ | **16 / 8** | **64 GiB** | R$4.961,49 | R$3.572,27 | **R$4.409,20** |
| `c3-highcpu-22` | 22 / 11 | 44 GiB | R$6.389,98 | R$4.600,79 | R$5.437,72 |

Descartado também o `t2d-standard-16` (16 cores **físicos**, sem SMT, PassMark
30.446): **T2D não aparece na lista de elegíveis do Compute Flexible CUD**
(`C3, C3D, C4, C4A, C4D, E2, N1, N2, N2D, N4, N4D, N4A` + `H3, H4D, C2, C2D`).
Migrar para lá perderia os 28% *e* manteria o commitment atual, que não pode
ser cancelado. Risco assimétrico demais.

### 5.4. Duas descobertas no extrato de agosto

**a) O flex CUD já existe — e é de 28%, não de 37%.**

```
C3 Core:  R$530,10 ÷ R$1.893,23 = 28,00%
C3 RAM:   R$120,49 ÷ R$  430,33 = 28,00%
```

Exatamente 28,00% nas duas linhas. Isso resolve uma dúvida que a documentação
pública do Google não responde: **C3 em São Paulo cai no Compute Flexible CUD
(28% / 1 ano, 46% / 3 anos), não no resource-based de 37%/55%.** O
[deploy.md](../operations/deploy.md) e o F-CAP-05 diziam "CUD nunca assinado,
zero em Programas de economia" — desatualizado desde ~19/08.

Consequência prática boa: **flex CUD é portátil entre famílias**, então o
desconto acompanha a migração para C2D sem ação nenhuma.

**b) Existe uma segunda VM não documentada, em Montreal.**

```
T2D AMD Core in Montreal:  739,44 core-h   → R$130,23
T2D AMD Ram  in Montreal: 2.957,77 GiB-h   → R$ 69,82
```

739,44 × 4 GiB = 2.957,76 — bate exato com **`t2d-standard-1` (1 vCPU, 4 GB)**
rodando 24/7 em `northamerica-northeast1`, **R$200,05/mês**. O IP externo de
1.478,88 h = 2 × 739,44 confirma dois IPs estáticos, um por VM. Propósito não
identificado (hipótese: proxy do plano de contorno do ban de IP). Follow-up
F-CAP-14.

---

## 6. Execução e resultado

Procedimento executado (detalhado em [deploy.md §4](../operations/deploy.md)):
verificação de IP reservado → backup verificado → snapshot dos 3 discos →
`docker compose stop` → `instances stop` → `set-machine-type` → `instances start`
→ `docker compose up -d`.

| | antes | depois |
|---|---|---|
| **p99 da janela de matching** | **2.390 ms** | **34,4 ms** *(69×)* |
| **PSI `some`** | 76,4% | **8,5%** |
| CPU total | 99–100% de 8 vCPU | **42% de 16 vCPU** |
| `id` | 0% | ~51% |
| Fila `r` | 19–38 | 1–11 |
| api (CPU) | 775% | 672% |
| api (RAM) | 7,43 GiB | 5,82 GiB *(sobe até ~7 conforme os rings enchem)* |
| RAM do host | 12,9 / 16 GB *(81%)* | 10,2 / 64 GB *(16%)* |

**A previsão de demanda (7,84 cores) errou por 4%** — consumo real medido em
7,5 cores no `vmstat` e 6,74 no `docker stats` já estabilizado.

Novo custo por emissora: **0,0236 core** (era 0,0289 sob saturação). Teto a 70%
de CPU: **~410–460 emissoras**, R$10,25/emissora no teto.

> **Observação sobre unit cost.** Antes do resize o custo era R$9,37/emissora —
> o mais baixo já registrado. Esse número era o **preço de rodar a 98% com o p99
> estourado**. O upgrade não comprou emissoras mais baratas; comprou margem
> operacional. Não compare unit cost entre regimes de utilização diferentes.

---

## 7. Armadilhas encontradas no caminho

Nenhuma causou dano, todas causariam se o procedimento fosse improvisado.

**7.1. Os containers não voltam sozinhos depois de um reboot da VM.**
Dos 25 serviços do `docker-compose.yml`, **só o `segments-cleanup` tem
`restart: unless-stopped`**. `postgres`, `api`, `minio`, `redis`, `nats`,
`prometheus` e `backup` estão todos no default `no`. Após `instances start`, o
Docker sobe e **nada mais**. O `docker compose up -d` manual é obrigatório.
Follow-up F-CAP-12.

**7.2. O `deploy.sh` é a ferramenta errada para manutenção de hardware.**
Ele faz `git pull` + rebuild + migrations — trocar máquina e software no mesmo
passo destrói a capacidade de saber o que quebrou. Além disso seu health check
tem 60 s (`seq 1 30` × `sleep 2`) e a API só escuta **depois** de carregar o
índice inteiro (`loader.LoadAll` precede `srv.ListenAndServe` em
`cmd/api/main.go`). Com Postgres frio pós-reboot isso passa de 60 s e o deploy
aborta por timeout sem que nada esteja errado. **Use `docker compose up -d`.**

**7.3. O `alertmanager` está fora do ar — e já estava antes.**
Ele não aparece no `docker compose ps` nem no `docker stats` **anterior** ao
resize. As regras de alerta do Prometheus não têm para onde disparar, o que
explica a máquina ter chegado a 100% sem ninguém ser avisado. Pré-existente,
não causado por esta migração. Follow-up F-CAP-11.

**7.4. A zona documentada estava errada.**
O `deploy.md` dizia `southamerica-east1-b` em dois lugares; a VM real está em
**`southamerica-east1-a`**. Corrigido nesta rodada.

**7.5. Verificar se o IP externo é reservado antes de parar a VM.**
Se fosse efêmero, o stop/start trocaria o IP e invalidaria qualquer allowlist
negociada com painéis de emissora. Confirmado reservado
(`radiocheck-prod-ip`, `34.39.163.110`, `IN_USE`) antes do stop. Este check
entrou no procedimento do `deploy.md`.

---

## 8. Perfil de CPU pós-migração e leveres identificados

Perfil de 30 s tirado **depois** do resize — perfil sob saturação é distorcido
(o tempo aparece onde há contenção, não onde há trabalho).

```
Go (pprof):          286,07%  =  2,86 cores
api (docker stats):  671,52%  =  6,72 cores
──────────────────────────────────────────
ffmpeg (diferença):             3,86 cores   ← 57% do total
```

Dentro dos 2,86 cores do Go (`runPCMReader` é 87,4% — é tudo o loop de matching):

| | % do Go | cores |
|---|---|---|
| `histogramFromHashes` *(lookup de índice)* | 31,6% | 0,90 |
| `PickPeaks` | 28,2% | 0,81 |
| ↳ **`sort` dentro de `percentile`** | **20,4%** | **0,58** |
| `STFT` / FFT (gonum) | 27,5% | 0,79 |
| resto (HPF, RMS, ReadLast) | ~4% | 0,11 |

**Descoberta:** [peaks.go](../../workers/pkg/audio/peaks.go) ordena o
espectrograma inteiro (`sort.Slice`, que ainda usa reflexão) para ler **um**
elemento — o 80º percentil. O custo aparece espalhado em seis entradas do
profile (`sort.pdqsort_func` 20,42%, `sort.partition_func` 16,23%,
`percentile.func1` 8,57%, `reflectlite.Swapper.func6`, `insertionSort_func`,
`median_func`). Quickselect é O(n) contra O(n log n) e devolve o mesmo valor.

**ffmpeg:** de 487 `ffmpeg: starting` numa janela limpa, **176 usaram
`-c:a copy` (36%)** e 311 (64%) caíram no caminho de re-encode
`-c:a aac -b:a 128k` ([ffmpeg.go](../../workers/internal/ingestor/ffmpeg.go)),
porque o muxer ADTS só aceita AAC. O custo relativo dos dois caminhos **não foi
medido**.

### Inventário de leveres

| lever | economia estimada | risco | follow-up |
|---|---|---|---|
| `percentile`: sort → quickselect | ~0,58 core | **baixíssimo** — mesmo valor, mesmos hashes | F-CAP-08 |
| ffmpeg sem re-encode nas 64% não-AAC | ~1,5 core *(a medir)* | médio-alto — muda formato da evidência | F-CAP-09 |
| índice filtrado por emissora | até 0,90 core | médio — muda semântica do noise sampler | F-CAP-10 |
| `programada` com horizonte de 7 dias | subconjunto de F-CAP-10 | baixo | F-CAP-10 |

Somados: até ~2,9 de 6,72 cores (43%), o que levaria o teto de ~460 para
**~740 emissoras** na mesma máquina.

Nenhum é urgente com a máquina a 42%. O do `percentile` é o de melhor relação
risco/retorno: função pura, resultado bit-a-bit idêntico, sem impacto em
fingerprint.

---

## 9. Ações

| # | Ação | Estado |
|---|---|---|
| 1 | Migrar para `c2d-standard-16` | ✅ feito 2026-09-01 |
| 2 | Atualizar `capacity-and-unit-cost.md` e `deploy.md` com a máquina e o CUD reais | ✅ feito 2026-09-01 |
| 3 | Ampliar o flex commitment para cobrir a máquina nova | ⏳ aguardando 5–7 dias de regime medido |
| 4 | Verificar que o desconto de 28% migrou para as linhas do C2D | ⏳ próximo ciclo de billing |
| 5 | Subir o `alertmanager` (F-CAP-11) | 🔴 aberto |
| 6 | Política de restart nos serviços do compose (F-CAP-12) | 🔴 aberto |
| 7 | Confirmar/desligar a VM t2d em Montreal (F-CAP-14) | 🔴 aberto |
| 8 | Leveres de CPU (F-CAP-08/09/10) | 🔴 aberto |
| 9 | Correlacionar p99 com `radiocheck_worker_reconnects_total` para fechar a hipótese de backpressure do §2 | 🔴 aberto |

## 10. Lições

1. **`us%` não dimensiona máquina saturada.** Use `/proc/pressure/cpu` (PSI) e
   custo unitário por unidade de trabalho, que não são clipados pelo teto.
2. **Custo por emissora não é constante** — depende do tamanho do índice.
   Declare o tamanho do índice em toda medição de capacidade.
3. **Unit cost baixo pode ser sintoma, não conquista.** R$9,37/emissora era o
   preço de rodar quebrado.
4. **Shapes de máquina não são regulares.** C3 pula de 8 para 22; C4 exige
   Hyperdisk. Valide `machine-types describe` e faça um `set-machine-type` de
   teste **antes** de agendar a janela.
5. **Manutenção de hardware não usa o pipeline de deploy de software.**

## Referências

- [capacity-and-unit-cost.md](../operations/capacity-and-unit-cost.md) — método de medição e unit economics
- [deploy.md](../operations/deploy.md) — especificação da VM e procedimento de resize
- [follow-ups-fase2.md](../roadmap/follow-ups-fase2.md) — F-CAP-01..14
- [incident-2026-05-12-pgdata-loss.md](incident-2026-05-12-pgdata-loss.md) — origem das regras 4.x do CLAUDE.md, seguidas aqui
