---
status: implementado
ultima-verificacao: 2026-07-21
codigo-relacionado:
  - scripts/deploy.sh
  - infra/docker/docker-compose.yml
  - infra/docker/docker-compose.override.yml
  - infra/docker/.env.example
---

# Deploy em Produção — Radiocheck

> Última atualização: 2026-05-08  
> Fase: 2 (Hardening) | Stack: Go API + PostgreSQL 16 + Redis 7 + NATS 2.10 + MinIO + Prometheus + Grafana + Jaeger + Python fingerprint + CLAP verifier + React/Vite

---

## Índice

1. [Análise do sistema](#1-análise-do-sistema)
2. [Domínio: Hostgator → Cloudflare](#2-domínio-hostgator--cloudflare)
3. [Cloudflare Pages — Frontend](#3-cloudflare-pages--frontend)
4. [Google VM — Especificações](#4-google-vm--especificações)
5. [Google VM — Configuração do zero](#5-google-vm--configuração-do-zero)
6. [Análise de custos](#6-análise-de-custos)
7. [Rotina de deploy](#7-rotina-de-deploy)

---

## 1. Análise do sistema

### Serviços e consumo de recursos

> ⚠️ **Medição PRÉ-tuning (08/05/2026).** Esta tabela foi capturada antes da
> Fase 1 de otimização de performance (2026-07-17), que subiu
> `shared_buffers`/`work_mem`/`effective_cache_size` do Postgres e adicionou
> `mem_limit`/`mem_reservation` no compose — ver seção "Tuning de Postgres e
> limites de memória" logo abaixo. Depois de aplicar os valores de prod, o
> `postgres` tende a encostar bem mais perto do teto de `shared_buffers`
> (2GB) conforme o cache aquece, então a linha do `postgres` nesta tabela e o
> "Total" da projeção ficam desatualizados. **Re-medir e atualizar aqui**
> após rodar em prod com os novos valores — não confie nestes números pra
> validar a Fase 1.
>
> **Parcialmente re-medido em 2026-07-21** (173 emissoras em prod): a linha do
> `api` e a projeção de 200 emissoras foram corrigidas — ver
> [capacity-and-unit-cost.md](capacity-and-unit-cost.md). **A linha do
> `postgres` continua sendo a medição pré-tuning de 08/05** e segue pendente
> de re-medição.

| Serviço | Container | RAM (medida) | Escala com emissoras? |
|---|---|---|---|
| API Go (supervisor + workers) | `api` | ~88 MB base + **~45 MB/emissora** (RSS bruto; ~30 MB líquido) | Sim |
| PostgreSQL 16 | `postgres` | ~72–200 MB | Cresce com dados |
| Redis 7 | `redis` | ~7 MB | Não |
| NATS 2.10 (JetStream) | `nats` | ~5 MB | Não |
| MinIO | `minio` | ~74–82 MB | Não |
| Python fingerprint | `fingerprint` | ~77 MB | Não |
| CLAP verifier (neural) | `clap-verifier` | ~47 MB | Não |
| Prometheus | `prometheus` | ~26 MB | Não |
| Grafana | `grafana` | ~57–60 MB | Não |
| Alertmanager | `alertmanager` | ~128 MB | Não |
| Jaeger | `jaeger` | ~11 MB | Não |
| Backup daemon | `backup` | ~1 MB | Não |
| Segments cleanup | `segments-cleanup` | ~1 MB | Não |

### Projeção para 200 emissoras

> ⚠️ **Corrigido em 2026-07-21 com medição de prod.** A projeção anterior
> (~3.0 GB para o `api`) vinha de um stress test de 50 workers em 08/05/2026 e
> **subestimava em ~2-3×**. Medição real com 173 emissoras em produção: Σ RSS
> dos 173 ffmpeg = **7.59 GB** (44.9 MB/processo). Método e ressalvas em
> [capacity-and-unit-cost.md](capacity-and-unit-cost.md).

| Componente | RAM estimada |
|---|---|
| API (88 MB base + 200 × ~30 MB líquido) | **~6.1 GB** (até ~9 GB por RSS bruto) |
| Stack fixo (todos os outros containers) | ~400 MB |
| PostgreSQL (`shared_buffers` 2 GB + work_mem sob concorrência) | ~2.5–5 GB |
| Fingerprint index em memória (~300 comerciais) | ~40 MB |
| **Total** | **~9–12 GB de 16 GB** |

**Consequência prática:** a folga de RAM a 200 emissoras é bem menor do que a
tabela antiga sugeria. Ainda cabe nos 16 GB, mas sem o conforto de "4–5 GB de
14". Se for passar de ~200 emissoras, dimensione RAM junto com CPU — não
assuma que só a CPU é o gargalo.

> **Ressalva de método:** os 44.9 MB são RSS por processo, que **double-conta**
> páginas compartilhadas entre os 173 ffmpeg (mesmo binário e mesmas libs). O
> líquido real é menor — a subtração `used` − `shared` do `free -m` situa em
> ~30 MB/emissora. Fechar o número exige PSS (`smaps_rollup`), não medido.
> Para planejamento, use **30 MB** como estimativa central e **45 MB** como
> pior caso.

### Tuning de Postgres e limites de memória (Fase 1 — performance, 2026-07-17)

Desde a Fase 1 de otimização de performance, o `command:` do service
`postgres` e os `mem_limit`/`mem_reservation` de `postgres`/`minio` em
`infra/docker/docker-compose.yml` são parametrizados por env var. Defaults =
valores de fábrica do PG16 / sem limite (no-op em dev). Setar no `.env` da VM
(Bloco I abaixo):

> ⚠️ **Estes valores foram calculados contra a VM de 16 GB e ainda não foram
> revisados para os 64 GB atuais** (migração de 2026-09-01). Toda a conta
> apertada abaixo — as duas rodadas de revisão que levaram `work_mem` a 8 MB e
> `PG_MEM_LIMIT` a 6g — existe *porque* a máquina antiga era pequena. Com
> 64 GB há folga para afrouxar, e as queries pesadas de dashboard se
> beneficiariam. **Mas isso exige recriar o `postgres`**, então vale a regra
> 4.1 do CLAUDE.md (`--no-deps` obrigatório) e uma janela própria — não faça
> junto de outra mudança. Enquanto não for revisado, os valores atuais são
> conservadores e seguros, só desperdiçam RAM.

| Var | Dev (default) | Prod (valores dimensionados p/ a VM de 16 GB — ver nota) | Motivo |
|---|---|---|---|
| `PG_SHARED_BUFFERS` | `128MB` | `2GB` | Cache dedicado do PG — cabe no SSD `/mnt/db` (300GB) e na RAM da VM. |
| `PG_EFFECTIVE_CACHE_SIZE` | `4GB` | `5GB` | `shared_buffers` (2GB) + page cache realista (~3GB). NÃO 8GB: o box de 16GB é compartilhado com `api` (**~6GB** projetado com os ffmpeg a 200 emissoras — número corrigido em 2026-07-21, era ~3GB; ver tabela acima), `minio` (1GB) e stack fixa (~0.5GB) + OS (~1GB); um valor inflado engana o planner a superestimar cache hits. **Revisitar:** com o `api` no dobro do previsto, o page cache realista encolhe — `5GB` pode estar otimista. |
| `PG_WORK_MEM` | `4MB` | `8MB` | Alocado por-nó de sort/hash, não por conexão. **Não é 16MB** — ver conta abaixo; 16MB estoura o `PG_MEM_LIMIT` na ponta adversa do intervalo de nós da `daily_play_summary`. |
| `PG_MAINTENANCE_WORK_MEM` | `64MB` | `512MB` | Usado por `VACUUM`/`CREATE INDEX` manual. **Não** é herdado pelo autovacuum se a var abaixo estiver setada. |
| `PG_AUTOVACUUM_WORK_MEM` | `-1` (default do PG = herda `maintenance_work_mem`) | `128MB` | **Crítico.** Sem essa var explícita, os 3 workers de autovacuum (`autovacuum_max_workers` default 3) herdam `maintenance_work_mem` inteiro — 3×512MB=1.5GB — que somado a `shared_buffers` + `work_mem` sob concorrência estoura o `PG_MEM_LIMIT` (ver conta abaixo). |
| `PG_EFFECTIVE_IO_CONCURRENCY` | `200` | `200` | Todo ambiente aqui roda em container Linux/SSD — sem motivo pra diferenciar dev/prod. |
| `PG_MAX_WAL_SIZE` | `1GB` | `4GB` | Menos checkpoints sob carga de escrita. |
| `PG_MEM_RESERVATION` | `256m` | `3g` | → `memory.low` (cgroup v2): prioridade de **reclaim**, *não* garantia. O kernel prefere reclamar páginas de quem está acima da própria reserva antes de mexer no `postgres` — **reduz fortemente** a chance dele ser a vítima sob pressão moderada (ex.: `api` com N ffmpeg crescendo). Sob exaustão global severa o OOM killer age por `oom_score` por-processo e ignora `memory.low`: não há imunidade. Doc do Docker: *"Docker attempts to keep the container's memory within the soft limit; however, this isn't guaranteed."* |
| `PG_MEM_LIMIT` | `0` (sem limite) | `6g` | → `memory.max`: teto **duro**. **Não protege o postgres** — cria uma forma *nova* dele morrer (OOM-kill escopado ao próprio cgroup se o consumo passar do teto). Só é aceitável como backstop de blast radius porque a conta abaixo fecha na ponta **adversa** do pior caso. |
| — | — | — | **O que realmente segura o postgres** não é nenhum dos dois knobs acima: é o consumo ser *bounded por config* (`shared_buffers` fixo + `work_mem` × cap do pool + `autovacuum_work_mem` explícito). Os knobs de cgroup são rede de segurança, não o plano. Se a conta não fechar, `mem_limit` deixa de ser rede e vira gatilho. |
| `MINIO_MEM_LIMIT` | `0` (sem limite) | `1g` | Hard cap do MinIO. |

**Conta de memória do Postgres sob `PG_MEM_LIMIT=6g` (6 GiB = 6144 MiB)** —
histórico de duas rodadas de revisão, ambas encontradas por code review
quantitativo pós-implementação:

- **Revisão #1 (2026-07-17):** os valores originalmente planejados,
  `shared_buffers=3GB` + `work_mem=32MB` + `effective_cache_size=8GB`
  **sem** `autovacuum_work_mem` explícito, chegavam a ~5.8GB só com 10
  conexões concorrentes e passariam de 9GB no pior caso de 40 conexões —
  estourando o próprio `PG_MEM_LIMIT` que esta task existe pra impor.
- **Revisão #2 (2026-07-17):** a correção da #1 usou `work_mem=16MB` e
  calculou o pior caso com **n=4 nós** de sort/hash — mas a `daily_play_summary`
  (`migrations/0041_detection_campaigns.up.sql`; 2 FULL OUTER JOIN + 2 GROUP BY
  + generate_series) tem um intervalo **estrutural** de 4-8 nós, e usar só a
  ponta favorável do intervalo é cherry-pick. Em n=8, `work_mem=16MB` dá
  `2048 + 384 + (40×8×16) + 300 = 7852 MiB ≈ 7.67 GiB` — **estoura o limite de
  6 GiB em ~1.67 GiB**. Corrigido para `work_mem=8MB`.

**Conta final, nos dois extremos do intervalo de nós** (fixos: `shared_buffers`
2048 MiB + autovacuum 3×128MB=384 MiB + overhead ~300 MiB = 2732 MiB; variável:
`DB_MAX_CONNS=40` × nós × `work_mem`):

| Nós (n) | `work_mem` × 40 conns | Total | vs. limite 6144 MiB |
|---|---|---|---|
| 4 (favorável) | 40×4×8MB = 1280 MiB | **4012 MiB ≈ 3.92 GiB** | folga de ~2.2 GiB |
| 8 (adverso) | 40×8×8MB = 2560 MiB | **5292 MiB ≈ 5.17 GiB** | folga de ~0.9 GiB |

Fecha nos dois extremos do intervalo declarado — não só na ponta favorável.
`work_mem=8MB` ainda é 2× o default de fábrica (4MB). A query que mais
pressiona esse número é justamente a `daily_play_summary`, e a Fase 3 deste
mesmo plano de performance a substitui por função parametrizada com pushdown
— então este valor é deliberadamente conservador enquanto essa view existir;
revisitar com medição real depois da Fase 3.

> **Sobre a contagem "4-8 nós": é estimativa estrutural, não medida.** Vem de
> contar operadores no SQL da view (FULL OUTER JOIN + GROUP BY), não de rodar
> `EXPLAIN (ANALYZE, BUFFERS)` contra dados representativos — não temos acesso
> a um Postgres com volume de prod pra medir agora (Docker não está de pé
> localmente nesta rodada; acesso a prod é vedado por CLAUDE.md §7). Trate o
> intervalo como uma faixa de segurança, não um fato medido. Antes de subir
> `PG_WORK_MEM` de novo, rode `EXPLAIN` real contra uma cópia de dados de
> prod (docs/operations/migrations.md tem o procedimento de clonar prod pra
> um Postgres descartável) e trave o node-count real.

`GOMEMLIMIT`/`API_GOMEMLIMIT` (service `api`) limita **só o heap Go** — não
tem efeito sobre o RSS dos ~200 processos ffmpeg de captura, que são o termo
dominante e variável desse container (~88MB base + ~30MB/emissora líquido ≈
6GB a 200 emissoras, ver tabela acima). Por isso `api` **não** tem `mem_limit` no
compose: um hard limit ali arriscaria matar a captura — o core do produto —
em vez de só conter o heap Go. A proteção do `api` é inteiramente soft
(`GOMEMLIMIT`, que deixa o GC mais agressivo perto do teto do heap).

---

## 2. Domínio: Hostgator → Cloudflare

O objetivo é transferir o controle do DNS do Hostgator para o Cloudflare. O domínio continua registrado no Hostgator — você só muda quem responde pelo DNS. Isso é necessário para o Cloudflare Pages e o Cloudflare Tunnel funcionarem com seu domínio.

### Passo 1 — Adicionar o domínio no Cloudflare

1. Acesse [dash.cloudflare.com](https://dash.cloudflare.com) → **Add a Site**
2. Digite seu domínio (ex: `e-monitor.online`) → **Continue**
3. Selecione o plano **Free** → **Continue**
4. O Cloudflare vai escanear os registros DNS existentes do Hostgator automaticamente. Revise a lista — geralmente importa tudo certo. Clique em **Continue**
5. Na próxima tela o Cloudflare vai te dar **2 nameservers**, algo como:
   ```
   chad.ns.cloudflare.com
   nina.ns.cloudflare.com
   ```
   Anote os dois — você vai precisar no próximo passo.

### Passo 2 — Trocar os nameservers no Hostgator

1. Acesse o painel do Hostgator → **Meus Produtos** → localize seu domínio → **Gerenciar**
2. Vá em **Servidores DNS** (ou "Nameservers")
3. Troque os nameservers atuais pelos dois que o Cloudflare forneceu:
   ```
   Nameserver 1: chad.ns.cloudflare.com
   Nameserver 2: nina.ns.cloudflare.com
   ```
   (os nomes exatos são os que o Cloudflare te deu no passo anterior)
4. Salve.

> A propagação leva de 5 minutos a 24 horas. Normalmente resolve em menos de 1 hora. Você pode checar em [dnschecker.org](https://dnschecker.org) digitando seu domínio e vendo se os nameservers já aparecem como Cloudflare.

### Passo 3 — Confirmar no Cloudflare

Depois que a propagação acontecer, o Cloudflare vai detectar automaticamente e mudar o status do domínio para **Active**. Você recebe um e-mail de confirmação.

### Passo 4 — Registros DNS que você vai precisar

Após o domínio estar ativo no Cloudflare, os registros abaixo são criados automaticamente pelos próximos passos deste guia:

| Subdomínio | Tipo | Criado por | Aponta para |
|---|---|---|---|
| `app.e-monitor.online` | CNAME | Cloudflare Pages (seção 3) | Endereço do Pages gerado automaticamente |
| `api.e-monitor.online` | CNAME | `cloudflared tunnel route dns` (seção 5, Bloco G) | Cloudflare Tunnel |

Você não precisa criar esses registros manualmente — os comandos das seções seguintes fazem isso.

---

## 3. Cloudflare Pages — Frontend

### Pré-requisitos

- Conta Cloudflare (free tier suficiente)
- Repositório no GitHub
- Domínio configurado no Cloudflare (necessário para HTTPS no backend via Tunnel)

> O frontend em Cloudflare Pages (`https://`) precisa falar com um backend em `https://`. Use Cloudflare Tunnel na VM — coberto na seção 4, Bloco G.

### Passo a passo

**1. Suba o repositório no GitHub:**

```bash
git remote add origin https://github.com/SEU_USUARIO/radiocheck.git
git push -u origin master
```

**2. No painel Cloudflare:** Workers & Pages → Create application → Pages → Connect to Git → selecione o repositório.

**3. Configurações de build:**

| Campo | Valor |
|---|---|
| Production branch | `master` |
| Build command | `npm run build` |
| Build output directory | `dist` |
| Root directory | `frontend` |
| Node.js version | `20` |

**4. Variáveis de ambiente:**

| Nome | Valor |
|---|---|
| `VITE_API_URL` | `https://api.e-monitor.online` |
| `NODE_VERSION` | `20` |

**5.** Clique em **Save and Deploy**. O primeiro build leva ~2 minutos.

**6. Domínio customizado (opcional):** Custom domains → adicione `app.e-monitor.online`. O Cloudflare cria o DNS automaticamente.

Todo `git push origin master` dispara rebuild automático.

---

## 4. Google VM — Especificações

### Região e zona

| Campo | Valor | Motivo |
|---|---|---|
| **Region** | `southamerica-east1` | São Paulo — menor latência para streams brasileiras (~15–40 ms). LGPD: dados no Brasil. |
| **Zone** | `southamerica-east1-a` | Zona onde a VM efetivamente roda. ⚠️ Até 2026-09-01 este documento dizia `-b` em dois lugares, o que estava **errado** — comandos copiados daqui falhavam com "resource not found". |

### Tipo de máquina

| Campo | Valor |
|---|---|
| **Nome da instância** | `vm-e-monitor` |
| **Família** | C2D (AMD EPYC Milan) |
| **Tipo** | `c2d-standard-16` |
| **vCPUs** | 16 (8 cores físicos + SMT) |
| **RAM** | 64 GB |
| **IP externo** | `34.39.163.110` — **reservado** (`radiocheck-prod-ip`) |

> **Histórico:** `c3-standard-4` → `c3-highcpu-8` (2026-06-08) →
> **`c2d-standard-16` (2026-09-01)**.

**Por que trocou em 2026-09-01?** Com 268 emissoras a `c3-highcpu-8` chegou a
**0% de CPU ociosa**, PSI de 76% e p99 da janela de matching em **2,39 s —
acima da própria cadência de 2 s**. Demanda medida: **7,84 cores numa máquina
de 8**. Postmortem completo:
[incident-2026-09-01](../incidents/incident-2026-09-01-cpu-saturation-vm-resize.md).

**Por que C2D e não outro C3?** Porque **a família C3 não tem shape de 16
vCPU** — os tamanhos são 4 → 8 → **22** → 44 → 88 → 176. O degrau seguinte
(`c3-highcpu-22`) custaria R$5.437/mês para capacidade de ~530 emissoras:
capacidade parada demais. O `c2d-standard-16` entrega os mesmos 8 cores
físicos por R$4.409/mês.

**Por que não C4?** O `c4-highcpu-16` seria melhor em tudo — R$200/mês mais
barato e ~34% mais rápido por core (Emerald Rapids). Mas **C4 exige
Hyperdisk** e os três discos desta VM são Persistent Disk:

```
ERROR: pd-balanced disk type cannot be used by c4-highcpu-16 machine type
```

Migrar os discos para Hyperdisk é o follow-up **F-CAP-13**. Enquanto não for
feito, C4 está fora.

**Por que não `t2d-standard-16`?** Ele tem 16 cores **físicos** (SMT desligado)
e PassMark 30.446 — seria o melhor throughput do lote. Mas **T2D não está na
lista de elegíveis do Compute Flexible CUD**, então migrar para lá perderia os
28% de desconto *e* manteria o commitment atual, que não pode ser cancelado.

> ⚠️ **Não dimensione por `load average`.** A carga é **em rajada** — os workers
> fecham janela de matching em ondas sincronizadas de 2 s, saturam os cores por
> instantes e ficam ociosos entre elas. O load lê como "máquina cheia" quando há
> idle de sobra. Use `vmstat 1 5` (linhas 2+, a primeira é média desde o boot) e
> principalmente **`cat /proc/pressure/cpu`**, que é a única métrica que não
> clipa no teto. Ver [capacity-and-unit-cost.md §6](capacity-and-unit-cost.md).

**Por que `standard` (4 GB/vCPU) e não `highcpu` (2 GB/vCPU)?** A carga
continua **CPU-bound**, e 32 GB bastariam até o teto de capacidade
(~26 MB/emissora medidos no cgroup × ~460 emissoras + Postgres ≈ 16 GB). Os
64 GB do `standard` vieram junto no shape por R$200/mês a mais, e o benefício
concreto é poder **afrouxar o tuning apertado do Postgres** (`PG_MEM_LIMIT=6g`,
`work_mem=8MB`) que só existe porque a máquina antiga tinha 16 GB.

> ⚠️ **O custo por emissora não é constante — depende do tamanho do índice de
> matching.** Foi exatamente isso que fez a projeção de julho subestimar e a
> máquina saturar. Qualquer novo dimensionamento precisa declarar contra qual
> tamanho de índice foi medido. Ver
> [capacity-and-unit-cost.md §4](capacity-and-unit-cost.md).

### Discos

| Disco | Tipo | Tamanho | Ponto de montagem | Motivo |
|---|---|---|---|---|
| **Boot** | Balanced Persistent Disk (SSD) | 50 GB | `/` | OS + Docker images + código |
| **PostgreSQL** | SSD Persistent Disk | 300 GB | `/mnt/db` | IOPS alto para queries de fingerprint |
| **Áudio / Logs** | Standard HDD | 300 GB | `/mnt/data` | Clips de evidência + logs |

> ⚠️ **Esta tabela é o layout *planejado*, não o *provisionado*.** Os SKUs
> faturados implicam **~199 GB de Balanced PD** e **~99 GB de SSD PD**, e
> **nenhum SKU de Standard HDD** — apesar do nome do disco. Nomes reais dos
> três discos (2026-09-01), todos **Persistent Disk**:
>
> ```
> vm-e-monitor          (boot)
> radiocheck-db-ssd
> radiocheck-data-hdd   ← nome enganoso: NÃO é Standard HDD
> ```
>
> Antes de dimensionar disco, confirme com `df -h /mnt/db /mnt/data` na VM.
> Follow-up: **F-CAP-07**.
>
> **Consequência não óbvia:** por serem Persistent Disk e não Hyperdisk, as
> famílias C4/C4A/C4D/N4 estão **indisponíveis** para esta VM — o
> `set-machine-type` falha com `pd-balanced disk type cannot be used by ...`.
> Ver F-CAP-13.

Não use disco único — contenção de I/O entre PostgreSQL e OS degrada latência de detecção.

### Sistema operacional

`Ubuntu 22.04 LTS` — imagem `ubuntu-2204-lts`

### Rede

| Campo | Valor |
|---|---|
| External IP | Static (não efêmero) — `radiocheck-prod-ip` = `34.39.163.110` |
| Network tier | Premium |
| HTTP / HTTPS | Bloqueados (Cloudflare Tunnel cuida disso) |
| SSH | Porta 22, restrita ao seu IP |

### Trocar o tipo de máquina (resize)

Procedimento validado em 2026-09-01
([postmortem](../incidents/incident-2026-09-01-cpu-saturation-vm-resize.md)).
A VM precisa ser **parada** para o `set-machine-type`, então há janela de
indisponibilidade — e enquanto os workers estiverem fora, **o áudio não é
capturado nem gravado**: é buraco de detecção irrecuperável. Faça de madrugada.

> ⚠️ **Não use o `scripts/deploy.sh` para isso.** Ele faz `git pull` + rebuild +
> migrations — trocar hardware e software no mesmo passo destrói a capacidade de
> saber o que quebrou. Além disso o health check dele tem 60 s e a API só escuta
> **depois** de carregar o índice inteiro (`loader.LoadAll` precede
> `srv.ListenAndServe` em `cmd/api/main.go`); com Postgres frio pós-reboot isso
> passa de 60 s e o deploy aborta sem que nada esteja errado.

**1. Pré-flight (Cloud Shell).** Confirme que o tipo alvo existe na zona — nem
toda família tem todo shape (o C3 pula de 8 para 22 vCPU):

```bash
export VM=vm-e-monitor ZONE=southamerica-east1-a
gcloud compute machine-types describe <TIPO> --zone="$ZONE" \
  --format='value(name,guestCpus,memoryMb)'
```

**2. Confirme que o IP externo é reservado.** Se for efêmero, o stop/start
troca o IP e invalida qualquer allowlist negociada com painéis de emissora:

```bash
gcloud compute addresses list --filter="address=34.39.163.110"
```

Vazio = efêmero. Promova antes de continuar:
`gcloud compute addresses create radiocheck-prod-ip --addresses=34.39.163.110 --region=southamerica-east1`

**3. Backup verificado (na VM).** Regra 4.5 do CLAUDE.md — confirme que o
arquivo chegou ao destino, não só que o comando rodou:

```bash
$COMPOSE exec backup sh /backup.sh
```

**4. Snapshot dos três discos (Cloud Shell).** O `--storage-location` evita
aumentar o problema do F-CAP-06 (snapshots vivendo nos EUA):

```bash
for d in vm-e-monitor radiocheck-db-ssd radiocheck-data-hdd; do
  gcloud compute disks snapshot "$d" --zone="$ZONE" \
    --snapshot-names="pre-resize-$d-$(date +%Y%m%d)" \
    --storage-location=southamerica-east1
done
```

**5. Pare a stack graciosamente (na VM).** `stop`, não `down` — deixa o
Postgres desligar limpo em vez de tomar SIGKILL no shutdown da VM:

```bash
$COMPOSE stop
```

**6. Troque (Cloud Shell).**

```bash
gcloud compute instances stop "$VM" --zone="$ZONE"
gcloud compute instances set-machine-type "$VM" --zone="$ZONE" --machine-type=<TIPO>
gcloud compute instances start "$VM" --zone="$ZONE"

gcloud compute instances describe "$VM" --zone="$ZONE" \
  --format='value(machineType.basename(),status,networkInterfaces[0].accessConfigs[0].natIP)'
```

A última linha confirma tipo novo, `RUNNING` e que o IP não mudou.

**7. Suba a stack — este passo NÃO é automático.**

> 🔴 **Dos 25 serviços do compose, só o `segments-cleanup` tem
> `restart: unless-stopped`.** `postgres`, `api`, `minio`, `redis`, `nats`,
> `prometheus` e `backup` estão todos no default `no`. Depois do
> `instances start`, o Docker sobe e **nada mais**. Follow-up: F-CAP-12.

```bash
$COMPOSE up -d
$COMPOSE logs -f api | grep -m1 'index loaded'
```

O `grep -m1` fica pendurado até o índice terminar de carregar e sai sozinho.
Demora ali é esperada (Postgres frio + índice inteiro), **não é falha**.

**8. Valide.**

```bash
nproc; free -m; $COMPOSE ps
cat /proc/pressure/cpu     # some deve estar < 10%
vmstat 1 5                 # r em 1 dígito, id bem acima de 0
docker stats --no-stream
```

E, após ~10 min acumulando dados novos, o critério de aceitação real:

```bash
curl -sG 'http://localhost:9090/api/v1/query' --data-urlencode \
 'query=histogram_quantile(0.99, sum by (le) (rate(radiocheck_match_window_duration_seconds_bucket[10m])))'
```

**O p99 tem que ficar em dezenas de milissegundos.** A cadência de janela é 2 s;
p99 próximo disso significa que o teto de capacidade já foi ultrapassado.

### Criando a VM no console GCP

```
Menu → Compute Engine → VM Instances → Create Instance

Name: vm-e-monitor
Region: southamerica-east1
Zone: southamerica-east1-a

Machine configuration:
  Series: C2D
  Machine type: c2d-standard-16

Boot disk:
  OS: Ubuntu 22.04 LTS
  Type: Balanced persistent disk
  Size: 50 GB

Firewall:
  ☐ Allow HTTP   (não marcar)
  ☐ Allow HTTPS  (não marcar)

Advanced → Disks → Add new disk:
  Disco 1 (PostgreSQL):
    Name: radiocheck-db-ssd
    Type: SSD persistent disk
    Size: 300 GB
    Deletion rule: Keep disk

  Disco 2 (Dados):
    Name: radiocheck-data-hdd
    Type: Standard persistent disk
    Size: 300 GB
    Deletion rule: Keep disk

Advanced → Networking:
  External IP: (IP estático reservado previamente)
  Network Service Tier: Premium

→ CREATE
```

**Reservar IP estático antes de criar a VM:**

```
Menu → VPC network → IP addresses → Reserve External Static Address
Name: radiocheck-prod-ip
Region: southamerica-east1
```

**Regra de firewall para SSH:**

```
Menu → VPC network → Firewall → Create Firewall Rule
Name: allow-ssh-myip
Direction: Ingress
Source IPv4 ranges: SEU_IP/32
Protocols: TCP:22
```

---

## 5. Google VM — Configuração do zero

Conecte via SSH:

```bash
gcloud compute ssh vm-e-monitor --zone southamerica-east1-a
```

---

### Bloco A — Atualização e usuário de serviço

```bash
sudo apt update && sudo apt upgrade -y

sudo apt install -y \
  curl wget git unzip jq htop iotop \
  ca-certificates gnupg lsb-release \
  apt-transport-https software-properties-common \
  ufw fail2ban

# Usuário de serviço
sudo useradd -m -s /bin/bash radiocheck
sudo usermod -aG sudo radiocheck

# Copiar chave SSH
sudo mkdir -p /home/radiocheck/.ssh
sudo cp ~/.ssh/authorized_keys /home/radiocheck/.ssh/
sudo chown -R radiocheck:radiocheck /home/radiocheck/.ssh
sudo chmod 700 /home/radiocheck/.ssh
sudo chmod 600 /home/radiocheck/.ssh/authorized_keys
```

---

### Bloco B — Montar os discos adicionais

```bash
# Ver discos disponíveis (sdb = SSD PostgreSQL, sdc = HDD dados)
lsblk

sudo mkfs.ext4 -L pgdata /dev/sdb
sudo mkfs.ext4 -L rcdata /dev/sdc

sudo mkdir -p /mnt/db /mnt/data
sudo mount /dev/sdb /mnt/db
sudo mount /dev/sdc /mnt/data

# Montagem automática no boot
echo "LABEL=pgdata  /mnt/db    ext4  defaults,nofail  0  2" | sudo tee -a /etc/fstab
echo "LABEL=rcdata  /mnt/data  ext4  defaults,nofail  0  2" | sudo tee -a /etc/fstab

sudo mount -a
df -h | grep mnt  # verificar

# Estrutura de diretórios
sudo mkdir -p /mnt/db/pgdata /mnt/db/pg_archive /mnt/db/pgbackup
sudo mkdir -p /mnt/data/minio /mnt/data/logs /mnt/data/audio-refs /mnt/data/masters

sudo chown -R radiocheck:radiocheck /mnt/db /mnt/data
```

---

### Bloco C — Docker e Docker Compose

```bash
sudo apt remove -y docker docker-engine docker.io containerd runc 2>/dev/null || true

sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | \
  sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
sudo chmod a+r /etc/apt/keyrings/docker.gpg

echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
  https://download.docker.com/linux/ubuntu \
  $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | \
  sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

sudo apt update
sudo apt install -y \
  docker-ce docker-ce-cli containerd.io \
  docker-buildx-plugin docker-compose-plugin

sudo systemctl enable docker
sudo systemctl start docker

sudo usermod -aG docker radiocheck
sudo usermod -aG docker $USER
newgrp docker

docker --version
docker compose version
```

---

### Bloco D — ffmpeg

```bash
sudo apt install -y ffmpeg

ffmpeg -version
ffmpeg -codecs 2>/dev/null | grep -E "aac|mp3|opus"
```

---

### Bloco E — Go

```bash
GO_VERSION="1.22.3"
wget "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -O /tmp/go.tar.gz
sudo tar -C /usr/local -xzf /tmp/go.tar.gz
rm /tmp/go.tar.gz

echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh
source /etc/profile.d/go.sh

go version
```

---

### Bloco F — Node.js

```bash
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.39.7/install.sh | bash
source ~/.bashrc
nvm install 20
nvm use 20
nvm alias default 20

node --version
npm --version
```

---

### Bloco G — Cloudflare Tunnel

Expõe o backend de forma segura sem abrir as portas 80/443.

```bash
curl -L --output cloudflared.deb \
  https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb
sudo dpkg -i cloudflared.deb
rm cloudflared.deb

# Autenticar (abre link no browser)
cloudflared tunnel login

# Criar tunnel
cloudflared tunnel create radiocheck-prod
# Anote o Tunnel ID gerado (UUID)

# Criar rota DNS
cloudflared tunnel route dns radiocheck-prod api.e-monitor.online

# Configuração do tunnel
sudo mkdir -p /etc/cloudflared
sudo tee /etc/cloudflared/config.yml > /dev/null <<'EOF'
tunnel: SEU_TUNNEL_ID_AQUI
credentials-file: /root/.cloudflared/SEU_TUNNEL_ID_AQUI.json

ingress:
  - hostname: api.e-monitor.online
    service: http://localhost:8080
  - service: http_status:404
EOF

sudo cloudflared service install
sudo systemctl enable cloudflared
sudo systemctl start cloudflared

sudo systemctl status cloudflared
```

---

### Bloco H — Clonar o repositório

```bash
sudo su - radiocheck

# Gerar chave SSH para GitHub (repo privado)
ssh-keygen -t ed25519 -C "radiocheck-prod" -f ~/.ssh/id_ed25519 -N ""
cat ~/.ssh/id_ed25519.pub
# Adicionar em: GitHub → Settings → SSH Keys → New SSH key

git clone git@github.com:Dereckkk1/Radiocheck.git /home/radiocheck/radiocheck
cd /home/radiocheck/radiocheck
```

---

### Bloco I — Variáveis de ambiente

```bash
cd /home/radiocheck/radiocheck
cp infra/docker/.env.example infra/docker/.env
nano infra/docker/.env
```

Preencher cada variável:

```env
# PostgreSQL
POSTGRES_DB=radiocheck
POSTGRES_USER=radiocheck
POSTGRES_PASSWORD=     # openssl rand -base64 32
POSTGRES_PORT=5432

# Redis
REDIS_PORT=6379

# NATS
NATS_PORT=4222

# MinIO
MINIO_ROOT_USER=radiocheck-minio
MINIO_ROOT_PASSWORD=   # openssl rand -base64 24
MINIO_PORT=9000
MINIO_CONSOLE_PORT=9001
S3_BUCKET=radiocheck-evidences
S3_ENDPOINT=http://minio:9000
S3_PUBLIC_ENDPOINT=http://localhost:9000
S3_REGION=us-east-1

# API
API_PORT=8080
JWT_SECRET=            # openssl rand -base64 48

# Admin inicial
RADIOCHECK_BOOTSTRAP_ADMIN_EMAIL=admin@suaempresa.com.br
RADIOCHECK_BOOTSTRAP_ADMIN_PASSWORD=

# Tracing
OTEL_EXPORTER_OTLP_ENDPOINT=jaeger:4317
OTEL_SERVICE_NAME=radiocheck-api
OTEL_TRACES_SAMPLER=parentbased_traceidratio
OTEL_TRACES_SAMPLER_ARG=0.1
RADIOCHECK_ENV=production

# Alertas
SLACK_WEBHOOK_URL=https://hooks.slack.com/services/SEU_WEBHOOK

# Backup (Cloudflare R2)
R2_ENDPOINT=https://SEU_ACCOUNT_ID.r2.cloudflarestorage.com
R2_BUCKET=radiocheck-backups
R2_ACCESS_KEY=
R2_SECRET_KEY=
```

Gerar senhas:

```bash
openssl rand -base64 32   # PostgreSQL password
openssl rand -base64 48   # JWT secret
openssl rand -base64 24   # MinIO password
```

---

### Bloco J — Redirecionar volumes para os discos externos

Criar `infra/docker/docker-compose.override.yml`:

```yaml
services:
  postgres:
    volumes:
      - /mnt/db/pgdata:/var/lib/postgresql/data
      - /mnt/db/pg_archive:/var/lib/postgresql/archive
      - ./migrations:/migrations

  minio:
    volumes:
      - /mnt/data/minio:/data

  api:
    volumes:
      - /mnt/data/masters:/app/masters
      - /mnt/data/audio-refs:/app/audio-refs
```

---

### Bloco K — Primeiro boot

```bash
cd /home/radiocheck/radiocheck

# Build (primeira vez — ~10 min)
docker compose -f infra/docker/docker-compose.yml \
               -f infra/docker/docker-compose.override.yml \
               build --no-cache

# Subir tudo
docker compose -f infra/docker/docker-compose.yml \
               -f infra/docker/docker-compose.override.yml \
               up -d

# Acompanhar migrations
docker compose logs -f migrate

# Acompanhar API
docker compose logs -f api
# Aguardar: "API listening on :8080"

# Verificar todos os containers
docker compose ps

# Testar
curl http://localhost:8080/health
```

---

### Bloco L — Backup automático

```bash
sudo apt install -y awscli

aws configure --profile r2
# Access Key ID: SEU_R2_ACCESS_KEY
# Secret Access Key: SEU_R2_SECRET_KEY
# Default region: auto
# Output format: json

sudo cp infra/cron/postgres-backup.cron /etc/cron.d/radiocheck-postgres
sudo chmod 644 /etc/cron.d/radiocheck-postgres

sudo mkdir -p /var/log/radiocheck
sudo touch /var/log/radiocheck/backup.log
sudo chown radiocheck:radiocheck /var/log/radiocheck/backup.log
```

---

### Bloco M — Firewall (UFW)

```bash
sudo ufw default deny incoming
sudo ufw default allow outgoing

# SSH apenas do seu IP
sudo ufw allow from SEU_IP/32 to any port 22

# NÃO abrir 8080, 3001, 9090 — Cloudflare Tunnel cuida do tráfego externo

sudo ufw enable
sudo ufw status verbose
```

---

### Bloco N — Auto-restart no boot

```bash
sudo tee /etc/systemd/system/radiocheck.service > /dev/null <<'EOF'
[Unit]
Description=Radiocheck Monitoring System
Requires=docker.service
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/home/radiocheck/radiocheck
ExecStart=/usr/bin/docker compose \
  -f infra/docker/docker-compose.yml \
  -f infra/docker/docker-compose.override.yml \
  up -d
ExecStop=/usr/bin/docker compose \
  -f infra/docker/docker-compose.yml \
  -f infra/docker/docker-compose.override.yml \
  down
User=radiocheck
Group=radiocheck

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable radiocheck
sudo systemctl start radiocheck
```

---

### Bloco O — Importar estações

```bash
cd /home/radiocheck/radiocheck
npm install --prefix scripts
node scripts/import_stations.mjs
```

---

### Bloco P — Verificação final

```bash
# Containers rodando
docker compose ps

# API local
curl http://localhost:8080/health

# API via Tunnel (no browser ou curl)
curl https://api.e-monitor.online/health

# Migrations aplicadas (deve mostrar 15 linhas — 0001..0015)
docker compose exec postgres psql -U radiocheck -d radiocheck \
  -c "SELECT version, applied_at FROM schema_migrations ORDER BY version;"

# Backfill shared-hash detection (rodar uma vez, depois de 0015 entrar):
# ver docs/shared-hash-detection.md
docker compose exec api backfill-shared-hashes --dsn "$DATABASE_URL"

# Bucket MinIO criado
docker compose exec minio mc ls local/

# Logs limpos
docker compose logs --tail=50 api | grep -E "ERROR|FATAL|panic"

# Grafana (via SSH tunnel — não expor direto)
ssh -L 3001:localhost:3001 radiocheck@IP_DA_VM
# Abrir http://localhost:3001 no browser local
```

---

## 6. Análise de custos

### Base de cálculo

Sizing baseado em medição de produção (2026-07-21, 173 emissoras ativas):
**~30 MB de RAM líquida** e **~0,022 vCPU por emissora** no container `api`.

> A base anterior (14.8 MB/emissora, stress test de 50 workers em 08/05/2026)
> **subestimava a RAM em ~2×**. Corrigida em 2026-07-21.

**Custo por emissora e teto de capacidade:** ver
[capacity-and-unit-cost.md](capacity-and-unit-cost.md) — a conta de unit
economics (custo médio × marginal × no teto) vive lá, não aqui.

### Composição do custo mensal — REAL, por SKU (2026-07-21)

Quebra por SKU do billing GCP, período de **19,84 dias** (MTD julho/2026).

> **Como o período foi determinado** (o relatório do console é parcial, não um
> mês cheio — normalizar antes de comparar): `External IP Charge` = 476,18 h;
> `C3 Instance Core` 3.809,43 ÷ 8 vCPU = 476,18 h; `C3 Instance RAM` 7.602,87 ÷
> 476,18 = 15,97 GiB ≈ 16 GB. As três baterem confirma o período **e** o tipo
> de máquina. 476,18 h = 19,84 dias → fator de normalização mensal **×1,534**.

| SKU | Uso (19,84d) | R$ período | **R$/mês** | % |
|---|---|---|---|---|
| C3 Instance Core in Sao Paulo | 3.809,43 core-h | 1.233,78 | **1.892,60** | 64,9% |
| C3 Instance RAM in Sao Paulo | 7.602,87 GiB-h | 279,85 | **429,30** | 14,7% |
| **→ Compute** | | **1.513,63** | **2.321,90** | **79,6%** |
| Balanced PD Capacity | 125,25 GiB-mês (~192 GB) | 110,59 | 169,60 | 5,8% |
| SSD backed PD Capacity | 61,25 GiB-mês (~94 GB) | 91,93 | 141,00 | 4,8% |
| **→ Discos** | | **202,52** | **310,60** | **10,6%** |
| Egress SP → South America | 67,72 GiB | 75,74 | 116,20 | 4,0% |
| Egress SP → Americas | 34,69 GiB | 38,79 | 59,50 | 2,0% |
| Egress via Carrier Peering | 33,86 GiB | 15,94 | 24,40 | 0,8% |
| **→ Egress (~209 GB/mês)** | | **130,47** | **200,10** | **6,9%** |
| PD snapshot transfer NA ↔ LatAm | 48,34 GiB | 39,84 | 61,10 | 2,1% |
| Storage PD Snapshot **in US** | 30,33 GiB-mês | 11,60 | 17,80 | 0,6% |
| Storage Machine Image **in US** | 4,84 GiB-mês | 1,42 | 2,20 | 0,1% |
| **→ Snapshots/imagens (cross-continente)** | | **52,86** | **81,10** | **2,8%** |
| External IP | 476,18 h | 3,07 | 4,70 | 0,2% |
| **TOTAL** | | **1.902,55** | **~2.918** | |

**R$95,90/dia.** Nenhuma linha tem "Programas de economia" — **tudo on-demand**.

#### O que a quebra real desmentiu

A estimativa de projeto (2026-05) dizia VM ~71% / discos 24% / resto 3%. O real
é **compute 79,6% / discos 10,6% / egress 6,9% / snapshots 2,8%**. Três erros:

1. **Compute custa mais que o previsto** — R$2.322/mês ≈ US$407, contra os
   ~US$305 estimados (+33%).
2. **Discos custam menos e são outros** — o previsto era 50 GB Balanced + 300 GB
   SSD + 300 GB HDD; o faturado implica **~192 GB Balanced + ~94 GB SSD** e
   **nenhum SKU de Standard HDD**. A tabela de discos da §4 está errada — ver
   aviso lá. (Os ↑122%/↑88% no billing indicam crescimento recente.)
3. **Egress é 4× o previsto** — estimado ~50 GB/mês (US$9,50); real ~209 GB/mês
   (R$200 ≈ US$35).

#### Alavancas, em ordem de tamanho

| # | Ação | Economia | Status |
|---|---|---|---|
| 1 | **Assinar CUD de 1 ano** no compute (~37%) | **~R$860/mês (~R$10,3k/ano)** | ⚠️ **vencido** — VM em prod desde 2026-06-08, doc mandava assinar após 30d |
| 2 | Mover snapshots/imagem de máquina dos **EUA** para SP | ~R$81/mês | Config provavelmente acidental (default multi-region) |
| 3 | Investigar egress 4× acima do previsto | até ~R$150/mês | Ver F-CAP-06 |

Com o CUD assinado, o custo por emissora no teto de capacidade cai de
**R$12,42 para ~R$8,76** — ver [capacity-and-unit-cost.md](capacity-and-unit-cost.md).

#### Estimativa original de projeto (2026-05) — mantida para histórico

> Superada pela quebra real acima. Preservada só para rastrear o quanto a
> estimativa errou.

| Componente | Tipo | Custo estimado |
|---|---|---|
| VM c3-highcpu-8 (8 vCPU, 16 GB) | Compute | ~$305 |
| Boot disk: 50 GB Balanced PD | Storage | $7,50 |
| PostgreSQL: 300 GB SSD PD | Storage | $76,50 |
| Dados/áudio: 300 GB Standard HDD | Storage | $18,00 |
| GCS Nearline (backups ~50 GB) | Storage | $1,00 |
| IP estático externo | Networking | $3,65 |
| Egress estimado (~50 GB/mês) | Networking | $9,50 |
| **TOTAL estimado** | | **~$429/mês (~R$ 2.445)** |

### Cenários

> ⚠️ **Toda esta seção §6 é histórica (2026-07-21, `c3-highcpu-8`, 173
> emissoras, sem CUD).** A máquina foi trocada em 2026-09-01 e o CUD foi
> assinado em ~19/08. Para os números que valem hoje, use
> [capacity-and-unit-cost.md §7](capacity-and-unit-cost.md). O que segue fica
> como registro de como a estimativa de projeto errou — vale ler antes de
> confiar em qualquer projeção nova.

> ✅ **CUD assinado em ~2026-08-19 — F-CAP-05 resolvido.** Mas **não como este
> documento previa**: o desconto real é de **28,00%** (Compute Flexible CUD de
> 1 ano), **não os 37% do resource-based** que as tabelas abaixo assumem.
> Confirmado aritmeticamente no extrato de agosto (`R$530,10 ÷ R$1.893,23 =
> 28,00%` no core, `R$120,49 ÷ R$430,33 = 28,00%` na RAM). **C3 e C2D em São
> Paulo não são elegíveis ao resource-based** — só ao flexível, que é portátil
> entre famílias mas desconta menos. Toda estimativa de economia neste
> documento que use 37%/55% está errada por construção.

| Cenário | Configuração | $/mês | R$/mês |
|---|---|---|---|
| **Melhor caso** | n2d-std-8, CUD 3 anos | **$276** | ~R$1.573 |
| **CUD 1 ano** (recomendado) | n2d-std-8, CUD 1 ano | **$343** | ~R$1.955 |
| **Sem compromisso** | n2d-std-8, on-demand | **$429** | ~R$2.445 |
| **Com folga (upgrade RAM)** | n2d-std-16 (64 GB), on-demand | **$722** | ~R$4.114 |

*Câmbio de referência: R$ 5,70/USD*

### Por que custa isso (percentuais REAIS, 2026-07-21)

**Compute — R$2.322/mês = 79,6%**  
São Paulo paga ~37% de premium sobre regiões dos EUA. A Virginia sairia mais
barato, mas a latência de 120–180 ms para streams brasileiras é pior que os
15–40 ms de SP. **Este é o custo do sistema** — quase 80% da conta. Toda
otimização relevante passa por aqui: CUD (−37%) ou encher a máquina até o teto
de ~235 emissoras. O split core/RAM (64,9% / 14,7%) confirma que a carga é
CPU-bound, coerente com o `highcpu` escolhido.

**Discos — R$311/mês = 10,6%**  
SSD para o PostgreSQL é inegociável (o matching faz leitura aleatória no índice
em tempo real). Bem abaixo dos 24% estimados no projeto — **não é alavanca de
custo**. Mas o provisionado não bate com o documentado: ver aviso na §4.

**Egress — R$200/mês = 6,9% (~209 GB/mês)**  
4× a estimativa original. O *ingest* dos streams não aparece aqui porque
ingress no GCP é tarifado a zero (960 GB/dia entrando, R$0). Este egress é
saída: evidência servida ao frontend, presigned URLs e upload de backup pro R2.
Escala com uso de cliente e detecções, não com nº de emissoras.

**Snapshots e imagem de máquina nos EUA — R$81/mês = 2,8%**  
Anomalia: snapshots de uma VM de São Paulo estão armazenados na **América do
Norte**, pagando transferência intercontinental (`PD snapshot Data Transfer
between North America and Latin America`) todo dia. Quase certamente default
multi-region não revisado. Ver F-CAP-06.

### Estratégia de compromisso

**Estado atual (2026-09-01): Compute Flexible CUD de 1 ano, ativo desde
~19/08, aplicando 28,00%.**

O que a recomendação original deste documento errou, e que vale registrar:

| | previsto aqui | real |
|---|---|---|
| Produto | resource-based CUD | **Compute Flexible CUD** |
| Desconto 1 ano | ~37% | **28,00%** |
| Desconto 3 anos | ~55% | 46% |
| Trava o tipo de máquina? | sim | **não** — é portátil entre famílias e regiões |

Essa última linha é a boa notícia: o desconto **acompanhou sozinho** a migração
`c3-highcpu-8` → `c2d-standard-16` em 01/09, sem nenhuma ação. A ressalva é que
**T2D não está na lista de elegíveis** (`C3, C3D, C4, C4A, C4D, E2, N1, N2,
N2D, N4, N4D, N4A` + `H3, H4D, C2, C2D`) — migrar para lá perderia o desconto.

### Pendente: ampliar o commitment

Flex CUD é compromisso de **gasto por hora**, não de máquina. O commitment
atual absorve ~R$3,14/h, que era a máquina antiga inteira; a nova consome
~R$6,71/h. **Até ser ampliado, metade da máquina roda a preço cheio —
~R$650/mês.**

Regras para ampliar (é irreversível por 12 meses):

1. **Espere 5–7 dias de regime** na máquina nova antes de comprar. Os
   follow-ups de CPU (F-CAP-08/09/10) podem reduzir a necessidade em até 43%, e
   um commitment comprado grande demais fica ocioso por um ano.
2. **Comprometa o piso do gasto horário medido**, nunca o pico — flex CUD não
   reembolsa folga.
3. **Compre pelo Console** (Faturamento → Descontos por uso contínuo →
   Comprar). Não há forma `gcloud` confirmada para a compra *spend-based*: o
   `gcloud compute commitments create --resources vcpu=...` da documentação é o
   **resource-based**, que é outro produto.
4. **Não pode ser cancelado nem redimensionado** — para aumentar cobertura,
   compra-se um commitment adicional.

---

## 7. Rotina de deploy

### Pré-requisitos

- Acesso SSH à VM funcionando
- Cloudflare Pages conectado ao repositório GitHub (rebuild automático no push)

### Conectar na VM

```bash
# Via Cloud Shell do GCP (browser) — funciona de qualquer IP
# Acesse: console.cloud.google.com → ícone >_ no topo

gcloud compute ssh vm-e-monitor --zone southamerica-east1-a
```

> Se o SSH travar por firewall (UFW bloqueando), rode no Cloud Shell:
> ```bash
> gcloud compute instances add-metadata vm-e-monitor \
>   --zone southamerica-east1-a \
>   --metadata startup-script="ufw allow from 35.235.240.0/20 to any port 22"
> gcloud compute instances reset vm-e-monitor --zone southamerica-east1-a
> ```
> Aguarde 1 minuto e tente novamente.

### Deploy do backend

```bash
# 1. Entrar como usuário de serviço
sudo su - radiocheck

# 2. Rodar o script de deploy
cd radiocheck
./scripts/deploy.sh
```

O script faz automaticamente:
1. `git pull` — puxa o código novo
2. `docker compose build` — rebuilda as imagens alteradas
3. `docker compose up -d` — sobe os containers
4. Aguarda migrations completarem
5. Health check na API
6. Mostra status final dos containers

### Deploy do frontend

O frontend é deployado automaticamente pelo Cloudflare Pages a cada `git push origin master`. Não requer acesso à VM.

Para forçar um redeploy sem mudança de código: Cloudflare Pages → seu projeto → **Deployments** → **Retry deployment**.

### Verificar após o deploy

```bash
# Saúde da API
curl https://api.e-monitor.online/v1/internal/health
# Esperado: {"deps":{"nats":"ok","postgres":"ok"},"status":"ok"}

# Ver logs em tempo real
docker compose -f infra/docker/docker-compose.yml \
               -f infra/docker/docker-compose.override.yml \
               --env-file infra/docker/.env \
               logs -f api
```

### Rollback

```bash
# Ver commits recentes
git log --oneline -10

# Voltar para um commit específico
git checkout HASH_DO_COMMIT

# Rebuildar
./scripts/deploy.sh
```
