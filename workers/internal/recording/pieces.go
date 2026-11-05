package recording

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// pieceExt NÃO é .aac de propósito: o sidecar segments-cleanup apaga *.aac com
// mais de 60min, e uma gravação pode durar 6h. Ele ainda apaga pastas vazias,
// por isso quem escreve precisa recriar a pasta a cada tentativa.
const pieceExt = ".adts"

const pieceTimeLayout = "20060102-150405"

// a<unix da tentativa>-<AAAAMMDD-HHMMSS>.adts. O prefixo impede que um ffmpeg
// novo, no mesmo minuto, sobrescreva o pedaço do anterior.
var pieceName = regexp.MustCompile(`^a\d+-(\d{8}-\d{6})\.adts$`)

// Piece é um arquivo de áudio em disco. Start vem do nome; End vem do mtime,
// que é o último instante em que o ffmpeg escreveu nele. É o mtime que deixa o
// cálculo de buraco exato quando o ffmpeg morre no meio de um minuto.
type Piece struct {
	Path  string
	Start time.Time
	End   time.Time
	Size  int64
}

// ListPieces lista os pedaços com áudio, em ordem cronológica. Pasta
// inexistente devolve vazio: é o estado normal com o stream fora.
func ListPieces(dir string) ([]Piece, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("recording: listar pedaços: %w", err)
	}
	var out []Piece
	for _, e := range entries {
		m := pieceName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Size() == 0 {
			continue
		}
		// O ffmpeg expande o strftime no fuso do processo.
		start, err := time.ParseInLocation(pieceTimeLayout, m[1], time.Local)
		if err != nil {
			continue
		}
		out = append(out, Piece{
			Path:  filepath.Join(dir, e.Name()),
			Start: start,
			End:   info.ModTime(),
			Size:  info.Size(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

// DirBytes soma o tamanho dos pedaços. Pasta inexistente vale 0.
func DirBytes(dir string) int64 {
	pieces, err := ListPieces(dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, p := range pieces {
		total += p.Size
	}
	return total
}

// ComputeGaps devolve os intervalos de [from, to] sem áudio. Devolve slice não
// nil para serializar como []. Trechos menores que minGap são oscilação normal
// de rotação de arquivo, não buraco.
func ComputeGaps(pieces []Piece, from, to time.Time, minGap time.Duration) []Gap {
	gaps := []Gap{}
	cursor := from
	for _, p := range pieces {
		start := p.Start
		if start.After(to) {
			start = to
		}
		if start.Sub(cursor) >= minGap {
			gaps = append(gaps, Gap{From: cursor, To: start})
		}
		if p.End.After(cursor) {
			cursor = p.End
		}
	}
	if to.Sub(cursor) >= minGap {
		gaps = append(gaps, Gap{From: cursor, To: to})
	}
	return gaps
}

// FinalStatus decide o status de encerramento. A regra é binária e vem dos
// buracos, não de um percentual: o áudio é usado como prova, então qualquer
// intervalo sem áudio (ComputeGaps já descarta oscilação abaixo do mínimo)
// torna a gravação parcial, por menor que seja a fração da janela.
func FinalStatus(hasAudio, canceled bool, gaps []Gap) string {
	if canceled {
		return StatusCancelada
	}
	if !hasAudio {
		return StatusFalhou
	}
	if len(gaps) > 0 {
		return StatusParcial
	}
	return StatusConcluida
}

// adtsHeaderSize é o menor header ADTS (sem CRC). É onde o comprimento já é legível.
const adtsHeaderSize = 7

// completeFrames percorre os frames ADTS a partir do início e devolve onde termina
// o último frame completo, e quantos são. Para no primeiro frame inválido ou que
// não caiba no restante do buffer.
func completeFrames(b []byte) (size int, frames int64) {
	for len(b)-size >= adtsHeaderSize {
		h := b[size:]
		if h[0] != 0xFF || h[1]&0xF0 != 0xF0 {
			break
		}
		n := int(h[3]&0x03)<<11 | int(h[4])<<3 | int(h[5])>>5
		if n < adtsHeaderSize || n > len(h) {
			break
		}
		size += n
		frames++
	}
	return size, frames
}

// Concat grava em sequência os frames completos de cada pedaço e devolve quantos
// gravou. Concatenar os bytes crus não basta: um pedaço cortado no meio de um frame
// (kill -9, crash da API) faz o remux parar ali, com exit 0 e arquivo curto, e todo
// o áudio dos pedaços seguintes some. Por isso cada pedaço é aparado no último frame
// completo. Um frame inválido encerra aquele pedaço, e o próximo segue normalmente.
func Concat(pieces []Piece, out string) (frames int64, err error) {
	dst, err := os.Create(out)
	if err != nil {
		return 0, fmt.Errorf("recording: criar %s: %w", out, err)
	}
	defer dst.Close()
	for _, p := range pieces {
		b, err := os.ReadFile(p.Path)
		if err != nil {
			return 0, fmt.Errorf("recording: ler pedaço: %w", err)
		}
		size, n := completeFrames(b)
		if _, err := dst.Write(b[:size]); err != nil {
			return 0, fmt.Errorf("recording: copiar pedaço: %w", err)
		}
		frames += n
	}
	return frames, dst.Close()
}
