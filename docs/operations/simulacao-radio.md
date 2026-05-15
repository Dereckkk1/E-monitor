# Simulação de Stream de Rádio

Ferramenta para validar o pipeline de detecção ponta-a-ponta sem depender de uma rádio real tocar os comerciais cadastrados.

## Como funciona

O serviço `radio-sim` (perfil `sim` no docker-compose) sobe um container que:

1. Lê todos os arquivos master da pasta `/data/masters` (mesmo volume usado pelo `fingerprint` e pelo `api`).
2. Monta uma playlist alternando **comercial → gap de 15s → comercial → gap → ...**, em loop infinito.
3. Aplica filtros de áudio simulando o processamento típico de uma emissora real (compressão, EQ, limitação, redução de bitrate).
4. Serve a stream via HTTP no formato ADTS AAC.

Os 5 presets de qualidade cobrem o espectro do que o sistema vai encontrar em produção.

## Presets disponíveis

| Preset           | Bitrate | Sample rate | Canais | Cadeia                                           | Quando usar                                        |
|------------------|---------|-------------|--------|--------------------------------------------------|----------------------------------------------------|
| `fm-hifi`        | 128 kbps| 44.1 kHz    | stereo | só loudnorm (-14 LUFS)                           | benchmark — caso ideal, deve detectar sempre        |
| `fm-standard`    | 96 kbps | 44.1 kHz    | stereo | compressor + EQ + loudnorm (-12 LUFS)            | rádio digital de qualidade média                    |
| `fm-compressed`  | 64 kbps | 44.1 kHz    | stereo | multibanda + limiter pesado + loudnorm (-9 LUFS) | FM brasileira típica (cadeia agressiva)             |
| `am`             | 48 kbps | 22 kHz      | mono   | bandpass 300–5000 Hz + compressão pesada         | AM degradado                                        |
| `bad-stream`     | 32 kbps | 22 kHz      | mono   | low bitrate, sem processamento sofisticado       | stream de baixa qualidade / problemas de conexão    |

## Como usar

### 1. Subir o simulador

```bash
cd infra/docker
docker compose --profile sim up -d radio-sim

# ou com qualidade específica:
SIM_QUALITY=fm-compressed docker compose --profile sim up -d radio-sim

# para acompanhar logs:
docker compose logs -f radio-sim
```

O simulador ficará acessível em:
- **Internamente** (de outros containers): `http://radio-sim:8000/stream`
- **Externamente** (no seu navegador / VLC para conferir): `http://localhost:18000/stream`

### 2. Apontar uma emissora para o simulador

Atualize a `stream_url` de uma emissora **ativa** (cadastrada via frontend) para apontar ao simulador. Como o frontend ainda não tem edição de URL, faça via SQL:

```bash
docker exec docker-postgres-1 psql -U radiocheck -d radiocheck -c "
  UPDATE stations SET stream_url = 'http://radio-sim:8000/stream' WHERE name = 'Itapoá';
"
```

Substitua `'Itapoá'` pelo nome da emissora que você quer transformar em alvo de teste.

### 3. Reiniciar o worker da emissora

O worker captura o `stream_url` no momento de iniciar — para forçar releitura, **pause e inicie a campanha** novamente pelo frontend (botões Pausar → Iniciar no card da campanha).

Como confirmação, o log do `api` deve mostrar o worker reconectando ao novo URL:

```bash
docker logs docker-api-1 --tail 20 | grep "worker started"
# → "stream_url":"http://radio-sim:8000/stream","commercials":N
```

### 4. Validar detecção

Em até ~30 segundos (o tempo de uma volta no playlist + janela de confirmação), uma detecção deve aparecer na página **Veiculações**. Para verificar via DB:

```bash
docker exec docker-postgres-1 psql -U radiocheck -d radiocheck -c "
  SELECT detected_at, commercial_id, confidence, evidence_status
  FROM detections ORDER BY detected_at DESC LIMIT 5;
"
```

### 5. Voltar ao stream real

Quando terminar o teste:

```bash
docker exec docker-postgres-1 psql -U radiocheck -d radiocheck -c "
  UPDATE stations SET stream_url = '<URL_ORIGINAL>' WHERE name = 'Itapoá';
"
docker compose --profile sim down
```

## Variáveis de ajuste

Coloque no `.env` ou passe inline:

| Variável          | Default       | O que faz                                        |
|-------------------|---------------|--------------------------------------------------|
| `SIM_QUALITY`     | `fm-standard` | Preset de qualidade (lista acima)                |
| `SIM_PORT`        | `18000`       | Porta exposta no host                            |
| `SIM_GAP_SECONDS` | `15`          | Segundos de gap entre comerciais                 |

## Limitações conhecidas

- **Uma conexão por vez**: o `ffmpeg -listen 1` aceita um cliente; se o worker reconectar, o simulador reinicia o servidor automaticamente (loop no script). Mas se houver **dois** workers tentando consumir, só um vai conseguir.
- **Sem variabilidade temporal**: comerciais aparecem em intervalos regulares. Para testar cooldown e desambiguação, ajuste `SIM_GAP_SECONDS` para algo curto (ex.: `5`) e veja se o sistema evita duplicatas.
- **Sem ruído de fundo dinâmico**: o gap é ruído rosa estático, não música. Para teste mais realista, substitua o gap no script por um arquivo de música real.

## Suite mínima de validação

Para considerar o PoC validado, rodar com pelo menos 3 presets diferentes e confirmar:

| Preset          | Detecta? | Confiança esperada |
|-----------------|----------|--------------------|
| `fm-hifi`       | sempre   | > 95%              |
| `fm-standard`   | sempre   | > 90%              |
| `fm-compressed` | sempre   | > 80%              |
| `am`            | sempre   | > 70%              |
| `bad-stream`    | maioria  | > 60%              |

Se algum preset abaixo de `bad-stream` falhar, o tuning de threshold/vizinhança precisa ser revisitado antes de avançar para a Fase 2.
