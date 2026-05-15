---
status: implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - workers/internal/neural/client.go
  - workers/internal/api/handlers/health.go
  # sem alerta Prometheus correspondente; verificacao manual via /health
---

# NeuralVerifierDown

## Sintomas
O sidecar `clap-verifier` não está respondendo. Detecções em estado `uncertain` não são resolvidas. O sistema continua funcionando com matching acústico mas sem verificação neural.

## Causas Comuns
1. Container `clap-verifier` parou ou travou
2. Modelo ONNX não encontrado no caminho configurado
3. `MOCK_MODE=false` e modelo não foi copiado para `/models`
4. Timeout na inicialização do modelo (modelo grande, I/O lento)

## Diagnóstico
```bash
# Verificar se o container está rodando
docker compose ps clap-verifier

# Ver logs do clap-verifier
docker compose logs clap-verifier | tail -50

# Testar endpoint de saúde
curl -s http://localhost:8081/health | jq

# Verificar se modelo existe
docker compose exec clap-verifier ls -lh /models/ 2>/dev/null
```

## Correção
1. **Container parado**: `docker compose restart clap-verifier`
2. **Modelo ausente**: copiar o arquivo `.onnx` para o volume `clap-models` e reiniciar o container
3. **Fallback imediato**: o sistema opera sem verificação neural automaticamente quando o sidecar não responde (timeout de 5s no cliente Go); nenhuma ação emergencial necessária
4. **Modo mock para diagnóstico**: setar `MOCK_MODE=true` temporariamente para confirmar que o container funciona sem o modelo

## Escalação
Se o container não iniciar após modelo verificado, acionar time de ML via `#dev-radiocheck`.
