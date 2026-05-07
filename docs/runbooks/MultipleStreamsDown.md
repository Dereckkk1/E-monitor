# MultipleStreamsDown

## Sintomas
Três ou mais workers ficaram sem receber bytes simultaneamente nos últimos 5 minutos. Diferente de `StreamDownProlongado` (que dispara para uma emissora isolada), aqui o padrão sugere causa raiz **compartilhada** entre múltiplas emissoras: rede, DNS, proxy de saída, ou bloqueio massivo por upstream comum.

## Causas Comuns
1. Queda de rede no host (link de saída, switch, gateway)
2. DNS do host respondendo lentamente ou retornando NXDOMAIN
3. IP do servidor entrou em blocklist de várias CDNs (StreamGuys, Shoutcast, Triton)
4. Proxy de saída / pool de IPs (Fase 3) com saída quebrada
5. ffmpeg upstream do host com problema de sysctl (file descriptors, conntrack)
6. Janela de manutenção em um datacenter compartilhado por várias emissoras

## Diagnóstico
```bash
# Quantos workers estão sem bytes?
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=count(increase(radiocheck_worker_bytes_received_total[5m]) == 0)' | jq

# Quais emissoras especificamente?
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=increase(radiocheck_worker_bytes_received_total[5m]) == 0' | jq '.data.result[].metric.station_id'

# Conectividade básica do host
ping -c 3 8.8.8.8
ping -c 3 1.1.1.1
dig +short stream.example.com  # alguma das emissoras afetadas

# Confere se é só host: tente baixar um stream conhecido
curl -I --max-time 5 https://ice.somafm.com/groovesalad

# Tabela conntrack no host (pode estar cheia)
sysctl net.netfilter.nf_conntrack_count net.netfilter.nf_conntrack_max
```

## Correção
- **Caso A — rede do host caiu**: verificar gateway e link com o time de infra. Workers reconectam automaticamente após restauração via backoff exponencial (§8.6).
- **Caso B — DNS lento**: trocar resolver para `1.1.1.1` ou `8.8.8.8` no `/etc/resolv.conf` do host (ou `dns:` do `docker-compose.yml`); reiniciar o serviço `api`.
- **Caso C — IP em blocklist**: trocar IP de saída do servidor; em Fase 3 ativar o pool rotativo de IPs (§14.3).
- **Caso D — conntrack cheio**: aumentar `net.netfilter.nf_conntrack_max` e `net.netfilter.nf_conntrack_buckets`.
- **Caso E — datacenter remoto em manutenção**: nada a fazer; aguardar e confirmar que reconexões automáticas voltam após janela.

## Escalação
- Se 5+ emissoras seguirem down após 15min, abrir incidente em `#ops` e notificar o on-call de infra.
- Se o padrão de IP em blocklist se repetir em 2 ocorrências no mês, antecipar a implementação do pool de IPs (§14.3) — não é mais um problema isolado.

## Prevenção
- Monitorar `radiocheck_worker_reconnects_total` semanalmente — picos sugerem instabilidade de rede crônica.
- Considerar mover ingestão para uma região/IP diferente de cada cluster de emissoras (anti-blocklist por design).
- Em Fase 3, garantir que o pool de IPs tenha pelo menos 3 IPs distintos por região.
