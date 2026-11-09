package recording

import (
	"os"
	"path/filepath"
	"strconv"
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
	cases := []struct {
		name                      string
		hasAudio, cancel, partial bool
		want                      string
	}{
		{"cancelada com audio", true, true, true, StatusCancelada},
		{"cancelada sem audio", false, true, false, StatusCancelada},
		{"sem audio", false, false, true, StatusFalhou},
		{"com audio inteiro", true, false, false, StatusConcluida},
		{"com audio faltando", true, false, true, StatusParcial},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, FinalStatus(c.hasAudio, c.cancel, c.partial))
		})
	}
}

func TestAssessCoverage(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)
	at := func(n float64) time.Time { return t0.Add(time.Duration(n * float64(time.Second))) }
	p := func(a, b float64) Piece { return Piece{Start: at(a), End: at(b), Size: 1} }
	secs := func(n float64) time.Duration { return time.Duration(n * float64(time.Second)) }
	const minGap, detailGap = 30 * time.Second, 5 * time.Second
	listed := func(pieces []Piece, to float64) []Gap { return ComputeGaps(pieces, at(0), at(to), minGap) }

	t.Run("flap de 25s no ar e 25s fora vira parcial e lista os trechos", func(t *testing.T) {
		// Pedaços medidos com ffmpeg real: nenhum intervalo chega a 30s, e sobraram
		// 117s de áudio em 210s de janela.
		pieces := []Piece{p(-0.2, 25.1), p(49.8, 52.9), p(52.8, 75.1), p(99.8, 112.8), p(112.8, 125),
			p(149.8, 168.7), p(167.8, 171.8), p(172.8, 175), p(199.8, 210)}
		require.Empty(t, listed(pieces, 210), "é este o furo: um a um, nenhum vira buraco")

		gaps, partial := AssessCoverage(pieces, at(0), at(210), listed(pieces, 210), secs(117), minGap, detailGap)

		require.True(t, partial)
		require.Equal(t, []Gap{{at(25.1), at(49.8)}, {at(75.1), at(99.8)}, {at(125), at(149.8)}, {at(175), at(199.8)}}, gaps)
	})
	t.Run("rajada de HLS sem faltar audio continua concluida", func(t *testing.T) {
		// O pedaço do minuto recebe menos frames que o relógio e o seguinte mais.
		// Somar os intervalos daria 10s por minuto de falso buraco; o total não sofre.
		var pieces []Piece
		for m := 0.0; m < 600; m += 120 {
			pieces = append(pieces, p(m, m+50), p(m+60, m+120))
		}
		require.Empty(t, listed(pieces, 600))

		gaps, partial := AssessCoverage(pieces, at(0), at(600), listed(pieces, 600), secs(600), minGap, detailGap)

		require.False(t, partial)
		require.Empty(t, gaps)
		require.NotNil(t, gaps)
	})
	t.Run("pre-roll nao mascara o audio que falta na janela", func(t *testing.T) {
		// 20s antes da janela + 2 furos de 20s: covered = 580 de 600, mas só 560 são
		// da janela. Sem descontar o pré-roll faltariam 20s, e sairia `concluida`.
		pieces := []Piece{p(-20, 280), p(300, 320), p(340, 600)}

		gaps, partial := AssessCoverage(pieces, at(0), at(600), listed(pieces, 600), secs(580), minGap, detailGap)

		require.True(t, partial)
		require.Equal(t, []Gap{{at(280), at(300)}, {at(320), at(340)}}, gaps)
	})
	t.Run("buraco unico de 20s cabe na tolerancia", func(t *testing.T) {
		pieces := []Piece{p(0, 300), p(320, 600)}

		gaps, partial := AssessCoverage(pieces, at(0), at(600), listed(pieces, 600), secs(580), minGap, detailGap)

		require.False(t, partial)
		require.Empty(t, gaps)
		require.NotNil(t, gaps)
	})
	t.Run("buraco de 31s mais outro de 20s lista so o de 31s", func(t *testing.T) {
		pieces := []Piece{p(0, 100), p(131, 300), p(320, 600)}

		gaps, partial := AssessCoverage(pieces, at(0), at(600), listed(pieces, 600), secs(549), minGap, detailGap)

		require.True(t, partial)
		require.Equal(t, []Gap{{at(100), at(131)}}, gaps)
	})
	t.Run("nao listado exatamente no limite vira parcial", func(t *testing.T) {
		pieces := []Piece{p(0, 300), p(315, 400), p(415, 600)}

		gaps, partial := AssessCoverage(pieces, at(0), at(600), listed(pieces, 600), secs(570), minGap, detailGap)

		require.True(t, partial)
		require.Len(t, gaps, 2)
	})
	t.Run("nao listado a um segundo do limite fica concluida", func(t *testing.T) {
		pieces := []Piece{p(0, 300), p(315, 400), p(414, 600)}

		_, partial := AssessCoverage(pieces, at(0), at(600), listed(pieces, 600), secs(571), minGap, detailGap)

		require.False(t, partial)
	})
	t.Run("muitos furos curtos somam e deixam a lista vazia", func(t *testing.T) {
		var pieces []Piece
		for s := 0.0; s < 600; s += 60 {
			pieces = append(pieces, p(s, s+56))
		}

		gaps, partial := AssessCoverage(pieces, at(0), at(600), listed(pieces, 600), secs(560), minGap, detailGap)

		require.True(t, partial, "faltam 40s")
		require.Empty(t, gaps, "nenhum furo chega a 5s")
		require.NotNil(t, gaps, "precisa serializar como [] e não null")
	})
	t.Run("audio a mais que a janela nao gera falta", func(t *testing.T) {
		pieces := []Piece{p(0, 600)}

		gaps, partial := AssessCoverage(pieces, at(0), at(600), listed(pieces, 600), secs(640), minGap, detailGap)

		require.False(t, partial)
		require.Empty(t, gaps)
	})
	t.Run("buraco listado basta para ser parcial", func(t *testing.T) {
		pieces := []Piece{p(0, 200), p(300, 600)}

		gaps, partial := AssessCoverage(pieces, at(0), at(600), listed(pieces, 600), secs(500), minGap, detailGap)

		require.True(t, partial)
		require.Equal(t, []Gap{{at(200), at(300)}}, gaps)
	})
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

// Sobra de corte é sempre menor que um frame. Mais que isso é lixo no meio do
// pedaço: aparar calado perderia o áudio depois dele.
func TestConcat_SobraNaoReconhecida(t *testing.T) {
	cases := []struct {
		name  string
		sobra int
		ok    bool
	}{
		{"no limite do maior frame passa", maxADTSFrame, true},
		{"um byte acima falha", maxADTSFrame + 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			a := filepath.Join(dir, "a"+pieceExt)
			require.NoError(t, os.WriteFile(a, []byte(validADTS(2)+strings.Repeat("x", c.sobra)), 0o644))

			frames, err := Concat([]Piece{{Path: a}}, filepath.Join(dir, "joined.tmp"))

			if c.ok {
				require.NoError(t, err)
				require.EqualValues(t, 2, frames)
				return
			}
			require.Error(t, err)
			require.Zero(t, frames)
			require.ErrorContains(t, err, filepath.Base(a))
			require.ErrorContains(t, err, strconv.Itoa(c.sobra)+" bytes")
		})
	}
}

func TestConcat_LixoNoMeioDoPedacoInterrompe(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a"+pieceExt)
	b := filepath.Join(dir, "b"+pieceExt)
	require.NoError(t, os.WriteFile(a, []byte(validADTS(3)), 0o644))
	require.NoError(t, os.WriteFile(b, []byte(validADTS(3)+strings.Repeat("x", 9000)+validADTS(3)), 0o644))

	_, err := Concat([]Piece{{Path: a}, {Path: b}}, filepath.Join(dir, "joined.tmp"))

	require.ErrorContains(t, err, filepath.Base(b))
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
