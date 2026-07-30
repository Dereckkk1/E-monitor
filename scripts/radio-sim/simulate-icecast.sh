#!/bin/sh
# Simulador de radio (variante Icecast) — multi-cliente, sobrevive ao ffprobe
# de pre-flight do worker (2 conexoes). ffmpeg encoda a playlist e empurra pro
# Icecast local; o worker consome http://radio-sim:8000/stream como radio real.
# Vars: QUALITY, PORT, GAP_SECONDS, MASTERS_DIR (iguais ao simulate.sh original).
set -eu

QUALITY="${QUALITY:-fm-standard}"
PORT="${PORT:-8000}"
GAP_SECONDS="${GAP_SECONDS:-15}"
MASTERS_DIR="${MASTERS_DIR:-/masters}"

echo "==> generating $GAP_SECONDS s of background"
ffmpeg -y -f lavfi -i "anoisesrc=color=pink:amplitude=0.08:duration=$GAP_SECONDS" \
       -ar 44100 -ac 2 -c:a pcm_s16le /tmp/gap.wav 2>/dev/null

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
[ "$COUNT" -eq 0 ] && { echo "ERROR: no masters in $MASTERS_DIR"; exit 1; }
echo "==> playlist: $COUNT masters + gaps"

case "$QUALITY" in
    fm-hifi)       AFILTER="loudnorm=I=-14:LRA=7:TP=-1"; BITRATE="128k"; SR="44100"; CH="2" ;;
    fm-standard)   AFILTER="acompressor=threshold=-18dB:ratio=3:attack=10:release=100,equalizer=f=8000:t=h:width=2000:g=2,loudnorm=I=-12:LRA=5:TP=-1"; BITRATE="96k"; SR="44100"; CH="2" ;;
    fm-compressed) AFILTER="acompressor=threshold=-24dB:ratio=6:attack=5:release=50,acompressor=threshold=-12dB:ratio=4:attack=2:release=30,alimiter=limit=0.97,loudnorm=I=-9:LRA=3:TP=-0.5"; BITRATE="64k"; SR="44100"; CH="2" ;;
    am)            AFILTER="highpass=f=300,lowpass=f=5000,acompressor=threshold=-24dB:ratio=8:attack=3:release=40,alimiter=limit=0.95,loudnorm=I=-7:LRA=2:TP=-0.5"; BITRATE="48k"; SR="22050"; CH="1" ;;
    bad-stream)    AFILTER="acompressor=threshold=-18dB:ratio=4,loudnorm=I=-12:LRA=4:TP=-1"; BITRATE="32k"; SR="22050"; CH="1" ;;
    *) echo "ERROR: quality desconhecida: $QUALITY"; exit 1 ;;
esac

echo "==> preset=$QUALITY bitrate=$BITRATE sr=$SR ch=$CH"

mkdir -p /tmp/ice/web /tmp/ice/admin /tmp/ice/log
cat > /tmp/ice/icecast.xml <<EOF
<icecast>
  <location>sim</location>
  <admin>sim@localhost</admin>
  <limits><clients>16</clients><sources>2</sources></limits>
  <authentication>
    <source-password>simpass</source-password>
    <admin-user>admin</admin-user>
    <admin-password>simpass</admin-password>
  </authentication>
  <hostname>radio-sim</hostname>
  <listen-socket><port>$PORT</port></listen-socket>
  <fileserve>0</fileserve>
  <paths>
    <basedir>/tmp/ice</basedir>
    <logdir>/tmp/ice/log</logdir>
    <webroot>/tmp/ice/web</webroot>
    <adminroot>/tmp/ice/admin</adminroot>
  </paths>
  <logging>
    <accesslog>access.log</accesslog>
    <errorlog>error.log</errorlog>
    <loglevel>3</loglevel>
  </logging>
  <security>
    <chroot>0</chroot>
    <changeowner><user>icecast</user><group>icecast</group></changeowner>
  </security>
</icecast>
EOF
chown -R icecast:icecast /tmp/ice 2>/dev/null || true
icecast -c /tmp/ice/icecast.xml &
sleep 2
echo "==> icecast up; servindo em http://0.0.0.0:$PORT/stream"

while true; do
    ffmpeg -hide_banner -loglevel warning \
        -re -stream_loop -1 \
        -f concat -safe 0 -i "$PLAYLIST" \
        -af "$AFILTER" \
        -c:a aac -b:a "$BITRATE" -ar "$SR" -ac "$CH" \
        -f adts -content_type audio/aac \
        "icecast://source:simpass@127.0.0.1:$PORT/stream" || true
    echo "==> source caiu, reiniciando push..."
    sleep 1
done
