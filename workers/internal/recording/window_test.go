package recording

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 2026-11-05 12:00 em São Paulo = 15:00 UTC.
var windowNow = time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC)

func TestParseWindow(t *testing.T) {
	cases := []struct {
		name             string
		day, start, end  string
		wantErr          string
		wantFrom, wantTo string // RFC3339 em UTC
	}{
		{name: "horario de SP vira UTC", day: "2026-11-05", start: "14:00", end: "15:00",
			wantFrom: "2026-11-05T17:00:00Z", wantTo: "2026-11-05T18:00:00Z"},
		{name: "exatamente 6h passa", day: "2026-11-05", start: "13:00", end: "19:00",
			wantFrom: "2026-11-05T16:00:00Z", wantTo: "2026-11-05T22:00:00Z"},
		{name: "6h01 nao passa", day: "2026-11-05", start: "13:00", end: "19:01",
			wantErr: "a gravação pode ter no máximo 6 horas"},
		{name: "fim antes do inicio", day: "2026-11-05", start: "23:30", end: "00:30",
			wantErr: "o fim precisa ser depois do início, no mesmo dia"},
		{name: "fim igual ao inicio", day: "2026-11-05", start: "14:00", end: "14:00",
			wantErr: "o fim precisa ser depois do início, no mesmo dia"},
		{name: "inicio ha mais de 1 minuto", day: "2026-11-05", start: "11:58", end: "13:00",
			wantErr: "o início já passou"},
		{name: "inicio ha exatamente 1 minuto comeca agora, sem retroagir", day: "2026-11-05", start: "11:59", end: "13:00",
			wantFrom: "2026-11-05T15:00:00Z", wantTo: "2026-11-05T16:00:00Z"},
		{name: "dia invalido", day: "ontem", start: "14:00", end: "15:00",
			wantErr: "dia ou horário inválido"},
		{name: "hora invalida", day: "2026-11-05", start: "25:00", end: "26:00",
			wantErr: "dia ou horário inválido"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			from, to, err := ParseWindow(c.day, c.start, c.end, windowNow)
			if c.wantErr != "" {
				var ve *ValidationError
				require.True(t, errors.As(err, &ve), "esperava ValidationError, veio %v", err)
				require.Equal(t, c.wantErr, ve.Msg)
				return
			}
			require.NoError(t, err)
			require.Equal(t, c.wantFrom, from.UTC().Format(time.RFC3339))
			require.Equal(t, c.wantTo, to.UTC().Format(time.RFC3339))
		})
	}
}

// Quem escolhe o minuto em curso (form tem resolução de minuto) começa agora.
func TestParseWindow_MinutoCorrenteComecaAgora(t *testing.T) {
	now := windowNow.Add(30 * time.Second) // 12:00:30 em SP
	from, to, err := ParseWindow("2026-11-05", "12:00", "13:00", now)
	require.NoError(t, err)
	require.True(t, from.Equal(now), "from = %s", from)
	require.Equal(t, "2026-11-05T16:00:00Z", to.UTC().Format(time.RFC3339))
}
