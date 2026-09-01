---
status: implementado
ultima-verificacao: 2026-09-01
codigo-relacionado:
  - infra/docker/docker-compose.yml
  - workers/internal/ingestor/worker.go
  - workers/internal/index/loader.go
  - workers/internal/metrics/metrics.go
  - docs/operations/deploy.md
---

# Capacidade e custo por emissora

Como medir quanto custa monitorar **uma** emissora, quantas cabem na VM atual,
e quais números usar para precificar.

> **Máquina atual: `c2d-standard-16` (16 vCPU / 64 GB), desde 2026-09-01.**
> Medições desta revisão feitas com **268 emissoras**. A revisão anterior
> (2026-07-21, 173 emissoras numa `c3-highcpu-8`) está preservada em §9 como
> histórico — os números dela **não valem mais para dimensionar**, mas a
> comparação entre as duas é que revelou o erro de modelagem da §4.
> Contexto completo da migração:
> [incident-2026-09-01](../incidents/incident-2026-09-01-cpu-saturation-vm-resize.md).

---

## 1. A resposta curta

| Número | Valor | Quando usar |
|---|---|---|
| **Custo médio** (total ÷ emissoras) | **R$16,45**/emissora/mês | comparar com o fornecedor externo |
| **Custo marginal** (a próxima emissora) | **~R$0** até o teto | decidir se aceita mais emissora/cliente |
| **Custo no teto de capacidade** | **~R$10,25**/emissora/mês | **precificar o serviço** |

Base: **R$4.409,20/mês** projetado com 268 emissoras, já com o Compute Flexible
CUD de 1 ano (28%) aplicado. Teto de capacidade: **~410–460 emissoras**.

**Use os R$10,25 para precificar.** Os R$16,45 embutem a folga da máquina nova
(58% de CPU livre), e repassá-la ao cliente é cobrar por capacidade parada —
ela desaparece sozinha conforme a operação cresce.

> ⚠️ **Não compare unit cost entre regimes de utilização diferentes.** Em
> 31/08, com a máquina antiga a 98% de CPU e o p99 de matching estourado, o
> custo era R$9,37/emissora — o mais baixo já registrado. Aquilo era o **preço
> de rodar quebrado**, não uma conquista. Unit cost só significa alguma coisa
> junto com a utilização em que foi medido.

---

## 2. Por que "custo total ÷ emissoras" não basta

A divisão simples responde uma pergunta (*quanto gastei por emissora no mês
passado?*) e é usada para responder outra (*quanto custa atender mais uma?*).
São números diferentes porque:

1. **Boa parte do custo é fixa.** Postgres, MinIO, Prometheus/Grafana/Jaeger,
   backup, API, discos, IP estático — nada disso escala com emissora.
2. **Storage é dirigido por detecção, não por emissora.** Uma emissora com 400
   tocadas/dia custa múltiplos de uma com 20.
3. **Storage é cumulativo.** A evidência cresce com o tempo mesmo com o número
   de emissoras parado.
4. **Custo marginal ≠ custo médio enquanto houver ociosidade.**
5. **O custo por emissora não é constante** — cresce com o tamanho do índice de
   matching. Ver §4, que é o achado mais importante deste documento.

---

## 3. Medições de produção (2026-09-01, 268 emissoras, `c2d-standard-16`)

| Recurso | Total | Por emissora | Escala com |
|---|---|---|---|
| CPU (`us`+`sy`) | ~42% de 16 vCPU = 6,74 cores | **~0,0236 vCPU** | emissora × índice |
| ↳ ffmpeg (268 processos) | 3,86 cores (57%) | 0,0144 vCPU | emissora |
| ↳ Go / matcher | 2,86 cores (43%) | 0,0107 vCPU | emissora × índice |
| RAM do container `api` (cgroup) | 5,82 GiB → ~7 GiB estabilizado | **~26 MB líquido** | emissora |
| PSI `some` (pressão de CPU) | 8,5% | — | — |
| p99 da janela de matching | 34,4 ms | — | — |
| Índice em memória | 917.349 hashes distintos | — | catálogo ativo |

### Achados que mudam decisões

**a. Use o cgroup para RAM, não a soma de RSS.** A ressalva de 2026-07-21
("`ps` double-conta páginas compartilhadas entre os ffmpeg, falta PSS") foi
resolvida sem PSS: **`docker stats` já reporta o consumo do cgroup do container
`api`, que é o número líquido.** Em 01/09: `ps` somava 11,75 GB de RSS enquanto
o cgroup reportava 5,82 GiB — 2× de dupla contagem. **F-CAP-04 fechado.**

**b. O ffmpeg é a maior fatia isolada, não o matcher.** 3,86 contra 2,86 cores.
Isso inverte a suposição de julho. E **64% das emissoras pagam re-encode AAC**
(`-c:a aac -b:a 128k`) porque o muxer ADTS só aceita AAC — as outras 36%
segmentam com `-c:a copy`, de graça. Amostra: 487 `ffmpeg: starting`, 176 com
`copy`. O custo relativo dos dois caminhos ainda não foi medido (F-CAP-09).

**c. Um quinto da CPU do Go é um sort desnecessário.** O perfil pós-migração
mostra 20,4% do processo Go em `sort` dentro de `percentile`
(`workers/pkg/audio/peaks.go`), que ordena o espectrograma inteiro para ler o
80º percentil. Ver F-CAP-08.

**d. O ingest não custa nada.** ~960 GB/dia entrando, mas é *ingress* no GCP —
tarifado a zero. Entra na conta de CPU (`si`/softirq), não na de rede.

---

## 4. O erro de modelagem que custou uma saturação

**Este é o achado mais importante deste documento.**

A revisão de 2026-07-21 modelava **0,022 vCPU por emissora**, constante.
Extrapolando para 268 emissoras:

```
0,4 (baseline) + 268 × 0,022 = 6,35 vCPU de 8 = 79%
```

O observado em 01/09 foi **99%**. O modelo errou por ~1,2 vCPU, e a máquina
saturou sem que a projeção previsse.

**Causa: o índice de matching é global e cresce independentemente do número de
emissoras.** O `index/loader.go` carrega todo material de campanha `ativa`
**ou** `programada`, e cada janela de matching faz lookup contra o índice
inteiro. No início do mês um bloco de campanhas transiciona para `ativa` e o
custo de **todas** as 134 janelas/s sobe junto.

Medição do efeito (`BenchmarkRealIndexPass` em
`workers/internal/match/engine_realindex_bench_test.go`):

| comerciais no índice | ms/janela | vs. base |
|---|---|---|
| 50 | 10,54 | — |
| 150 | 12,76 | **+21%** |
| 300 | 15,64 | **+48%** |

### A regra que sai disso

```
custo ≈ f(emissoras × tamanho_do_índice),  NÃO  f(emissoras)
```

**Toda medição de capacidade deste sistema tem que declarar contra qual tamanho
de índice foi feita.** Um número de vCPU/emissora sem essa referência é uma
armadilha: ele envelhece sozinho conforme o catálogo cresce, e o erro é sempre
na direção de subestimar.

Índice na medição atual: **917.349 hashes distintos**. Registre esse número
junto com qualquer nova medição.

### Fator secundário: a saturação se auto-alimenta

Com `cs` em 76–104 mil trocas de contexto/s na máquina antiga, parte do custo
era thrash de cache e de scheduler. Medido nos dois lados: a api saiu de **7,75
para 6,72 cores fazendo exatamente o mesmo trabalho** — **13% do custo era a
própria saturação.** Ou seja: perto do teto, o custo por emissora que você mede
já está inflado, e projetar a partir dele superdimensiona.

---

## 5. Teto de capacidade

| Alvo de CPU média | Emissoras | R$/emissora/mês |
|---|---|---|
| **70% (recomendado, preserva headroom de rajada)** | **~410–460** | **~R$10,25** |
| 80% (agressivo, só com p99 de matching validado) | ~490 | ~R$9,00 |

Conta: `(16 × 0,70 − 0,4 de baseline) ÷ 0,0236 vCPU/emissora`.

A faixa 410–460 existe porque as duas leituras de consumo discordam levemente
(`vmstat` 7,5 cores, `docker stats` 6,74 já estabilizado). **Use 410 para
planejar.**

> ⚠️ Este teto assume o índice de hoje (917 mil hashes). Se o catálogo ativo
> crescer, o teto **cai sozinho**. Reveja quando o índice passar de ~1,2 milhão.

### O gargalo é rajada, não CPU média

Os workers fecham janela de matching em cadência sincronizada de 2 s, então a
carga vem em ondas. Na máquina antiga isso lia como `load average` de 6,9/8 com
a CPU em 53%. **Não dimensione por `load average`** — use `vmstat 1 5` (linhas
2+) e, principalmente, `/proc/pressure/cpu`.

### O número que valida o teto

```bash
curl -sG 'http://localhost:9090/api/v1/query' --data-urlencode \
  'query=histogram_quantile(0.99, sum by (le) (rate(radiocheck_match_window_duration_seconds_bucket[1h])))'
```

**A cadência de janela é 2 segundos.** Se o p99 se aproximar disso, o teto já
foi ultrapassado e a latência de detecção está degradando. Referências:

| situação | p99 |
|---|---|
| saudável (01/09 pós-migração, 42% de CPU) | **34 ms** |
| saturado (31/08, 98% de CPU) | **2.390 ms** |

---

## 6. Procedimento de medição (reproduzir na VM)

### 6.1. Pressão de CPU — comece por aqui

```bash
cat /proc/pressure/cpu
```

`some avgN` é a **fração do tempo em que havia tarefa pronta esperando CPU**.
Ao contrário de `us%`, **não satura no teto** — é a métrica correta para
responder "quão longe do limite eu estou".

| `some avg300` | leitura |
|---|---|
| < 10% | saudável (atual: 8,5%) |
| 10–40% | apertando, planeje |
| > 40% | máquina pequena demais |
| > 60% | saturado (31/08: 76%) |

`full > 0` significa que *todas* as tarefas travaram ao mesmo tempo — sempre
foi 0 aqui, inclusive sob saturação.

### 6.2. CPU real — não use `uptime`

```bash
vmstat 1 5
```

Leia as linhas **2 em diante** (a primeira é média desde o boot). `us`+`sy` é a
CPU real, `wa` é I/O wait, `r` é a fila de execução, `cs` são trocas de
contexto. Fila muito acima do número de vCPUs = oversubscription.

### 6.3. Repartição por componente

```bash
docker stats --no-stream
```

Os ffmpeg são subprocessos do processo Go, então entram no número do container
`api`. Para separar:

```bash
ps -eo pcpu,rss,args --no-headers \
| awk '/ffmpeg/ && !/awk/ {c+=$1; r+=$2; n++}
       END {printf "procs: %d | CPU: %.1f%% = %.2f cores | RSS bruto: %.2f GB\n", n, c, c/100, r/1048576}'
```

> **RAM:** use o `MEM USAGE` do `docker stats` (cgroup, líquido), **não** a soma
> de RSS do `ps` — ela double-conta as páginas compartilhadas entre os ffmpeg
> por um fator de ~2×.

### 6.4. Demanda real — o método que funciona com a máquina saturada

`us%` clipa em 100% e não diz quanto o sistema **quer**. Custo de CPU *por
janela* é custo por unidade de trabalho e não clipa:

```bash
# janelas efetivamente processadas por segundo
curl -sG 'http://localhost:9090/api/v1/query' --data-urlencode \
  'query=sum(rate(radiocheck_match_window_duration_seconds_count[5m]))'
```

```
CPU-s por janela   = (cores do container api − cores de ffmpeg) ÷ janelas/s
demanda necessária = (emissoras ÷ 2) × CPU-s por janela + cores de ffmpeg + baseline
```

O `emissoras ÷ 2` é a taxa de janelas **requerida** pela realidade (1 janela a
cada 2 s por emissora), fixa e independente da CPU disponível — é isso que faz
a conta escapar do clipping.

Em 31/08 esse método previu **7,84 cores** de demanda; o consumo medido depois
do resize foi 6,74–7,5. **Erro de 4%.**

### 6.5. Perfil de CPU do processo Go

```bash
docker exec docker-api-1 wget -qO - \
  'http://127.0.0.1:6060/debug/pprof/profile?seconds=30' > cpu.pprof

docker run --rm -v "$PWD":/w -w /w golang:1.26-alpine \
  go tool pprof -top -nodecount=40 cpu.pprof
```

> **Tire o perfil com a máquina fora de saturação.** Perfil sob contenção é
> distorcido: o tempo aparece onde há espera, não onde há trabalho.

O pprof cobre **só o processo Go** — os ffmpeg não aparecem. Para o custo do
ffmpeg, subtraia: `docker stats da api − total do pprof`.

### 6.6. Tamanho do índice

```bash
docker logs docker-api-1 --since 48h 2>&1 | grep 'index loaded' | tail -3
```

Custo zero — o loader já loga. **Registre este número junto com toda medição de
capacidade** (ver §4).

### 6.7. Bytes e evidência (Prometheus)

```bash
curl -sG 'http://localhost:9090/api/v1/query' --data-urlencode \
  'query=sum(increase(radiocheck_worker_bytes_received_total[7d]))'
curl -sG 'http://localhost:9090/api/v1/query' --data-urlencode \
  'query=radiocheck_evidence_storage_bytes'
```

> **Não existe métrica de CPU/RAM no Prometheus.** O `node-exporter` roda com
> `--collector.disable-defaults` (só textfile, para o backup). CPU e RAM saem de
> `vmstat`/`ps`/`docker stats` na VM ou do Cloud Monitoring.

### 6.8. O sanity check que dispensa modelo

Desligue N emissoras por 48h e meça a queda do custo/dia no billing. A
inclinação é o `v_e` medido, sem rateio e sem premissa.

---

## 7. Composição do custo e alavancas

Projeção mensal na máquina nova, a partir dos preços unitários derivados do
extrato real de agosto/2026:

| Grupo | R$/mês | % |
|---|---|---|
| **Compute** (`c2d-standard-16`, já com flex CUD 28%) | 3.572,27 | 81,0% |
| Discos (Balanced + SSD PD) | 320,85 | 7,3% |
| VM `t2d-standard-1` em **Montreal** (propósito não identificado) | 200,05 | 4,5% |
| Egress (~193 GB/mês) | 190,80 | 4,3% |
| Snapshots/imagem **nos EUA** | 91,30 | 2,1% |
| IP estático (2 IPs) | 21,37 | 0,5% |
| Linha de commitment | 12,56 | 0,3% |
| **TOTAL** | **~4.409** | |

Preços unitários de São Paulo, derivados do billing (não de tabela pública):

```
C3 Core: R$0,32006 / core-hora      C3 RAM: R$0,03637 / GiB-hora
```

### As alavancas, em ordem

**1. Ampliar o flex commitment — ~R$650/mês. Pendente.**
O commitment atual absorve ~R$3,14/h, que era a máquina antiga inteira. A nova
consome ~R$6,71/h, então **a metade nova roda a preço cheio** até o commitment
ser ampliado. Ver §8 para o procedimento e a ressalva de dimensionamento.

**2. Leveres de CPU — até 43% da CPU, sem custo recorrente.**
`percentile` sem sort (F-CAP-08, ~0,58 core), ffmpeg sem re-encode (F-CAP-09,
~1,5 core estimado) e índice filtrado por emissora (F-CAP-10, até 0,90 core).
Somados levariam o teto de ~460 para **~740 emissoras na mesma máquina** —
adiando a próxima compra de hardware indefinidamente.

**3. VM em Montreal — R$200,05/mês.** Não documentada, propósito não
identificado (F-CAP-14).

**4. Snapshots armazenados nos EUA — R$91,30/mês.** Três SKUs revelam que os
snapshots de uma VM de São Paulo vivem na América do Norte, pagando
transferência intercontinental (F-CAP-06). Ao criar snapshots manualmente, passe
`--storage-location=southamerica-east1`.

**5. Migrar para C4 — ~R$200/mês + ~34% por core.** Bloqueado: C4 exige
Hyperdisk e os discos atuais são Persistent Disk (F-CAP-13).

---

## 8. Committed Use Discount — o que está contratado

**Compute Flexible CUD de 1 ano, assinado ~2026-08-19, aplicando 28,00%.**

Confirmado aritmeticamente no extrato de agosto: `R$530,10 ÷ R$1.893,23 =
28,00%` no core e `R$120,49 ÷ R$430,33 = 28,00%` na RAM.

Isso resolve uma dúvida que a documentação pública do Google não responde
diretamente: **C3 e C2D em São Paulo caem no Compute Flexible CUD (28% / 1 ano,
46% / 3 anos), não no resource-based de 37%/55%.** Estimativas anteriores neste
repositório que usavam 37% estavam erradas.

Propriedades que importam:

- **Portátil entre famílias e regiões.** O desconto acompanhou a migração
  C3 → C2D sem ação nenhuma. T2D, porém, **não** está na lista de elegíveis.
- **É compromisso de gasto por hora, não de máquina.** Consumo acima do
  comprometido roda a preço cheio.
- **Não pode ser cancelado nem redimensionado.** Para aumentar cobertura,
  compra-se um commitment adicional.

### Como dimensionar o incremento

Não projete — **meça**. Depois de 5–7 dias de regime na máquina nova, leia o
gasto horário de compute no relatório de billing e comprometa o **piso**, nunca
o pico: flex CUD não reembolsa folga.

Compra pelo Console (**Faturamento → Descontos por uso contínuo → Comprar**).
Não há forma `gcloud` confirmada para a compra *spend-based* — o
`gcloud compute commitments create --resources vcpu=...` que aparece na
documentação é o **resource-based**, que é outro produto. Para uma compra
irreversível de 12 meses, o Console mostra valor e preço antes de confirmar.

---

## 9. Histórico — medição de 2026-07-21 (superada)

Preservada porque a comparação entre as duas revisões é o que expôs o erro de
modelagem da §4. **Não use para dimensionar.**

`c3-highcpu-8` (8 vCPU / 16 GB), 173 emissoras, R$2.918/mês sem CUD:

| Recurso | Total | Por emissora |
|---|---|---|
| CPU (`us`+`sy`) | ~53% de 8 vCPU | ~0,022 vCPU |
| CPU só dos ffmpeg | 2,09 cores | 0,012 vCPU |
| RAM (Σ RSS dos ffmpeg) | 7,59 GB | 44,9 MB bruto / ~30 MB estimado |
| Ingest de stream | 960 GB/dia | 169 GB/mês |
| Evidência (tier `hot`) | 44,8 GB | ~260 MB/detecção |

Custo médio R$16,87/emissora; teto projetado de ~235 emissoras a 70% de CPU.
**O teto se mostrou otimista** — a máquina saturou em 268 porque o modelo linear
ignorava o crescimento do índice.

> A quebra por SKU de julho, que desmentiu a estimativa original de projeto
> (previa VM 71% / discos 24%; o real foi compute 79,6% / discos 10,6%), segue
> em [deploy.md §6](deploy.md).

---

## 10. Limitações conhecidas destas medições

- **Ponto único no tempo** (2026-09-01, ~12h, logo após o resize). A RAM da api
  (5,82 GiB) ainda subia — os ring buffers de 35 s enchem ao longo de minutos.
  Espere estabilizar perto de 7 GiB.
- **Baseline fixo estimado, não medido.** Os ~0,4 core de stack fixa são
  estimativa; medir de verdade exige derrubar os workers.
- **Custo relativo copy × re-encode do ffmpeg não medido** (F-CAP-09) — é o
  maior componente isolado de CPU e o que menos se sabe sobre ele.
- **`radiocheck_detections_total` continua sem série** (F-CAP-01), então o termo
  `v_d × Detecções` do modelo de custo segue sem poder ser calculado.

---

## 11. Follow-ups abertos por estas medições

| # | Achado | Impacto |
|---|---|---|
| F-CAP-01 | `radiocheck_detections_total` sem nenhuma série | Cega o custo por detecção |
| F-CAP-02 | Tiering `cold`/`archive` zerado — não move nada | Evidência acumula no tier caro |
| ~~F-CAP-03~~ | ~~Gap sem quebra por SKU~~ | ✅ **Resolvido 2026-07-21** |
| ~~F-CAP-04~~ | ~~RAM líquida por emissora não fechada (falta PSS)~~ | ✅ **Resolvido 2026-09-01** — o cgroup do `docker stats` já é o número líquido: ~26 MB/emissora |
| ~~F-CAP-05~~ | ~~CUD nunca assinado~~ | ✅ **Resolvido ~2026-08-19** — flex 1 ano a 28%. Sucessor: **ampliar o commitment** para a máquina nova |
| F-CAP-06 | Snapshots/imagem nos **EUA** + egress acima do previsto | ~R$91/mês + ~R$191/mês |
| F-CAP-07 | Discos provisionados divergem do documentado | Parcialmente fechado: são 3 discos (`vm-e-monitor`, `radiocheck-db-ssd`, `radiocheck-data-hdd`) |
| **F-CAP-08** | **`percentile` ordena o espectrograma inteiro para ler 1 elemento** | **~0,58 core (20,4% do Go), risco baixíssimo** |
| **F-CAP-09** | **64% das emissoras pagam re-encode AAC; custo relativo não medido** | **~1,5 core estimado** |
| **F-CAP-10** | **Índice global: cada janela pontua contra todos os materiais do sistema, mas só os da emissora viram state machine** | **até 0,90 core; ataca a causa raiz da §4** |
| **F-CAP-11** | **`alertmanager` fora do ar** | **Alertas do Prometheus não disparam para ninguém** |
| **F-CAP-12** | **24 de 25 serviços do compose sem política de `restart`** | **Stack não volta sozinha após reboot da VM** |
| **F-CAP-13** | **C4 exigiria Hyperdisk; discos atuais são PD** | ~R$200/mês + ~34% por core, bloqueado |
| **F-CAP-14** | **VM `t2d-standard-1` em Montreal, propósito não identificado** | **R$200,05/mês** |
