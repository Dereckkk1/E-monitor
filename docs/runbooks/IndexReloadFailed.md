# IndexReloadFailed

## Sintomas
O índice de fingerprints não pôde ser carregado na memória durante a inicialização do serviço. Workers iniciam sem comerciais para detectar.

## Causas Comuns
1. PostgreSQL inacessível no momento do startup
2. Tabela `commercial_hashes` vazia — nenhum comercial foi processado ainda
3. Fingerprint pipeline falhou para todos os comerciais (ver logs do serviço `fingerprint`)
4. Memória insuficiente para carregar o índice completo

## Diagnóstico
```bash
# Verificar se o banco está acessível
docker compose exec postgres psql -U radiocheck -c "SELECT COUNT(*) FROM commercial_hashes;"

# Ver logs de startup do worker
docker compose logs api | grep -i "index\|fingerprint\|load" | head -50

# Contar hashes por comercial
docker compose exec postgres psql -U radiocheck -c \
  "SELECT commercial_id, COUNT(*) FROM commercial_hashes GROUP BY commercial_id ORDER BY 2 DESC LIMIT 10;"

# Ver uso de memória do container api
docker stats api --no-stream
```

## Correção
1. **Banco inacessível**: aguardar o PostgreSQL subir; o serviço tentará recarregar no próximo restart
2. **Tabela vazia**: rodar o pipeline de fingerprint para os comerciais cadastrados via `fingerprint/fingerprint/main.py`
3. **Memória insuficiente**: aumentar limite de memória do container ou reduzir número de comerciais ativos
4. **Força reload**: `POST /v1/internal/index/reload` (se implementado) ou reiniciar o container `api`

## Escalação
Se o índice não carregar após 3 tentativas, acionar time de desenvolvimento via `#dev-radiocheck`.
