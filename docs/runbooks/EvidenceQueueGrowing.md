# EvidenceQueueGrowing

## Sintomas
Spool de upload de evidências (`radiocheck_evidence_queue_size`) acima de 100 itens **e** crescendo (derivada > 0) por 15 minutos. Diferente de `EvidenceUploadFailures` — aqui não estamos vendo erros explícitos, apenas que a fila não drena. Pode indicar gargalo de banda ou backpressure silencioso.

## Causas Comuns
1. Endpoint S3/R2 lento (não falhando, apenas com latência alta)
2. Banda de upload do servidor saturada (concorrência com backup, tiering, outro tráfego)
3. Detecções acima do volume normal (campanha de pico) e workers gerando evidência mais rápido do que o uploader consegue enviar
4. Worker uploader com poucas threads — não escala com volume
5. Disco local lento → escrever spool já é gargalo

## Diagnóstico
```bash
# Tamanho atual da fila e taxa
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=radiocheck_evidence_queue_size' | jq
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=deriv(radiocheck_evidence_queue_size[10m])' | jq

# Listar arquivos no spool e tamanho total
ls -lh /var/spool/radiocheck/evidence-spool/ | head -20
du -sh /var/spool/radiocheck/evidence-spool/

# Latência de upload (deve existir um histograma; se não, medir manualmente)
time curl -X PUT --data-binary @/var/spool/radiocheck/evidence-spool/<algum_arquivo> \
  -H "Authorization: ..." <s3_endpoint>/<bucket>/test.opus

# IO do disco local
iostat -x 1 5

# Banda de saída
iftop -t -s 10 2>&1 | head -20
```

## Correção
- **Caso A — endpoint lento**: confirmar com provedor de storage. Se persistente, considerar bucket alternativo ou região mais próxima.
- **Caso B — banda saturada**: pausar tiering temporariamente (`docker compose stop evidence-tiering`) ou reagendar backup.
- **Caso C — pico de detecções**: aumentar `EVIDENCE_UPLOAD_CONCURRENCY` no env do `api` (default tipicamente 4). Cuidado: subir demais pode saturar mais ainda.
- **Caso D — disco lento**: verificar se o spool está em SSD; mover se estiver em HDD/NFS.
- **Caso E — alívio emergencial**: aumentar tamanho do filesystem do spool antes que o disco encha; ou descartar (`rm`) os mais antigos com aviso ao time — perda controlada de evidência é melhor que perda de detecção.

## Escalação
Se a fila ultrapassar 1000 itens ou o crescimento continuar após 1h de mitigação, escalar para `#ops`. Se houver risco real de encher disco em <30min, descartar evidências antigas e abrir incidente — comunicar clientes.

## Prevenção
- Definir SLO interno de tamanho médio de fila (ex.: <50 em P95).
- Provisionar a banda esperada com folga de 2x o pico observado.
- Considerar upload assíncrono para fila externa (SQS/NATS) já em Fase 2 se o volume for alto.
