---
status: parcialmente-implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - infra/prometheus/alerts.yml
  # depende de node_exporter com modulo time habilitado (infra externa)
---

# ClockDrift

## Sintomas
Drift de relógio (`node_timex_offset_seconds`) acima de 2 segundos em algum host por 5 minutos. Compromete diretamente a correlação temporal de detecções entre workers e o cooldown anti-duplicata (§9.5), além de timestamps em evidências e webhooks.

## Causas Comuns
1. NTP/chrony parado ou sem peers configurados
2. Firewall bloqueando porta UDP 123 (NTP)
3. VM hibernou e voltou (`hwclock` desincronizou)
4. Container rodando com `--privileged` ajustando o relógio do host
5. Servidor NTP escolhido fora do ar

## Diagnóstico
```bash
# Estado do chrony no host
chronyc tracking
chronyc sources

# Ou systemd-timesyncd
timedatectl status
systemctl status systemd-timesyncd

# Offset reportado pelo Prometheus
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=abs(node_timex_offset_seconds)' | jq

# Diferença manual entre host e referência
ntpdate -q pool.ntp.org

# Containers herdam o relógio do host — confirmar
docker compose exec api date
date
```

## Correção
- **Caso A — chrony parado**: `systemctl restart chronyd` (ou `systemd-timesyncd`).
- **Caso B — firewall**: liberar UDP 123 saída no host. Em redes restritas, usar NTP interno do datacenter.
- **Caso C — VM hibernou**: `hwclock --hctosys` no host (e configurar `chrony` para correção rápida via `makestep`).
- **Caso D — peers ruins**: trocar `pool.ntp.org` por servidores específicos da região (ex.: `a.ntp.br`, `b.ntp.br`, `c.ntp.br` no Brasil).
- **Caso E — container com tempo distinto**: garantir que `docker run` não recebe `--privileged` desnecessariamente; evitar montar `/etc/localtime` divergente.

## Escalação
- Drift > 30s → tratar como incidente crítico mesmo que o alerta seja warning. Pode causar detecções fora de janela e falhas de cooldown.
- Após 2 ocorrências em 24h, escalar para `#ops` para revisão da configuração de NTP do datacenter.

## Prevenção
- Configurar `chrony` com `makestep 1.0 3` para corrigir drift agressivo no boot.
- Adicionar verificação de NTP em healthcheck do container (ex.: `chronyc tracking | grep -q "Leap status.*Normal"`).
- Documentar servidores NTP usados em cada região no inventário de infra.
