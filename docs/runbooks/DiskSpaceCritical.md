# DiskSpaceCritical

## Sintomas
Menos de 5% de espaço livre no disco raiz do servidor. Alerta dispara no Slack. Risco de falha de escrita nos logs, spool de evidências e banco de dados.

## Causas Comuns
1. Logs do Docker crescendo sem limite configurado
2. Spool de evidências acumulado por falhas prolongadas de upload
3. Crescimento inesperado do volume PostgreSQL (WAL logs, tabelas grandes)
4. Imagens Docker antigas acumuladas

## Diagnóstico
```bash
# Ver uso geral do disco
df -h /

# Ver maiores consumidores
du -sh /var/lib/docker/* 2>/dev/null | sort -h | tail -10
du -sh /var/spool/radiocheck/ 2>/dev/null

# Ver volumes Docker
docker system df

# Ver tamanho dos logs de cada container
docker compose logs --no-log-prefix api 2>/dev/null | wc -c
```

## Correção
1. **Logs Docker**: `docker compose down && docker system prune -f --volumes=false`; configurar `--log-opt max-size=100m --log-opt max-file=3` nos serviços
2. **Spool cheio**: verificar se o endpoint S3 voltou; limpar arquivos mais antigos que 7 dias se o endpoint continua inacessível
3. **Imagens antigas**: `docker image prune -a -f`
4. **WAL logs**: `docker compose exec postgres psql -U radiocheck -c "CHECKPOINT; SELECT pg_switch_wal();"` — e investigar crescimento de tabelas grandes

## Escalação
Se disco abaixo de 1% ou I/O de escrita falhar, parar containers não essenciais imediatamente e acionar time de infra via `#ops` com prioridade máxima.
