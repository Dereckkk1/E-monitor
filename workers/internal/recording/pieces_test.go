package recording

import (
	"os"
	"path/filepath"
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

func TestConcat(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a"+pieceExt)
	b := filepath.Join(dir, "b"+pieceExt)
	require.NoError(t, os.WriteFile(a, []byte("aaa"), 0o644))
	require.NoError(t, os.WriteFile(b, []byte("bb"), 0o644))
	out := filepath.Join(dir, "joined.tmp")

	require.NoError(t, Concat([]Piece{{Path: a}, {Path: b}}, out))

	got, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Equal(t, "aaabb", string(got))
}

func TestConcat_PedacoInexistente(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "joined.tmp")
	err := Concat([]Piece{{Path: filepath.Join(dir, "nao-existe"+pieceExt)}}, out)
	require.Error(t, err)
}
