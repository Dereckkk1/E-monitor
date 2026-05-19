---
status: implementado
ultima-verificacao: 2026-05-19
codigo-relacionado:
  - workers/cmd/api/main.go
---

# Profiling com pprof

A API expõe os endpoints padrão `net/http/pprof` num listener dedicado em
`127.0.0.1:6060` **dentro do container**. A porta deliberadamente **não** é
publicada no compose, e o bind é em loopback, então o endpoint é
inacessível fora do container — segurança por construção (não vai pelo
Cloudflare Tunnel, não escuta na rede docker, só dentro do próprio
processo da api).

## Por que existe

Sem pprof não dá pra responder concretamente perguntas tipo:

- "O matching está gastando mais CPU em STFT ou em peak picking?"
- "Tem leak de goroutine acumulando ao longo do dia?"
- "Quem é o maior alocador na minha heap agora?"

Habilitado em 2026-05-19 junto com o refactor do pool FFT em `pkg/audio/stft.go`,
justamente pra medir o ganho real do refactor antes/depois.

## Como acessar

> Use `127.0.0.1` explícito, não `localhost`. Containers alpine resolvem
> `localhost` pra `::1` (IPv6) e o pprof faz bind só em IPv4 — `wget` retorna
> "connection refused" se você cair no IPv6.


Todos os exemplos rodam **na VM de produção** ou no host local (com docker
compose subido). O acesso é via `docker compose exec`:

```bash
# Caminho canônico em prod (override file incluído):
DC="docker compose -f infra/docker/docker-compose.yml \
                  -f infra/docker/docker-compose.override.yml \
                  --env-file infra/docker/.env"

# Snapshot de heap (estado atual de alocações vivas):
$DC exec api wget -qO - http://127.0.0.1:6060/debug/pprof/heap > heap.pprof

# CPU profile (bloqueia o endpoint por 30s, samplear o processo):
$DC exec api wget -qO - 'http://127.0.0.1:6060/debug/pprof/profile?seconds=30' > cpu.pprof

# Goroutines (instantâneo):
$DC exec api wget -qO - http://127.0.0.1:6060/debug/pprof/goroutine > goroutine.pprof

# Allocs cumulativos (alocações totais — útil pra ver onde GC tá vindo):
$DC exec api wget -qO - http://127.0.0.1:6060/debug/pprof/allocs > allocs.pprof

# Bloqueio em sync primitivos:
$DC exec api wget -qO - http://127.0.0.1:6060/debug/pprof/block > block.pprof

# Mutex contention:
$DC exec api wget -qO - http://127.0.0.1:6060/debug/pprof/mutex > mutex.pprof
```

Depois, no host local com Go instalado:

```bash
go tool pprof -http=:8000 cpu.pprof
# abre browser em http://localhost:8000 com flamegraph, top, source view etc.
```

Sem `-http`, abre CLI interativo:

```bash
go tool pprof cpu.pprof
(pprof) top 20
(pprof) list MatchWindow
(pprof) web
```

## Comparando antes/depois de mudança

```bash
# Antes:
$DC exec api wget -qO - 'http://127.0.0.1:6060/debug/pprof/profile?seconds=60' > before.pprof

# Aplicar mudança, recreate api:
$DC up -d --force-recreate --no-deps api

# Esperar estabilizar (workers tornarem a subir, ~30s):
sleep 30

# Depois:
$DC exec api wget -qO - 'http://127.0.0.1:6060/debug/pprof/profile?seconds=60' > after.pprof

# Diff:
go tool pprof -base=before.pprof after.pprof
(pprof) top 20
```

Valores positivos = mais tempo gasto na função após a mudança; negativos = menos.

## Custo do profile ligado

- **Idle:** zero. Os endpoints só executam quando alguém faz GET.
- **Durante `/profile?seconds=N`:** o runtime ativa amostragem de CPU
  (100 Hz). Custo ~1-5% adicional no processo durante os N segundos.
- **Durante `/heap`:** sub-milissegundo. Snapshot de estado, não amostragem.

Não tem porque desabilitar em prod. Mas se precisar:

```yaml
api:
  environment:
    PPROF_ENABLED: "false"
```

## O que NÃO está habilitado

- **Block/Mutex profiling** estão registrados mas com rate 0 por default —
  ou seja, retornam payload vazio. Pra ativar, é preciso chamar
  `runtime.SetBlockProfileRate(1)` / `runtime.SetMutexProfileFraction(1)`
  em `main.go`. Deixado opt-in porque ambos têm overhead permanente
  (block sample em todo `<-chan`, mutex sample em todo `sync.Mutex`).
  Se for investigar contenção, ativar pontualmente.

## Por que não pelo router público

`api.NewRouter` monta todos os handlers de negócio (auth, detections,
campaigns etc.) e vai pra `:8080`, atrás de auth JWT e do Cloudflare
Tunnel. Expor pprof por ali implicaria:

1. Adicionar `/debug/pprof/*` ao roteador chi, que normalmente exige auth.
2. Decidir uma política de quem pode acessar (admin only?).
3. Lidar com o fato de `?seconds=N` prende a goroutine de handler por N segundos —
   ataque DoS trivial mesmo sem credenciais válidas (rate limit ajuda só
   parcialmente).

Listener separado em loopback dentro do container resolve tudo isso de
graça: ninguém de fora alcança, e `docker exec` já é uma operação
privilegiada.
