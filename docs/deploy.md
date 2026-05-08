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

---

## 1. Análise do sistema

### Serviços e consumo de recursos

| Serviço | Container | RAM (medida) | Escala com emissoras? |
|---|---|---|---|
| API Go (supervisor + workers) | `api` | ~88 MB base + **14.8 MB/emissora** | Sim |
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

### Projeção para 200 emissoras

Números baseados em stress test real (50 workers ativos medidos em 08/05/2026):

| Componente | RAM estimada |
|---|---|
| API (88 MB base + 200 × 14.8 MB) | ~3.0 GB |
| Stack fixo (todos os outros containers) | ~400 MB |
| Fingerprint index em memória (100 comerciais) | ~500 MB–1 GB |
| **Total** | **~4–5 GB** |

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
| **Família** | N2D (AMD EPYC) |
| **Tipo** | `n2d-standard-8` |
| **vCPUs** | 8 |
| **RAM** | 32 GB |

**Por que 8 vCPU e não 4?** Stress test com 50 workers mostrou ~140% CPU no ambiente de dev. Em produção com 200 FFmpeg simultâneos + matching em Go, 4 cores aperiam. 8 cores dão margem segura.

**Por que 32 GB e não 16 GB?** 200 emissoras precisam de ~4–5 GB. 32 GB dá folga para a biblioteca de comerciais crescer (fingerprint index) e para PostgreSQL usar shared_buffers adequado.

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
  Series: N2D
  Machine type: n2d-standard-8

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

# Migrations aplicadas (deve mostrar 14 linhas)
docker compose exec postgres psql -U radiocheck -d radiocheck \
  -c "SELECT version, applied_at FROM schema_migrations ORDER BY version;"

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

Sizing baseado em medição real: stress test com 50 workers ativos em 08/05/2026 mostrou **14.8 MB de RAM por emissora adicional** no container `api`. O chute inicial de 80 MB/emissora era ~5× exagerado.

### Composição do custo mensal (southamerica-east1)

| Componente | Tipo | Custo (on-demand) |
|---|---|---|
| VM n2d-standard-8 (8 vCPU, 32 GB) | Compute | $313,39 |
| Boot disk: 50 GB Balanced PD | Storage | $7,50 |
| PostgreSQL: 300 GB SSD PD | Storage | $76,50 |
| Dados/áudio: 300 GB Standard HDD | Storage | $18,00 |
| GCS Nearline (backups ~50 GB) | Storage | $1,00 |
| IP estático externo | Networking | $3,65 |
| Egress estimado (~50 GB/mês) | Networking | $9,50 |
| **TOTAL** | | **~$429/mês (~R$ 2.445)** |

### Cenários

| Cenário | Configuração | $/mês | R$/mês |
|---|---|---|---|
| **Melhor caso** | n2d-std-8, CUD 3 anos | **$276** | ~R$1.573 |
| **CUD 1 ano** (recomendado) | n2d-std-8, CUD 1 ano | **$343** | ~R$1.955 |
| **Sem compromisso** | n2d-std-8, on-demand | **$429** | ~R$2.445 |
| **Com folga (upgrade RAM)** | n2d-std-16 (64 GB), on-demand | **$722** | ~R$4.114 |

*Câmbio de referência: R$ 5,70/USD*

### Por que custa isso

**A VM ($313/mês = 73% do custo total)**  
São Paulo paga ~37% de premium sobre regiões dos EUA. Motivo: infraestrutura mais cara no Brasil, mercado menor, conectividade internacional. A Virginia custaria ~$247/mês pela mesma máquina — mas a latência de 120–180 ms para streams brasileiras é pior do que os 15–40 ms de São Paulo.

**Os discos ($102/mês = 24% do custo)**  
SSD para PostgreSQL é inegociável — o matching faz leituras aleatórias no índice de fingerprints em tempo real. HDD para áudio é suficiente (leitura sequencial de clips, baixa frequência).

**Tudo mais ($15/mês = 3%)**  
GCS, egress e IP estático são ruído nessa escala.

### Estratégia de compromisso

Recomendação: suba **on-demand por 30 dias**. Monitore RAM e CPU pelo Grafana. Com dados reais de produção, assine **CUD 1 ano** — economiza ~$86/mês. Só considere 3 anos se o projeto for estratégico de longo prazo.
