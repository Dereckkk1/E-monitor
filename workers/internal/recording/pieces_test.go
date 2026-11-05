package recording

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writePiece(t *testing.T, dir, attempt string, start, end time.Time, content string) {
	t.Helper()
	p := filepath.Join(dir, "a"+attempt+"-"+start.Local().Format("20060102-150405")+pieceExt)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	require.NoError(t, os.Chtimes(p, end, end))
}

// validADTS devolve frames ADTS íntegros (AAC-LC, 44,1 kHz, estéreo), com payload
// pequeno: para os testes só a estrutura do frame importa.
func validADTS(frames int) string {
	const payload = 8
	n := 7 + payload
	frame := []byte{0xFF, 0xF1, 0x50, 0x80 | byte(n>>11), byte(n >> 3), byte(n<<5) | 0x1F, 0xFC}
	frame = append(frame, make([]byte, payload)...)
	return strings.Repeat(string(frame), frames)
}

func TestListPieces(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 11, 5, 20, 0, 0, 0, time.Local)
	writePiece(t, dir, "2", t0.Add(time.Minute), t0.Add(2*time.Minute), "bb")
	writePiece(t, dir, "1", t0, t0.Add(time.Minute), "a")
	writePiece(t, dir, "3", t0.Add(2*time.Minute), t0.Add(3*time.Minute), "") // vazio: ignora
	require.NoError(t, os.WriteFile(filepath.Join(dir, "joined.tmp"), []byte("x"), 0o644))

	got, err := ListPieces(dir)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.True(t, got[0].Start.Equal(t0))
	require.True(t, got[0].End.Equal(t0.Add(time.Minute)))
	require.True(t, got[1].Start.Equal(t0.Add(time.Minute)))
}

// O sidecar segments-cleanup apaga pasta vazia: pasta inexistente é o estado
// normal de uma gravação com o stream fora, não um erro.
func TestListPieces_PastaInexistente(t *testing.T) {
	got, err := ListPieces(filepath.Join(t.TempDir(), "nao-existe"))
	require.NoError(t, err)
	require.Empty(t, got)
	require.Zero(t, DirBytes(filepath.Join(t.TempDir(), "nao-existe")))
}

func TestComputeGaps(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)
	at := func(n float64) time.Time { return t0.Add(time.Duration(n * float64(time.Minute))) }
	p := func(a, b float64) Piece { return Piece{Start: at(a), End: at(b), Size: 1} }

	t.Run("sem buraco", func(t *testing.T) {
		got := ComputeGaps([]Piece{p(0, 1), p(1, 2), p(2, 3)}, at(0), at(3), 30*time.Second)
		require.Empty(t, got)
		require.NotNil(t, got, "precisa serializar como [] e não null")
	})
	t.Run("buraco no meio", func(t *testing.T) {
		got := ComputeGaps([]Piece{p(0, 1), p(4, 5)}, at(0), at(5), 30*time.Second)
		require.Equal(t, []Gap{{From: at(1), To: at(4)}}, got)
	})
	t.Run("ffmpeg morreu no meio do pedaco: usa o mtime, nao o minuto cheio", func(t *testing.T) {
		got := ComputeGaps([]Piece{p(0, 0.1), p(2.5, 3)}, at(0), at(3), 30*time.Second)
		require.Equal(t, []Gap{{From: at(0.1), To: at(2.5)}}, got)
	})
	t.Run("comeco e fim faltando", func(t *testing.T) {
		got := ComputeGaps([]Piece{p(2, 3)}, at(0), at(5), 30*time.Second)
		require.Equal(t, []Gap{{From: at(0), To: at(2)}, {From: at(3), To: at(5)}}, got)
	})
	t.Run("oscilacao menor que o minimo nao vira buraco", func(t *testing.T) {
		got := ComputeGaps([]Piece{p(0, 1), p(1.2, 2)}, at(0), at(2), 30*time.Second)
		require.Empty(t, got)
	})
	t.Run("sem pedaco nenhum: a janela inteira", func(t *testing.T) {
		got := ComputeGaps(nil, at(0), at(5), 30*time.Second)
		require.Equal(t, []Gap{{From: at(0), To: at(5)}}, got)
	})
	t.Run("pre-roll antes da janela nao gera buraco", func(t *testing.T) {
		got := ComputeGaps([]Piece{p(-0.3, 1), p(1, 2)}, at(0), at(2), 30*time.Second)
		require.Empty(t, got)
	})
	t.Run("pedaco que comeca depois do fim: um unico buraco ate to", func(t *testing.T) {
		got := ComputeGaps([]Piece{p(6, 7)}, at(0), at(5), 30*time.Second)
		require.Equal(t, []Gap{{From: at(0), To: at(5)}}, got)
	})
	t.Run("to antes de from: vazio e nao nil", func(t *testing.T) {
		got := ComputeGaps(nil, at(5), at(0), 30*time.Second)
		require.Empty(t, got)
		require.NotNil(t, got)
	})
}
func TestFinalStatus(t *testing.T) {
	gap := []Gap{{From: time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 5, 20, 7, 0, 0, time.UTC)}}
	cases := []struct {
		name             string
		hasAudio, cancel bool
		gaps             []Gap
		want             string
	}{
		{"cancelada com audio", true, true, gap, StatusCancelada},
		{"cancelada sem audio", false, true, nil, StatusCancelada},
		{"sem audio", false, false, gap, StatusFalhou},
		{"com audio sem buraco", true, false, []Gap{}, StatusConcluida},
		{"com audio e um buraco", true, false, gap, StatusParcial},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, FinalStatus(c.hasAudio, c.cancel, c.gaps))
		})
	}
}

func TestCompleteFrames(t *testing.T) {
	frame := len(validADTS(1))
	badLength := []byte(validADTS(1))
	badLength[3], badLength[4], badLength[5] = badLength[3]&^0x03, 0, 0 // comprimento 0
	tooLong := []byte(validADTS(1))
	tooLong[3] |= 0x03 // comprimento maior que o que sobrou no buffer
	noSync := []byte(validADTS(1))
	noSync[0] = 0x00

	cases := []struct {
		name   string
		in     []byte
		size   int
		frames int64
	}{
		{"buffer vazio", nil, 0, 0},
		{"stream íntegro", []byte(validADTS(3)), 3 * frame, 3},
		{"cortado no meio do header", []byte(validADTS(3))[:2*frame+4], 2 * frame, 2},
		{"cortado no meio do payload", []byte(validADTS(3))[:2*frame+9], 2 * frame, 2},
		{"menor que um header", []byte(validADTS(1))[:3], 0, 0},
		{"comprimento inválido", append([]byte(validADTS(2)), badLength...), 2 * frame, 2},
		{"comprimento maior que o restante", append([]byte(validADTS(2)), tooLong...), 2 * frame, 2},
		{"sem sync", append([]byte(validADTS(2)), noSync...), 2 * frame, 2},
		{"frame íntegro depois do inválido não conta", append(append([]byte(validADTS(1)), noSync...), validADTS(1)...), frame, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			size, frames := completeFrames(c.in)
			require.Equal(t, c.size, size)
			require.Equal(t, c.frames, frames)
		})
	}
}

func TestConcat(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a"+pieceExt)
	b := filepath.Join(dir, "b"+pieceExt)
	require.NoError(t, os.WriteFile(a, []byte(validADTS(3)), 0o644))
	require.NoError(t, os.WriteFile(b, []byte(validADTS(2)), 0o644))
	out := filepath.Join(dir, "joined.tmp")

	frames, err := Concat([]Piece{{Path: a}, {Path: b}}, out)
	require.NoError(t, err)
	require.EqualValues(t, 5, frames)

	got, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Equal(t, validADTS(5), string(got))
}

// Um pedaço cortado no meio de um frame (kill -9) faz o remux parar ali com exit 0
// e descartar todos os pedaços seguintes, sem aviso.
func TestConcat_ApagaFrameCortadoESegueParaOProximoPedaco(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a"+pieceExt)
	b := filepath.Join(dir, "b"+pieceExt)
	cortado := validADTS(3)
	require.NoError(t, os.WriteFile(a, []byte(cortado[:len(cortado)-5]), 0o644))
	require.NoError(t, os.WriteFile(b, []byte(validADTS(2)), 0o644))
	out := filepath.Join(dir, "joined.tmp")

	frames, err := Concat([]Piece{{Path: a}, {Path: b}}, out)
	require.NoError(t, err)
	require.EqualValues(t, 4, frames)

	got, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Equal(t, validADTS(4), string(got))
}

func TestConcat_SemFrameCompleto(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a"+pieceExt)
	require.NoError(t, os.WriteFile(a, []byte("xx"), 0o644))

	frames, err := Concat([]Piece{{Path: a}}, filepath.Join(dir, "joined.tmp"))
	require.NoError(t, err)
	require.Zero(t, frames)
}

func TestConcat_PedacoInexistente(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "joined.tmp")
	_, err := Concat([]Piece{{Path: filepath.Join(dir, "nao-existe"+pieceExt)}}, out)
	require.Error(t, err)
}
