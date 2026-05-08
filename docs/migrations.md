# Migrations — runner automático

Toda vez que o `docker compose up` sobe, um service dedicado (`migrate`) aplica todas as migrations pendentes em `migrations/*.up.sql` **antes** do `api` iniciar. Não há mais necessidade de aplicação manual.

## Como funciona

- Service `migrate` (imagem oficial `migrate/migrate:v4.17.1`) está declarado em `infra/docker/docker-compose.yml`.
- Ele depende de `postgres` saudável (`condition: service_healthy`).
- Comando executado: `migrate -path=/migrations -database=postgres://... up`.
- O `api` declara `migrate: condition: service_completed_successfully` no seu `depends_on` — só inicia quando o `migrate` sair com código 0.
- Estado é rastreado pela tabela `schema_migrations(version bigint, dirty boolean)`, criada automaticamente pelo runner na primeira execução.
- O número que vai pra `version` é o inteiro contido no nome do arquivo (`0014_disambiguation.up.sql` → `version=14`). Zeros à esquerda são preservados como prefixo de ordenação no nome do arquivo, mas o valor numérico no DB é apenas `14`.

## Bootstrap em DB existente (one-shot)

O DB que estava em produção/dev antes deste runner **já tem 0001..0014 aplicadas manualmente**. Sem nenhum estado, o runner tentaria aplicá-las de novo e quebraria com `relation "..." already exists`. Para resolver, popule `schema_migrations` com a versão atual antes do primeiro `up` do compose:

```bash
# (uma única vez, no host)
cd /caminho/do/repo
docker compose -f infra/docker/docker-compose.yml up -d postgres   # garante DB up
bash scripts/bootstrap-migrations.sh
```

O script é **idempotente** (`INSERT ... ON CONFLICT DO NOTHING`) — rodar de novo não causa dano. Ele:

1. Lê `POSTGRES_USER` e `POSTGRES_DB` de `infra/docker/.env`.
2. Cria `schema_migrations` se não existe.
3. Insere `(14, false)` (ou o valor de `BOOTSTRAP_VERSION` se setada).
4. Imprime o conteúdo final da tabela para verificação.

Após rodar, `docker compose up -d --build` deve mostrar o service `migrate` saindo com `no change` (a partir da 14, nada novo).

Em **DBs novos** (volume `pgdata` recém-criado), **não rodar o script**. O runner aplicará 0001..0014 do zero, marcando cada versão na tabela.

## Como adicionar nova migration

1. Crie dois arquivos em `migrations/`, sequenciais:
   - `0015_<descricao_curta>.up.sql`
   - `0015_<descricao_curta>.down.sql`
2. Convenções:
   - **Numeração:** `max(versão_existente) + 1`. Sequencial, sem buracos novos. (Há um buraco histórico em `0010` herdado de merge — não preencher.)
   - **Nome:** `snake_case`, descritivo, curto.
   - **UP idempotente** sempre que possível: `CREATE TABLE IF NOT EXISTS`, `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`, `CREATE INDEX IF NOT EXISTS`.
   - **DOWN deve reverter**, mas em produção evitamos rollback automático — DOWN é guard-rail para dev.
3. Subir: `docker compose -f infra/docker/docker-compose.yml up -d --build`. O service `migrate` aplica 0015 antes do `api` reiniciar.
4. Verificar: `docker compose logs migrate` → deve mostrar `15/u <descricao>` e a tabela deve ter `version=15, dirty=false`.

## Como debugar

### Ver o que rodou
```bash
docker compose -f infra/docker/docker-compose.yml logs migrate
```
Saída típica em no-op: `no change`. Em aplicação: linhas `<N>/u <name> (XXXms)`.

### Inspecionar a tabela
```bash
docker compose -f infra/docker/docker-compose.yml exec postgres \
  psql -U radiocheck -d radiocheck -c 'SELECT * FROM schema_migrations ORDER BY version;'
```

### Migration falhou — `dirty=true`
Quando uma migration falha no meio (ex: SQL inválido, constraint violada), o runner marca `dirty=true` na versão problemática e sai com erro. Próximo `up` do compose **não avança** até resolver. Procedimento:

1. Investigar o erro: `docker compose logs migrate`.
2. Decidir se a migration parcial precisa ser revertida manualmente no DB (ou se o estado já está válido).
3. Forçar a versão correta:
   ```bash
   docker compose -f infra/docker/docker-compose.yml run --rm migrate \
     -path=/migrations \
     -database=postgres://radiocheck:radiocheck@postgres:5432/radiocheck?sslmode=disable \
     force <N>
   ```
   Onde `<N>` é a versão **última aplicada com sucesso** (anterior à que falhou) — isso limpa `dirty`.
4. Corrigir o `.up.sql` da migration problemática.
5. `docker compose up -d` — runner reaplica a versão recém-corrigida.

### Rollback manual (apenas dev)
```bash
docker compose -f infra/docker/docker-compose.yml run --rm migrate \
  -path=/migrations \
  -database=postgres://radiocheck:radiocheck@postgres:5432/radiocheck?sslmode=disable \
  down 1
```
Aplica o `*.down.sql` da última versão. Em produção, prefira ALTER manual ou nova migration corretiva.

## Notas operacionais

- **Numeração não-contígua:** o repositório tem `0001..0009` e depois `0011..0014` (sem `0010`). golang-migrate aceita gaps — só ordena pelos inteiros existentes. Não preencher buracos retroativamente; sempre incrementar a partir do maior.
- **Volume readonly:** o service `migrate` monta `migrations/` como `:ro`. Não há risco de o runner reescrever os arquivos.
- **Não toca em `docker-entrypoint-initdb.d`:** o postgres ainda monta `migrations/` em `/docker-entrypoint-initdb.d` para o caso de DB novo (volume `pgdata` recém-criado), mas isso só dispara na primeira inicialização do volume. Após esse momento, é o `migrate` que governa.
- **Multi-réplica:** se um dia rodarmos múltiplas instâncias do compose contra o mesmo DB, o golang-migrate adquire um advisory lock (`pg_advisory_lock`) automaticamente — apenas uma instância aplica por vez, as outras esperam.
