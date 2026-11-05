package recording

import "time"

// saoPaulo cai no fuso fixo se o tzdata faltar: o Brasil não tem horário de
// verão desde 2019, e derrubar o boot da API por isso seria pior (regra 6.5).
var saoPaulo = func() *time.Location {
	if loc, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
		return loc
	}
	return time.FixedZone("America/Sao_Paulo", -3*60*60)
}()

const windowLayout = "2006-01-02 15:04"

// ParseWindow transforma o que veio do formulário no intervalo a gravar.
//
// A folga de 1 minuto existe porque o form tem resolução de minuto: quem quer
// gravar agora escolhe o minuto em curso, que já começou. Nesse caso `from` é
// puxado para `now`, para a janela não nascer com um buraco que nunca existiu.
func ParseWindow(day, start, end string, now time.Time) (time.Time, time.Time, error) {
	from, err := time.ParseInLocation(windowLayout, day+" "+start, saoPaulo)
	if err != nil {
		return time.Time{}, time.Time{}, &ValidationError{"dia ou horário inválido"}
	}
	to, err := time.ParseInLocation(windowLayout, day+" "+end, saoPaulo)
	if err != nil {
		return time.Time{}, time.Time{}, &ValidationError{"dia ou horário inválido"}
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, &ValidationError{"o fim precisa ser depois do início, no mesmo dia"}
	}
	if to.Sub(from) > MaxDuration {
		return time.Time{}, time.Time{}, &ValidationError{"a gravação pode ter no máximo 6 horas"}
	}
	if from.Before(now.Add(-time.Minute)) {
		return time.Time{}, time.Time{}, &ValidationError{"o início já passou"}
	}
	if from.Before(now) {
		from = now
	}
	return from, to, nil
}
