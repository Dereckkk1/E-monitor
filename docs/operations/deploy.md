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

| Var | Dev (default) | Prod (VM c3-highcpu-8, 16GB RAM) | Motivo |
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
| **Zone** | `southamerica-east1-b` | Zona mais estável historicamente em SP. |

### Tipo de máquina

| Campo | Valor |
|---|---|
| **Família** | C3 (Intel Sapphire Rapids) |
| **Tipo** | `c3-highcpu-8` |
| **vCPUs** | 8 |
| **RAM** | 16 GB |

> Máquina em produção desde 2026-06-08 (era `c3-standard-4` / 4 vCPU / 16 GB).

**Por que 8 vCPU e não 4?** Stress test com 50 workers mostrou ~140% CPU no ambiente de dev. Em produção com 200 FFmpeg simultâneos + matching em Go, 4 cores aperiam. Além disso, o fix de densidade **#2** (peak-picking ~4× mais denso — ver [migração de re-fingerprint](refingerprint-density-migration.md)) **dobrou o custo do matcher por janela**, então 8 vCPU passou a ser necessário tanto pela escala quanto pelo algoritmo. **Uso medido em 2026-07-21 com 173 emissoras: ~53% de CPU** (`us` 42-46% + `sy` 8-9%, `wa`=0) — ver [capacity-and-unit-cost.md](capacity-and-unit-cost.md).

> ⚠️ **Não dimensione por `load average` nesta máquina.** O load fica em ~6.9
> de 8 com a CPU em 53%, e o `r` do `vmstat` oscila entre 1 e 23. A carga é
> **em rajada** (os workers fecham janela de matching em ondas sincronizadas):
> satura os 8 cores por instantes e fica ociosa entre elas. O load lê como
> "máquina cheia" quando há 47% de idle. Use `vmstat 1 5` (linhas 2+, a
> primeira é média desde o boot), não `uptime`.

**Por que `highcpu` (16 GB) e não `standard` (32 GB)?** A carga é **CPU-bound** (matching + 200 FFmpeg); o índice de fingerprint é **leve** — ~10 MB para ~300 comerciais, ~40 MB mesmo com a densidade #2. **Ao escalar para 200 emissoras:** com a correção de RAM de 2026-07-21 (~30 MB/emissora, não 14.8), os ~200 FFmpeg + PostgreSQL projetam **~9–12 GB dos 16 GB** — folga real, mas bem menor que o previsto antes. Se passar de ~200 emissoras, migrar para `c3-standard-8` (32 GB).

### Discos

| Disco | Tipo | Tamanho | Ponto de montagem | Motivo |
|---|---|---|---|---|
| **Boot** | Balanced Persistent Disk (SSD) | 50 GB | `/` | OS + Docker images + código |
| **PostgreSQL** | SSD Persistent Disk | 300 GB | `/mnt/db` | IOPS alto para queries de fingerprint |
| **Áudio / Logs** | Standard HDD | 300 GB | `/mnt/data` | Clips de evidência + logs |

Não use disco único — contenção de I/O entre PostgreSQL e OS degrada latência de detecção.

### Sistema operacional

`Ubuntu 22.04 LTS` — imagem `ubuntu-2204-lts`

### Rede

| Campo | Valor |
|---|---|
| External IP | Static (não efêmero) |
| Network tier | Premium |
| HTTP / HTTPS | Bloqueados (Cloudflare Tunnel cuida disso) |
| SSH | Porta 22, restrita ao seu IP |

### Criando a VM no console GCP

```
Menu → Compute Engine → VM Instances → Create Instance

Name: radiocheck-prod
Region: southamerica-east1
Zone: southamerica-east1-b

Machine configuration:
  Series: C3
  Machine type: c3-highcpu-8

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
gcloud compute ssh radiocheck-prod --zone southamerica-east1-b
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

### Composição do custo mensal (southamerica-east1)

> ⚠️ **A tabela abaixo é a estimativa de projeto (2026-05), não o gasto real.**
> Faturamento observado em 2026-07-21: **~R$97/dia ≈ R$2.949/mês ≈ US$517**,
> contra os **US$429** projetados — **~US$88/mês (20%) acima**, não explicado.
> Nenhuma quebra por SKU foi feita até hoje. **Ação pendente:** GCP Console →
> Billing → Reports → agrupar por SKU, e reconciliar contra esta tabela.

| Componente | Tipo | Custo (on-demand) |
|---|---|---|
| VM c3-highcpu-8 (8 vCPU, 16 GB) | Compute | ~$305 (verificar no billing) |
| Boot disk: 50 GB Balanced PD | Storage | $7,50 |
| PostgreSQL: 300 GB SSD PD | Storage | $76,50 |
| Dados/áudio: 300 GB Standard HDD | Storage | $18,00 |
| GCS Nearline (backups ~50 GB) | Storage | $1,00 |
| IP estático externo | Networking | $3,65 |
| Egress estimado (~50 GB/mês) | Networking | $9,50 |
| **TOTAL** | | **~$429/mês (~R$ 2.445)** |

### Cenários

> **Máquina em produção (desde 2026-06-08): `c3-highcpu-8` (8 vCPU, 16 GB), ~$305/mês on-demand** — mesmo patamar do `n2d-standard-8` abaixo (o C3 tem vCPU mais caro, compensado pela RAM menor). Os cenários `n2d` abaixo ficam como referência; o C3 também tem CUD de 1/3 anos com desconto análogo.

| Cenário | Configuração | $/mês | R$/mês |
|---|---|---|---|
| **Melhor caso** | n2d-std-8, CUD 3 anos | **$276** | ~R$1.573 |
| **CUD 1 ano** (recomendado) | n2d-std-8, CUD 1 ano | **$343** | ~R$1.955 |
| **Sem compromisso** | n2d-std-8, on-demand | **$429** | ~R$2.445 |
| **Com folga (upgrade RAM)** | n2d-std-16 (64 GB), on-demand | **$722** | ~R$4.114 |

*Câmbio de referência: R$ 5,70/USD*

### Por que custa isso

**A VM (~$305/mês = ~71% do custo total)**  
São Paulo paga ~37% de premium sobre regiões dos EUA. Motivo: infraestrutura mais cara no Brasil, mercado menor, conectividade internacional. A Virginia custaria ~$247/mês pela mesma máquina — mas a latência de 120–180 ms para streams brasileiras é pior do que os 15–40 ms de São Paulo.

**Os discos ($102/mês = 24% do custo)**  
SSD para PostgreSQL é inegociável — o matching faz leituras aleatórias no índice de fingerprints em tempo real. HDD para áudio é suficiente (leitura sequencial de clips, baixa frequência).

**Tudo mais ($15/mês = 3%)**  
GCS, egress e IP estático são ruído nessa escala.

### Estratégia de compromisso

Recomendação: suba **on-demand por 30 dias**. Monitore RAM e CPU pelo Grafana. Com dados reais de produção, assine **CUD 1 ano** — economiza ~$86/mês. Só considere 3 anos se o projeto for estratégico de longo prazo.

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
