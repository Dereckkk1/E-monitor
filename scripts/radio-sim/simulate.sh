#!/bin/sh
# Simulador de stream de rádio para validação ponta-a-ponta do Radiocheck.
# Toca os masters cadastrados em loop, com gaps de música/silêncio entre eles,
# aplicando filtros que simulam o processamento de uma emissora real.
#
# Variáveis de ambiente:
#   QUALITY      → fm-hifi | fm-standard | fm-compressed | am | bad-stream
#   PORT         → porta HTTP (default 8000)
#   GAP_SECONDS  → segundos entre comerciais (default 15)
#   MASTERS_DIR  → pasta com os masters (default /masters)
set -eu

QUALITY="${QUALITY:-fm-standard}"
PORT="${PORT:-8000}"
GAP_SECONDS="${GAP_SECONDS:-15}"
MASTERS_DIR="${MASTERS_DIR:-/masters}"

# Gap entre comerciais: ruído rosa baixo simulando "música/ambiente" (mais realista que silêncio puro).
echo "==> generating $GAP_SECONDS s of background"
ffmpeg -y -f lavfi -i "anoisesrc=color=pink:amplitude=0.08:duration=$GAP_SECONDS" \
       -ar 44100 -ac 2 -c:a pcm_s16le /tmp/gap.wav 2>/dev/null

# Pré-normaliza todos os masters para WAV 44.1kHz stereo s16le.
# O concat demuxer falha ao misturar formatos diferentes (mp3+wav), então
# convertemos tudo antes de montar a playlist.
echo "==> normalizing masters"
mkdir -p /tmp/norm
PLAYLIST=/tmp/playlist.txt
: > "$PLAYLIST"
COUNT=0
for f in "$MASTERS_DIR"/*; do
    [ -f "$f" ] || continue
    case "$f" in
        *.wav|*.mp3|*.m4a|*.aac|*.mpeg|*.WAV|*.MP3|*.M4A|*.AAC|*.MPEG)
            BASE=$(basename "$f")
            OUT="/tmp/norm/${BASE%.*}.wav"
            ffmpeg -y -i "$f" -ar 44100 -ac 2 -c:a pcm_s16le "$OUT" 2>/dev/null \
                || { echo "  [skip] $BASE (decode failed)"; continue; }
            printf "file '%s'\nfile '/tmp/gap.wav'\n" "$OUT" >> "$PLAYLIST"
            COUNT=$((COUNT + 1))
            echo "  [ok] $BASE"
            ;;
    esac
done

if [ "$COUNT" -eq 0 ]; then
    echo "ERROR: no master audio files found in $MASTERS_DIR"
    echo "Cadastre comerciais via frontend antes de subir o simulador."
    exit 1
fi

echo "==> playlist: $COUNT masters + gaps"

# Filtros por preset (broadcast simulation).
# Todos terminam com loudnorm para ficar dentro do nível típico de uma emissora.
case "$QUALITY" in
    fm-hifi)
        # Quase sem processamento — emissora premium digital.
        AFILTER="loudnorm=I=-14:LRA=7:TP=-1"
        BITRATE="128k"; SR="44100"; CH="2"
        ;;
    fm-standard)
        # Compressão leve, EQ, loudness padrão.
        AFILTER="acompressor=threshold=-18dB:ratio=3:attack=10:release=100,equalizer=f=8000:t=h:width=2000:g=2,loudnorm=I=-12:LRA=5:TP=-1"
        BITRATE="96k"; SR="44100"; CH="2"
        ;;
    fm-compressed)
        # Cadeia típica de FM brasileira: multibanda + limiter pesado.
        AFILTER="acompressor=threshold=-24dB:ratio=6:attack=5:release=50,acompressor=threshold=-12dB:ratio=4:attack=2:release=30,alimiter=limit=0.97,loudnorm=I=-9:LRA=3:TP=-0.5"
        BITRATE="64k"; SR="44100"; CH="2"
        ;;
    am)
        # AM: bandpass 300-5000Hz, mono, compressão pesada, baixa taxa.
        AFILTER="highpass=f=300,lowpass=f=5000,acompressor=threshold=-24dB:ratio=8:attack=3:release=40,alimiter=limit=0.95,loudnorm=I=-7:LRA=2:TP=-0.5"
        BITRATE="48k"; SR="22050"; CH="1"
        ;;
    bad-stream)
        # Stream ruim: bitrate muito baixo, mono, taxa reduzida.
        AFILTER="acompressor=threshold=-18dB:ratio=4,loudnorm=I=-12:LRA=4:TP=-1"
        BITRATE="32k"; SR="22050"; CH="1"
        ;;
    *)
        echo "ERROR: quality desconhecida: $QUALITY"
        echo "Disponíveis: fm-hifi fm-standard fm-compressed am bad-stream"
        exit 1
        ;;
esac

echo "==> preset=$QUALITY bitrate=$BITRATE sr=$SR ch=$CH"
echo "==> servindo em http://0.0.0.0:$PORT/stream"

# Loop externo: ffmpeg -listen 1 sai quando o cliente desconecta. Reinicia automaticamente.
while true; do
    ffmpeg -hide_banner -loglevel warning \
        -re -stream_loop -1 \
        -f concat -safe 0 -i "$PLAYLIST" \
        -af "$AFILTER" \
        -c:a aac -b:a "$BITRATE" -ar "$SR" -ac "$CH" \
        -f adts \
        -listen 1 \
        "http://0.0.0.0:$PORT/stream" || true
    echo "==> client disconnected, restarting..."
    sleep 1
done
