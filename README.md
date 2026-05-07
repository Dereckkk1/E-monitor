# Radiocheck

Sistema de monitoramento de veiculação de comerciais em rádios AM/FM via streaming.
Detecta comerciais conhecidos por fingerprint acústico (estilo Shazam), grava clip de
evidência e expõe API + frontend para operação.

> Blueprint arquitetural: [plano_implementacao.md](plano_implementacao.md) ·
> Status atual: [docs/status-e-roadmap.md](docs/status-e-roadmap.md) ·
> Guia para Claude: [CLAUDE.md](CLAUDE.md)

---

## Pré-requisitos

| Ferramenta | Versão | Para quê |
|---|---|---|
| Docker Desktop | 24+ (com `docker compose`) | Postgres, NATS, MinIO, API Go, fingerprint Python |
| Node.js | 20+ | Frontend (Vite + React 19) |
| Git | qualquer | Clonar e rodar |

Opcional (apenas para desenvolvimento sem Docker):
- Go 1.22+ — buildar/rodar o worker Go fora do container
- Python 3.11+ — rodar o pipeline de fingerprint local

---

## Subindo tudo (caminho rápido)

Da raiz do repositório:

**Windows (PowerShell):**
```powershell
.\scripts\start.ps1
```

**Git Bash / Linux / macOS:**
```bash
./scripts/start.sh
```

O script:
1. Cria `infra/docker/.env` a partir de `.env.example` se ainda não existir.
2. Sobe o stack com `docker compose up -d --build` (Postgres, NATS, MinIO, API, fingerprint).
3. Aguarda a API ficar pronta em `http://localhost:8080/v1/internal/health`.
4. Instala dependências do frontend (`npm install`) se `node_modules/` não existir.
5. Inicia o frontend em modo dev (`npm run dev`) em foreground, em [http://localhost:3000](http://localhost:3000).

Quando você pressiona `Ctrl+C`, **só o frontend para**. Os containers continuam rodando.
Para parar tudo: `docker compose -f infra/docker/docker-compose.yml --env-file infra/docker/.env down`.

---

## Endpoints e portas (defaults do `.env.example`)

| Serviço | URL / porta | Notas |
|---|---|---|
| Frontend | http://localhost:3000 | Vite dev server, proxy `/v1` → API |
| API Go | http://localhost:8080 | `/v1/internal/health` para checar saúde |
| Postgres | localhost:5432 | user/pass/db = `radiocheck` |
| NATS | localhost:4222 | monitor em http://localhost:8222 |
| MinIO (S3) | http://localhost:9000 | console: http://localhost:9001 (`minioadmin`/`minioadmin`) |
| Redis | localhost:6379 | reservado para uso futuro |

---

## Caminho manual (sem o script)

Se preferir rodar passo a passo:

```bash
# 1. Configurar env
cp infra/docker/.env.example infra/docker/.env

# 2. Subir backend
docker compose -f infra/docker/docker-compose.yml --env-file infra/docker/.env up -d --build

# 3. Conferir saúde
curl http://localhost:8080/v1/internal/health

# 4. Frontend
cd frontend
npm install
npm run dev
```

---

## Estrutura do repositório

```
.
├── plano_implementacao.md     Blueprint arquitetural (fonte única de verdade)
├── CLAUDE.md                  Guia para a IA assistente
├── docs/                      Documentação operacional
├── workers/                   API + match engine (Go)
├── fingerprint/               Pipeline de fingerprint (Python)
├── frontend/                  UI React + Vite
├── migrations/                DDL Postgres
├── infra/docker/              docker-compose + Dockerfiles
└── scripts/                   Scripts de automação (start, simulador)
```

---

## Solução de problemas

- **"docker: command not found"** — Docker Desktop não está instalado/aberto.
- **API nunca fica `ok`** — `docker compose logs api` para ver erros. Geralmente é Postgres ainda subindo (aguardar mais 10–20 s) ou `.env` corrompido.
- **Frontend não conecta na API** — confirme que a porta `8080` está livre. O Vite faz proxy de `/v1` para `http://localhost:8080` (ver [frontend/vite.config.js](frontend/vite.config.js)).
- **Porta ocupada** — edite `infra/docker/.env` e troque `API_PORT`, `POSTGRES_PORT`, etc.
- **Reset total** — `docker compose -f infra/docker/docker-compose.yml --env-file infra/docker/.env down -v` apaga volumes (Postgres, MinIO).
