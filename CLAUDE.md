# Radiocheck — Guia para Claude

Sistema de monitoramento de veiculação de comerciais em rádios AM/FM via streaming, substituindo fornecedor externo. Veja o plano completo em [plano_implementacao.md](plano_implementacao.md).

---

## Regras Críticas

### 1. O plano é lei — mas pode ser alterado com justificativa

O arquivo `plano_implementacao.md` é a fonte única de verdade arquitetural deste projeto. Ao desenvolver qualquer coisa:

- **Consulte o plano antes de começar.** Use o índice abaixo para ir direto à seção relevante sem ler o arquivo inteiro.
- **Não fuja do escopo silenciosamente.** Se perceber que a implementação vai em direção diferente do plano, pare e avise.
- **O plano é maleável**, mas só deve ser alterado quando uma solução melhor justificar a mudança. Antes de implementar diferente do que está escrito: defenda a tese, explique o porquê vale a pena mudar, aponte qual seção deve ser atualizada, e só então implemente.

### 2. Documentação de novas funcionalidades vai em `/docs`

**Nunca documente funcionalidades novas no `plano_implementacao.md`.** Esse arquivo é blueprint arquitetural, não documentação operacional. Toda funcionalidade implementada deve ter sua documentação oficial criada em `/docs`. Se a pasta não existir, crie-a.

### 3. Não invente escopo

Os **Não Objetivos** (seção 1.3, linha 32) definem explicitamente o que está fora do sistema. Não adicione reconhecimento de música, transcrição, integração com sistemas de tráfego ou captura over-the-air — mesmo que pareça útil. Qualquer expansão de escopo precisa de aprovação explícita do usuário.

### 4. Operações destrutivas em prod — leitura obrigatória antes de mexer

Esta seção existe por causa do **incidente 2026-05-12** ([postmortem](docs/incident-2026-05-12-pgdata-loss.md)) que destruiu 100% do `pgdata` de produção. Regras:

**4.1. `--force-recreate` sem `--no-deps` é proibido em produção.**

`docker compose up -d --force-recreate <service>` propaga o recreate para as dependências declaradas em `depends_on`. Para `api`, isso inclui `postgres`. Em determinadas condições o Docker considera o volume divergente e o recria do zero — apagando o dado.

```bash
# ❌ NUNCA em prod:
docker compose -f infra/docker/docker-compose.yml up -d --force-recreate api

# ✅ CORRETO:
docker compose -f infra/docker/docker-compose.yml up -d --force-recreate --no-deps api
```

A mesma regra vale para `down`, `restart`, `up`: se está mexendo em qualquer service stateful (postgres/minio/redis), passe `--no-deps` quando aplicável.

**4.2. `docker compose exec` NÃO usa imagem nova — recreate antes.**

`docker compose build api` produz imagem nova mas **não troca o binário no container que está rodando**. `docker compose exec api ...` continua executando o binário antigo. Ordem correta de deploy:

```bash
docker compose build api                                              # 1. build da imagem
docker compose up -d --force-recreate --no-deps api                   # 2. recreate (PEGA imagem nova)
docker compose exec api backfill-shared-hashes --dsn "$DATABASE_URL"  # 3. comandos one-shot
```

Inverter a ordem (2 e 3) faz o comando rodar com binário velho silenciosamente. Foi o que aconteceu com o F-108 v2 em prod no incidente 2026-05-12 — backfill rodou antigo.

**4.3. Antes de testar comando experimental em prod: teste local primeiro.**

Você tem dev local (Windows). Use. Não rode comando inédito direto na VM.

**4.4. Volumes de dado crítico usam bind mount no host, não volume nomeado.**

Em prod, o `.env` da VM deve ter `PGDATA_HOST_PATH`, `MINIODATA_HOST_PATH` e `MASTERSDATA_HOST_PATH` apontando para paths em `/srv/radiocheck/*`. Bind mounts sobrevivem a `docker compose down -v` e `--force-recreate`. Detalhes: [docs/data-durability.md](docs/data-durability.md).

**4.5. Backup precisa ser testado end-to-end, não só configurado.**

O backup do incidente 2026-05-12 estava "configurado" há 4 dias mas **nunca tinha rodado uma vez com sucesso** — falhas silenciosas no entrypoint engoliam o erro. Antes de confiar em qualquer backup, valide que o arquivo realmente aparece no destino:

```bash
# Após qualquer mudança no backup: rode manualmente e confirme upload
docker compose exec backup sh /backup.sh
docker run --rm -e AWS_ACCESS_KEY_ID="$R2_ACCESS_KEY" \
  -e AWS_SECRET_ACCESS_KEY="$R2_SECRET_KEY" \
  amazon/aws-cli --endpoint-url "$R2_ENDPOINT" \
  s3 ls "s3://$R2_BUCKET/postgres/daily/" | tail -3
```

**4.6. NUNCA monte `migrations/` em `docker-entrypoint-initdb.d` + use `migrate` service simultaneamente.**

O incidente 2026-05-12 expôs esse conflito latente: `initdb.d` executa `.sql` em ordem alfabética (incluindo `.down.sql` ANTES de `.up.sql`), deixando o schema em estado fragmentado que o golang-migrate marca como `dirty` e se recusa a continuar. Resultado: `users` table não criada → api crash loop. Recovery exige `DROP SCHEMA public CASCADE` + re-run do migrate. Limpeza definitiva em F-111 (pendente — remover o mount do `initdb.d`).

**4.7. Prod usa `docker-compose.override.yml` (gitignored) — sempre incluir em comandos manuais.**

A VM tem `infra/docker/docker-compose.override.yml` com bind mounts para `/mnt/db/pgdata`, `/mnt/data/minio`, `/mnt/data/masters` e `/mnt/data/audio-refs`. O `scripts/deploy.sh` em prod sempre inclui esse arquivo. **Comandos manuais sem o override usam configuração diferente da rodando — silenciosamente.** Isso causou ~4h de caos no incidente 2026-05-12: rodamos diagnóstico apontado pro `pgdata` named volume vazio enquanto o dado real estava intacto em `/mnt/db/pgdata`. Detalhes em [incident-2026-05-12-pgdata-loss.md §Causa raiz #5](docs/incident-2026-05-12-pgdata-loss.md).

```bash
# ❌ CONFIGURAÇÃO DIFERENTE da prod (silenciosamente):
docker compose -f infra/docker/docker-compose.yml ps

# ✅ Espelha o que o deploy.sh usa:
docker compose -f infra/docker/docker-compose.yml \
               -f infra/docker/docker-compose.override.yml \
               --env-file infra/docker/.env ps

# ✅✅ Atalho — só roda o deploy.sh, que já faz isso:
./scripts/deploy.sh
```

Após F-116 (base yml suporta `PGDATA_HOST_PATH` via env vars), o override é redundante e será removido em limpeza futura. Por enquanto continua existindo na VM por compatibilidade.

---

## Índice do Plano (`plano_implementacao.md`)

Use estes links para ir direto à seção relevante em vez de ler o arquivo inteiro.

| Seção | Linha | Conteúdo |
|-------|-------|----------|
| [§1 Visão Geral](plano_implementacao.md#L11) | 11 | Contexto, objetivos, não-objetivos, métricas de sucesso |
| [§1.1 Contexto de Negócio](plano_implementacao.md#L13) | 13 | Por que o projeto existe, o que substitui |
| [§1.2 Objetivos Funcionais](plano_implementacao.md#L19) | 19 | Lista dos 8 requisitos funcionais do sistema |
| [§1.3 Não Objetivos](plano_implementacao.md#L32) | 32 | **Leia antes de adicionar qualquer coisa nova** |
| [§1.4 Métricas de Sucesso](plano_implementacao.md#L41) | 41 | KPIs que definem quando o projeto é bem-sucedido |
| [§2 Glossário](plano_implementacao.md#L53) | 53 | Fingerprint, constellation map, peak pair, LUFS, SBR etc. |
| [§3 Riscos Técnicos](plano_implementacao.md#L81) | 81 | Catálogo de R1–R37: todos os riscos mapeados |
| [§3.1 Riscos de Áudio Broadcast](plano_implementacao.md#L85) | 85 | R1–R10: compressão, clipping, codecs, AM |
| [§3.2 Riscos de Transporte](plano_implementacao.md#L107) | 107 | R11–R17: quedas, dropouts, mudança de URL |
| [§3.3 Riscos de Detecção](plano_implementacao.md#L123) | 123 | R18–R25: falsos positivos/negativos |
| [§3.4 Riscos Operacionais](plano_implementacao.md#L141) | 141 | R26–R37: memory leak, cooldown, storage |
| [§4 Mitigações](plano_implementacao.md#L169) | 169 | Como cada risco é endereçado |
| [§5 Arquitetura de Alto Nível](plano_implementacao.md#L229) | 229 | Componentes, fluxo de dados, deployment |
| [§5.1 Visão de Componentes](plano_implementacao.md#L231) | 231 | Diagrama textual dos componentes |
| [§5.2 Fluxo de Dados](plano_implementacao.md#L249) | 249 | Como os dados fluem entre componentes |
| [§5.3 Comunicação entre Componentes](plano_implementacao.md#L310) | 310 | Protocolos internos |
| [§5.4 Modelo de Deployment](plano_implementacao.md#L322) | 322 | Topologia de servidores |
| [§6 Stack Tecnológico](plano_implementacao.md#L420) | 420 | Linguagens, frameworks, bibliotecas — com justificativas |
| [§6.1 Stack Definitiva](plano_implementacao.md#L334) | 334 | Decisões fechadas de tecnologia |
| [§6.2 Justificativas](plano_implementacao.md#L356) | 356 | Por que cada escolha foi feita |
| [§6.3 Bibliotecas Externas](plano_implementacao.md#L386) | 386 | Libs específicas usadas |
| [§7 Pipeline de Fingerprint](plano_implementacao.md#L420) | 420 | Geração de fingerprint acústico do master |
| [§7.1 Fluxo Geral](plano_implementacao.md#L424) | 424 | Passos do pipeline |
| [§7.2 Comandos Concretos](plano_implementacao.md#L461) | 461 | Comandos ffmpeg exatos |
| [§7.3 Algoritmo de Fingerprint](plano_implementacao.md#L510) | 510 | Constellation map, peak pairs, hashing |
| [§7.4 Persistência](plano_implementacao.md#L601) | 601 | Como fingerprints são armazenados |
| [§7.5 Validação Pós-Geração](plano_implementacao.md#L609) | 609 | Como validar que o fingerprint gerado é bom |
| [§8 Pipeline de Ingestão](plano_implementacao.md#L621) | 621 | Stream worker: captura e análise de stream |
| [§8.1 Estrutura do Worker](plano_implementacao.md#L623) | 623 | Arquitetura interna do worker |
| [§8.2 Comando ffmpeg](plano_implementacao.md#L653) | 653 | Comando exato de ingestão |
| [§8.3 Ring Buffers](plano_implementacao.md#L682) | 682 | Buffers para evidência e análise |
| [§8.4 Pré-processamento](plano_implementacao.md#L690) | 690 | Processamento da janela antes do matching |
| [§8.5 Health Monitoring](plano_implementacao.md#L713) | 713 | Como detectar worker travado |
| [§8.6 Reconexão](plano_implementacao.md#L723) | 723 | Backoff exponencial e jitter |
| [§8.7 Restart Preventivo](plano_implementacao.md#L744) | 744 | Reinício periódico para mitigar memory leaks |
| [§9 Algoritmo de Matching](plano_implementacao.md#L751) | 751 | Matching acústico: o coração do sistema |
| [§9.1 Visão Geral](plano_implementacao.md#L755) | 755 | Overview do algoritmo |
| [§9.2 Índice em Memória](plano_implementacao.md#L768) | 768 | Estrutura de dados do índice de hashes |
| [§9.3 Match por Janela](plano_implementacao.md#L795) | 795 | Histograma de delta, algoritmo completo |
| [§9.4 Threshold Adaptativo](plano_implementacao.md#L871) | 871 | Calibração por emissora |
| [§9.5 State Machine](plano_implementacao.md#L891) | 891 | Máquina de estados de detecção |
| [§9.6 Cobertura Temporal](plano_implementacao.md#L928) | 928 | Cálculo de cobertura para confirmação |
| [§9.7 Matching Multi-Rate](plano_implementacao.md#L950) | 950 | Tolerância a time stretching (R8) |
| [§9.8 Desambiguação de Versões](plano_implementacao.md#L962) | 962 | Escolha entre cortes do mesmo comercial (R20) |
| [§9.9 Audit de Evidência Pré-Persist](plano_implementacao.md#L979) | 979 | Re-fingerprint do clipe salvo × master antes de marcar veiculação como confirmada |
| [§10 Verificação Neural](plano_implementacao.md#L1037) | 1037 | Camada de verificação por embeddings neurais |
| [§10.1 Quando Ativar](plano_implementacao.md#L985) | 985 | Condições para uso da camada neural |
| [§10.2 Modelo](plano_implementacao.md#L995) | 995 | Modelo de embedding usado |
| [§10.3 Pipeline](plano_implementacao.md#L1001) | 1001 | Pipeline de verificação neural |
| [§11 Geração de Evidência](plano_implementacao.md#L1033) | 1033 | Gravação e armazenamento de clips de evidência |
| [§11.1 Fluxo](plano_implementacao.md#L1035) | 1035 | Como a evidência é gerada |
| [§11.2 Encoding Final](plano_implementacao.md#L1048) | 1048 | Comando de encoding do clip |
| [§11.3 Layout no Storage](plano_implementacao.md#L1062) | 1062 | Estrutura de pastas/buckets |
| [§11.4 Retenção e Tiering](plano_implementacao.md#L1079) | 1079 | Política de 30 dias hot / 12 meses cold |
| [§11.5 Tratamento de Falhas](plano_implementacao.md#L1087) | 1087 | O que fazer quando o clip não salva |
| [§12 Esquema de Dados](plano_implementacao.md#L1097) | 1097 | DDL PostgreSQL completo |
| [§12.1 DDL Completo](plano_implementacao.md#L1099) | 1099 | Tabelas, índices, constraints |
| [§12.2 Estimativas de Volume](plano_implementacao.md#L1358) | 1358 | Projeção de crescimento dos dados |
| [§13 Contratos de API](plano_implementacao.md#L1371) | 1371 | API REST e eventos NATS |
| [§13.1 API Externa (Clientes)](plano_implementacao.md#L1373) | 1373 | Endpoints para integração de clientes |
| [§13.2 API Interna (Operadores)](plano_implementacao.md#L1493) | 1493 | Endpoints de operação |
| [§13.3 Eventos NATS](plano_implementacao.md#L1504) | 1504 | Mensageria interna |
| [§14 Infraestrutura](plano_implementacao.md#L1536) | 1536 | Topologia de servidores, networking, backup |
| [§14.1 Topologia](plano_implementacao.md#L1538) | 1538 | Layout de servidores recomendado |
| [§14.2 Distribuição de Workers](plano_implementacao.md#L1569) | 1569 | Como distribuir workers entre servidores |
| [§14.3 Networking](plano_implementacao.md#L1573) | 1573 | Rede, IPs, pool para anti-bloqueio |
| [§14.4 Backup e DR](plano_implementacao.md#L1581) | 1581 | Estratégia de backup e disaster recovery |
| [§14.5 Provisionamento](plano_implementacao.md#L1600) | 1600 | Configuração de servidores |
| [§15 Observabilidade](plano_implementacao.md#L1622) | 1622 | Métricas, logs, tracing, dashboards, alertas |
| [§15.1 Métricas Prometheus](plano_implementacao.md#L1624) | 1624 | Lista de métricas expostas |
| [§15.2 Logging](plano_implementacao.md#L1659) | 1659 | Estratégia de logging estruturado |
| [§15.3 Tracing](plano_implementacao.md#L1666) | 1666 | Distributed tracing |
| [§15.4 Dashboards Grafana](plano_implementacao.md#L1677) | 1677 | Painéis de monitoramento |
| [§15.5 Alertas](plano_implementacao.md#L1697) | 1697 | Regras de alerta crítico |
| [§15.6 Runbooks](plano_implementacao.md#L1714) | 1714 | Procedimentos de resposta a incidentes |
| [§16 Segurança](plano_implementacao.md#L1736) | 1736 | Auth, roles, proteção de dados, audit log |
| [§17 Migração e Coexistência](plano_implementacao.md#L1797) | 1797 | Convivência com o fornecedor atual |
| [§18 Plano de Execução por Fases](plano_implementacao.md#L1845) | 1845 | Cronograma: 4 fases ao longo de 36 semanas |
| [§18.1 Fase 1 — Prova Técnica](plano_implementacao.md#L1847) | 1847 | Semanas 1–6: PoC funcional |
| [§18.2 Fase 2 — Hardening](plano_implementacao.md#L1873) | 1873 | Semanas 7–18: estabilização e coexistência |
| [§18.2.1 Ciclo de vida de campanha](plano_implementacao.md#L1893) | 1893 | Estados programada/ativa/concluida/cancelada com transição automática por data |
| [§18.2.2 Desambiguação de versões](plano_implementacao.md#L1970) | 1970 | Dedup pós-confirmação entre cortes 30s/60s do mesmo cliente |
| [§18.3 Fase 3 — Escala](plano_implementacao.md#L1892) | 1892 | Semanas 19–30: migração comercial |
| [§18.4 Fase 4 — Corte Total](plano_implementacao.md#L1908) | 1908 | Semanas 31–36: desligamento do fornecedor |
| [§18.5 Cronograma Sumário](plano_implementacao.md#L1922) | 1922 | Tabela resumida |
| [§18.6 Go/No-Go](plano_implementacao.md#L1934) | 1934 | Marcos de decisão entre fases |
| [§19 Validação](plano_implementacao.md#L1945) | 1945 | Testes unitários, integração, carga, chaos |
| [§20 Estimativa de Custos](plano_implementacao.md#L2009) | 2009 | Infraestrutura, equipe, comparativo com fornecedor |
| [§21 Riscos do Projeto](plano_implementacao.md#L2065) | 2065 | Riscos de prazo, técnicos, regulatórios |
| [§22 Decisões Pendentes](plano_implementacao.md#L2120) | 2120 | Decisões tomadas, premissas, itens aguardando input |
| [§23 Referências](plano_implementacao.md#L2158) | 2158 | Material de estudo e papers |
| [§24 Apêndice A — ffmpeg](plano_implementacao.md#L2174) | 2174 | Comandos ffmpeg de referência |
| [§25 Apêndice B — Constantes](plano_implementacao.md#L2234) | 2234 | Tabela de constantes do sistema |
| [§26 Apêndice C — Estrutura de Repositório](plano_implementacao.md#L2276) | 2276 | Layout de pastas esperado |
| [§27 Conclusão e Próximos Passos](plano_implementacao.md#L2401) | 2401 | Próximas ações e critério de sucesso final |

---

## Contexto Rápido

- **Problema:** substituir fornecedor de monitoramento de comerciais em rádio que custa caro e tem atendimento ruim.
- **Meta principal:** monitorar 200+ emissoras simultaneamente, detectar comerciais em <10s, com 98%+ de concordância com o fornecedor atual.
- **Núcleo técnico:** fingerprinting acústico estilo Shazam (constellation map + histogram de delta), com broadcast simulation na referência para robustez a degradação de stream.
- **Fase atual:** ver §18 para o cronograma. Fase 1 é PoC técnico (semanas 1–6).
- **Stack:** ver §6. Decisões fechadas; não sugira trocar sem justificativa forte.

---

## Documentação Oficial

A documentação operacional do sistema fica em `/docs`. Ao implementar uma funcionalidade:
1. Implemente o código.
2. Crie ou atualize o arquivo correspondente em `/docs`.
3. Não modifique `plano_implementacao.md` para documentar o que foi feito — esse arquivo é blueprint, não changelog.

### Operação básica que você precisa saber antes de mexer

- **Migrations:** runner automático via service `migrate` no docker-compose. Aplica `migrations/*.up.sql` antes do `api` subir. Adicionar nova migration = criar arquivo `0NNN_name.up.sql`/`.down.sql` e fazer `docker compose up -d --build`. Ver [docs/migrations.md](docs/migrations.md) — leitura obrigatória antes de mexer em schema.
- **Auth:** todas as rotas `/v1/internal/*` exigem JWT (admin/operator). Bootstrap admin idempotente via env vars `RADIOCHECK_BOOTSTRAP_ADMIN_EMAIL` / `RADIOCHECK_BOOTSTRAP_ADMIN_PASSWORD` no `.env` (criado se não existir, no-op se já existir). Mutações sensíveis (webhook config, campaign cancel) exigem role admin. Ver [docs/auth-bootstrap.md](docs/auth-bootstrap.md).
- **Tracing:** OTLP gRPC pra Jaeger (`localhost:16686`). Setar `OTEL_EXPORTER_OTLP_ENDPOINT=` (vazio) desliga sem panic. Ver [docs/tracing.md](docs/tracing.md).
- **Worker reconciler:** a cada 30s cada worker reconfere no banco se a lista de comerciais carregada bate com o que deveria — e reinicia caso divirja. Métricas `radiocheck_worker_commercials{station_id}` e `radiocheck_worker_reconcile_runs_total` no Prometheus. Existe pra impedir que `target_stations` editado em campanha ativa deixe worker cego (incidente 2026-05-08). Ver [docs/worker-commercial-reconciler.md](docs/worker-commercial-reconciler.md).
- **Shared-hash detection:** algoritmo bidirecional que distingue subset (corte 30s/60s) de sting (~6s compartilhados) e pula comerciais < 10s. Ver [docs/shared-hash-detection.md](docs/shared-hash-detection.md). Bidirecional + skip vieram dos incidentes 2026-05-09 (sting) e 2026-05-12 (subset) — ver [docs/incident-2026-05-09-jingle-falsepos.md](docs/incident-2026-05-09-jingle-falsepos.md).
- **Durabilidade de dados:** prod usa bind mount em `/srv/radiocheck/*` + pg_dump diário no R2 + snapshot do disco GCP + alerta Prometheus de backup ausente. Ver [docs/data-durability.md](docs/data-durability.md). Histórico de como chegamos aqui em [docs/incident-2026-05-12-pgdata-loss.md](docs/incident-2026-05-12-pgdata-loss.md).
- **Follow-ups conhecidos:** ver [docs/follow-ups-fase2.md](docs/follow-ups-fase2.md) — dívida técnica catalogada (F-01 a F-83) que deve ser resolvida antes/durante a Fase 3. Novos follow-ups F-100+ rastreados nos respectivos docs de incidente.

### Estado das branches stacked (estado em 2026-05-12)

Ao final do incidente de 2026-05-12, ficaram 4 branches stacked não-mergeadas em master. Se você é um agente futuro entrando no projeto, **verifique primeiro se elas já foram mergeadas** antes de assumir que o estado do master é o atual:

| Branch | Conteúdo principal |
|--------|--------------------|
| `fix/backup-actually-runs` | Backup container que de fato roda (entrypoint + env vars + aws-cli) + postmortem do incidente. Base: `master`. |
| `fix/postgres-bind-mount` | Bind mount pgdata/minio/masters + node-exporter + F-110 alerta + tzdata + RADIOCHECK_ENV parametrizado. Base: `fix/backup-actually-runs`. |
| `fix/sharing-bidirectional-subset` | F-108 v3 (subset bidirecional + skip de comerciais < 10s). Base: `master`. |
| `feat/campaign-wizard-foundations` | Novo fluxo de cadastro de campanhas em wizard (65 commits). Base: `master`. |

`git log --all --not master` lista o que está fora de master. Verificar antes de assumir qualquer estado.
