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

// Piece é um arquivo de áudio em disco. Start vem do nome; End vem do mtime, que é
// o último instante em que o ffmpeg escreveu nele. O mtime pode passar do áudio:
// para medir buraco, passe os pedaços por TrimToAudio.
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

// TrimToAudio limita o fim de cada pedaço ao áudio que ele tem. Quando o stream cai, o
// ffmpeg ainda espera a reconexão antes de fechar o arquivo, e o mtime anda ~12s sem
// áudio nenhum: medido em ar real, uma queda de 32s saía `concluida`, com um buraco de
// 34s no arquivo. O mtime continua valendo como teto, porque o burst do servidor ao
// conectar entrega mais áudio que o tempo de relógio do pedaço.
func TrimToAudio(pieces []Piece) error {
	for i := range pieces {
		b, err := os.ReadFile(pieces[i].Path)
		if err != nil {
			return fmt.Errorf("recording: ler pedaço: %w", err)
		}
		_, frames := completeFrames(b)
		if end := pieces[i].Start.Add(framesDuration(frames)); end.Before(pieces[i].End) {
			pieces[i].End = end
		}
	}
	return nil
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

// DetailGap é o menor intervalo listado quando já se sabe que falta áudio. Só vale
// nesse caso (ver AssessCoverage). Mais baixo daria falso em HLS, em que os frames
// chegam em rajadas e a fronteira do pedaço abre intervalos de poucos segundos sem
// faltar áudio nenhum.
const DetailGap = 5 * time.Second

// AssessCoverage decide se falta áudio na janela [from, to] e devolve a lista final
// de buracos. gaps é o ComputeGaps com minGap, e covered, a duração do arquivo.
//
// Olhar só a lista não basta: cada intervalo abaixo de minGap é descartado sozinho, e
// um stream oscilando (25s no ar, 25s fora) perdeu 45% do áudio sem nenhum buraco
// listado. Somar os intervalos também não serve: em HLS o pedaço de um minuto pode ter
// menos frames que o relógio e o seguinte mais, e a soma de falsos buracos daria
// parcial em gravação perfeita. Por isso a decisão vem do total de áudio, que não
// sofre com a fronteira do pedaço.
//
// A tolerância é de minGap no total do que não foi listado: um buraco único de 20s
// continua concluída. Acima disso a lista é refeita com detailGap. Ela pode continuar
// vazia, com muitos furos abaixo de detailGap: a gravação é parcial mesmo assim, e o
// percentual é o que mostra a falta.
func AssessCoverage(pieces []Piece, from, to time.Time, gaps []Gap, covered, minGap, detailGap time.Duration) ([]Gap, bool) {
	inWindow := covered
	if len(pieces) > 0 && pieces[0].Start.Before(from) {
		inWindow -= from.Sub(pieces[0].Start) // pré-roll: áudio de antes da janela
	}
	unlisted := to.Sub(from) - inWindow
	for _, g := range gaps {
		unlisted -= g.To.Sub(g.From)
	}
	if unlisted < minGap {
		return gaps, len(gaps) > 0
	}
	if detailed := ComputeGaps(pieces, from, to, detailGap); len(detailed) > 0 {
		gaps = detailed
	}
	return gaps, true
}

// FinalStatus decide o status de encerramento. A regra é binária e não usa o
// percentual: o áudio é usado como prova, então faltar áudio (partial, de
// AssessCoverage) torna a gravação parcial, por menor que seja a fração da janela.
func FinalStatus(hasAudio, canceled, partial bool) string {
	if canceled {
		return StatusCancelada
	}
	if !hasAudio {
		return StatusFalhou
	}
	if partial {
		return StatusParcial
	}
	return StatusConcluida
}

// maxADTSFrame é o maior frame ADTS possível: o comprimento tem 13 bits.
const maxADTSFrame = 8191

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
// completo. Sobra de corte é sempre menor que um frame: mais que maxADTSFrame bytes
// depois do último frame é lixo no meio do pedaço ou formato não reconhecido, e pode
// haver áudio depois dele. Aí devolve erro em vez de aparar, para os pedaços ficarem.
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
		if rest := len(b) - size; rest > maxADTSFrame {
			return 0, fmt.Errorf("recording: pedaço %s: %d bytes não reconhecidos", filepath.Base(p.Path), rest)
		}
		if _, err := dst.Write(b[:size]); err != nil {
			return 0, fmt.Errorf("recording: copiar pedaço: %w", err)
		}
		frames += n
	}
	return frames, dst.Close()
}
