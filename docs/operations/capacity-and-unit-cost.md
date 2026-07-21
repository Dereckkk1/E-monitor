---
status: implementado
ultima-verificacao: 2026-07-21
codigo-relacionado:
  - infra/docker/docker-compose.yml
  - workers/internal/metrics/metrics.go
  - docs/operations/deploy.md
---

# Capacidade e custo por emissora

Como medir quanto custa monitorar **uma** emissora, quantas cabem na VM atual,
e quais números usar para precificar. Medições de produção em **2026-07-21**
com **173 emissoras** ativas numa `c3-highcpu-8` (8 vCPU / 16 GB).

---

## 1. A resposta curta

| Número | Valor | Quando usar |
|---|---|---|
| **Custo médio** (total ÷ emissoras) | **R$16,85**/emissora/mês | comparar com o fornecedor externo |
| **Custo marginal** (a próxima emissora) | **~R$0** até o teto | decidir se aceita mais emissora/cliente |
| **Custo no teto de capacidade** | **~R$12,50**/emissora/mês | **precificar o serviço** |

Base: R$97/dia = **R$2.949/mês** com 173 emissoras.

**Use os R$12,50 para precificar.** Os R$16,85 embutem a ociosidade atual da
máquina (47% de CPU livre) — repassá-la ao cliente é cobrar por capacidade
parada, e ela desaparece sozinha conforme a operação cresce.

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
   de emissoras parado, então R$/emissora/mês hoje ≠ daqui a 6 meses.
4. **Custo marginal ≠ custo médio enquanto houver ociosidade.** Com 47% de CPU
   livre, a emissora 174 não aumenta a fatura em nada.

O modelo correto tem duas parcelas:

```
Custo_mês = F  +  v_e × Emissoras  +  v_d × Detecções
```

---

## 3. Medições de produção (2026-07-21, 173 emissoras)

| Recurso | Total | Por emissora | Escala com |
|---|---|---|---|
| CPU (`us`+`sy`) | ~53% de 8 vCPU | **~0,022 vCPU** | emissora |
| CPU só dos ffmpeg | 2,09 cores | 0,012 vCPU | emissora |
| RAM (Σ RSS dos ffmpeg) | 7,59 GB | 44,9 MB bruto / **~30 MB líquido** | emissora |
| Ingest de stream | 6,72 TB / 7d (960 GB/dia) | 5,55 GB/dia = **169 GB/mês** | emissora |
| Evidência (tier `hot`) | 44,8 GB | ~260 MB | detecção |

### Achados que mudam decisões

**a. O ffmpeg é só ~30% do custo variável de CPU.** Os 173 processos somam
2,09 cores, mas a máquina usa ~4,2. O restante é majoritariamente o processo
Go fazendo *matching* — que também é trabalho **por emissora**, só que num
processo único que o `ps` não separa por station. **Medir apenas os ffmpeg
subestima o custo por emissora em ~3×.**

**b. O ingest não custa nada.** São 960 GB/dia entrando, mas é *ingress* no
GCP — tarifado a zero. A rede não entra na conta de custo por emissora.
(Entra na de CPU: `si`/softirq está em 4,2%.)

**c. A carga é em rajada, e isso — não a CPU média — é o teto real.** Ver §4.

---

## 4. Teto de capacidade: o gargalo é burst, não CPU média

Com a CPU em 53%, o `load average` fica em ~6,9 de 8 e o `r` do `vmstat`
oscila entre **1 e 23**. Vinte e três processos disputando 8 cores com 47% de
idle na média significa **carga em ondas**: os workers fecham janela de
matching em cadência sincronizada, saturam os cores por instantes, e a máquina
fica ociosa entre as ondas.

**Consequência:** subir a CPU média para 80% transformaria picos de `r=23` em
`r=35+`, e as janelas de matching passariam a esperar na fila — degradando
latência de detecção **antes** de qualquer alarme de CPU disparar.

| Alvo de CPU média | Emissoras | R$/emissora/mês |
|---|---|---|
| 70% (recomendado, preserva headroom de rajada) | **~235** | **R$12,55** |
| 80% (agressivo, só com p99 de matching validado) | ~273 | R$10,80 |

Conta: 53% − ~5% de baseline fixo = 48% variável ÷ 173 = 0,022 vCPU/emissora.

### Como validar o teto antes de encostar nele

O número que decide entre 235 e 273 já está instrumentado:

```bash
curl -sG 'http://localhost:9090/api/v1/query' \
  --data-urlencode 'query=histogram_quantile(0.99, sum by (le) (rate(radiocheck_match_window_duration_seconds_bucket[1h])))'
```

Se o p99 estiver bem abaixo do intervalo entre janelas, há folga e o teto sobe.
Se estiver encostando, o teto é **abaixo** de 235 e o gargalo é burst.

---

## 5. Procedimento de medição (reproduzir na VM)

Todos rodam **na VM**, direto no host.

### CPU real — não use `uptime`

```bash
vmstat 1 5
```

Leia as linhas **2 em diante** (a primeira é média desde o boot). `us`+`sy` é
a CPU real; `wa` é I/O wait; `r` é a fila de execução. **O `load average` do
`uptime` engana nesta máquina** — conta processos em rajada e lê ~6,9 quando a
CPU está em 53%.

### RAM e CPU dos ffmpeg

```bash
ps -eo pcpu,rss,args --no-headers \
| awk '/ffmpeg/ && !/awk/ {c+=$1; r+=$2; n++}
       END {printf "procs: %d\nCPU: %.1f%% -> %.3f%%/emissora\nRSS: %.2f GB -> %.1f MB/emissora\n", n, c, c/n, r/1048576, r/1024/n}'
```

`pcpu` é a média de CPU ao longo da vida do processo — que é o que se quer
para custo, não o pico instantâneo.

> **Ressalva:** a soma de RSS **double-conta** páginas compartilhadas entre os
> 173 ffmpeg (mesmo binário, mesmas libs). O líquido sai de `used` − `shared`
> no `free -m` (cuidado: `shared` também inclui os 2 GB de `shared_buffers` do
> Postgres). Fechar o número exige PSS via `smaps_rollup` — **não medido**.
> Planeje com **30 MB** central e **45 MB** pior caso.

### Bytes e detecções por emissora (Prometheus)

```bash
# bytes ingeridos 7d, total e por emissora
curl -sG 'http://localhost:9090/api/v1/query' \
  --data-urlencode 'query=sum(increase(radiocheck_worker_bytes_received_total[7d]))'
curl -sG 'http://localhost:9090/api/v1/query' \
  --data-urlencode 'query=topk(20, sum by (station_id) (increase(radiocheck_worker_bytes_received_total[7d])))'

# storage de evidência por tier
curl -sG 'http://localhost:9090/api/v1/query' \
  --data-urlencode 'query=radiocheck_evidence_storage_bytes'
```

> **Não existe métrica de CPU/RAM no Prometheus.** O `node-exporter` roda com
> `--collector.disable-defaults` (só textfile, para o backup) —
> [docker-compose.yml](../../infra/docker/docker-compose.yml). CPU e RAM saem
> de `vmstat`/`ps` na VM ou do Cloud Monitoring do GCP.

### O sanity check que dispensa modelo

Desligue N emissoras por 48h e meça a queda do custo/dia no billing. A
inclinação é o `v_e` medido, sem rateio e sem premissa.

---

## 6. A maior alavanca de custo não é por emissora

Otimizar custo por emissora rende, no melhor caso, a diferença entre R$16,85 e
R$12,55 — e rende **sozinho**, só enchendo a máquina até ~235.

Já a conta tem **~US$88/mês (20%) acima do projetado** e sem explicação: o
[deploy.md §6](deploy.md#6-análise-de-custos) estimava US$429/mês, o real é
~US$517. E ~29% do custo total são discos (300 GB SSD PD = US$76,50 + 300 GB
HDD = US$18 + boot 50 GB) que **não escalam com emissora nenhuma** e nunca
foram auditados quanto a uso real.

**Ação pendente:** GCP Console → Billing → Reports → agrupar por SKU.
Reconciliar contra a tabela do `deploy.md` e conferir a ocupação real dos
discos (`df -h /mnt/db /mnt/data`) antes de qualquer otimização por emissora.

---

## 7. Limitações conhecidas destas medições

- **Um único ponto no tempo** (2026-07-21, ~11h). Sem série histórica, não
  captura variação por horário/dia da semana. A carga de matching depende do
  volume de tocadas, que varia.
- **Baseline fixo estimado, não medido.** Os "~5% de CPU de stack fixa" são
  estimativa. Medir de verdade exige derrubar os workers e ler a CPU ociosa.
- **RAM líquida não fechada** (falta PSS — ver §5).
- **`radiocheck_detections_total` não retorna série nenhuma** em 7 dias, então
  o termo `v_d × Detecções` do modelo **não pôde ser calculado**. Ver §8.

---

## 8. Follow-ups abertos por esta medição

| # | Achado | Impacto |
|---|---|---|
| 1 | `radiocheck_detections_total` sem nenhuma série em 7d | Cega o custo por detecção e a taxa de detecção no Prometheus |
| 2 | `radiocheck_evidence_storage_bytes{tier="cold"}` e `{tier="archive"}` zerados — tiering não move nada | Evidência acumula no tier caro indefinidamente |
| 3 | Gap de ~US$88/mês entre custo projetado e real, sem quebra por SKU | ~20% da fatura não explicada |
| 4 | RAM líquida por emissora não fechada (falta PSS) | Dimensionamento acima de 200 emissoras fica com incerteza de ~50% |
