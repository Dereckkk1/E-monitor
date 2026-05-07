# EvidenceUploadFailures

## Sintomas
Mais de 10 falhas de upload de evidência (clipes de áudio) para o storage S3/R2 na última hora. Alerta dispara no Slack.

## Causas Comuns
1. Credenciais S3/R2 expiradas ou incorretas
2. Bucket não existe ou sem permissão de escrita
3. Endpoint S3 inacessível (falha de rede ou Cloudflare R2 fora do ar)
4. Disco local cheio — spool de evidências não consegue gravar arquivos temporários

## Diagnóstico
```bash
# Ver tamanho do spool de evidências (retry queue)
ls -lh /var/spool/radiocheck/evidence-spool/ 2>/dev/null || echo "spool vazio ou caminho diferente"

# Ver logs de erros de upload
docker compose logs api | grep "evidence" | grep -i "error\|fail" | tail -50

# Testar acesso ao bucket S3
aws s3 ls s3://<bucket_name> --endpoint-url <s3_endpoint> 2>&1

# Ver métrica de falhas
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=radiocheck_evidence_upload_failures_total' | jq
```

## Correção
1. **Credenciais inválidas**: atualizar `AWS_ACCESS_KEY_ID` e `AWS_SECRET_ACCESS_KEY` nas variáveis de ambiente do container e reiniciar
2. **Bucket ausente**: criar o bucket; verificar política de CORS e permissões
3. **Rede inacessível**: verificar conectividade com `curl -I <s3_endpoint>`
4. **Disco cheio**: liberar espaço; o serviço tem retry automático — os arquivos em spool serão reenviados quando o endpoint voltar

## Escalação
Se spool crescer acima de 1 GB ou o endpoint não voltar em 2 horas, escalar para time de infra via `#ops`.
