# StreamDownProlongado

## Sintomas
Worker de uma emissora sem bytes recebidos por mais de 10 minutos. Alerta dispara no Slack via `#alertas-radiocheck`.

## Causas Comuns
1. Stream URL mudou (emissora trocou CDN ou provedor)
2. Bloqueio de IP pelo servidor da emissora
3. ffmpeg travado em estado inconsistente
4. Queda de rede no servidor

## Diagnóstico
```bash
# Ver status de todos os workers
curl -s http://localhost:8080/internal/workers | jq

# Ver logs do worker específico
docker compose logs api | grep <station_id> | tail -50

# Testar stream URL manualmente
ffmpeg -i <stream_url> -t 5 -f null - 2>&1

# Ver métrica de bytes no Prometheus
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=increase(radiocheck_worker_bytes_received_total[10m])' | jq
```

## Correção
1. **URL mudou**: atualizar via `PATCH /v1/internal/stations/{id}` com nova `stream_url`
2. **IP bloqueado**: trocar IP de saída do servidor; em Fase 3 usar pool de IPs rotativo
3. **ffmpeg travado**: o health ticker detecta e reinicia automaticamente em até 60s; se o restart falhar, reiniciar o container `api`
4. **Queda de rede**: verificar conectividade com `curl -I <stream_url>`; aguardar restauração

## Escalação
Se após 30 minutos o alerta não resolver automaticamente, acionar o time de infra via canal `#ops`.
