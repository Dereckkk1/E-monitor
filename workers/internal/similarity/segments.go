package similarity

// mergeRegionsSec funde os ranges casados de um eixo (own OU other) na união
// mínima, clampa em [0, totalFrames] e converte pra segundos. É o que a
// timeline desenha em cada faixa.
//
// Por que por-eixo (e não pares own↔other): o offset estimado por janela é
// ruidoso (as variantes de broadcast-sim com ruído geram alinhamentos
// concorrentes), então parear own↔other fragmenta material idêntico em vários
// segmentos sobrepostos. Como a timeline não desenha conectores, basta a união
// dos trechos casados em cada eixo — limpa e consistente com o cov.
func mergeRegionsSec(ranges []frameRange, totalFrames int) [][2]float64 {
	out := make([][2]float64, 0)
	for _, r := range mergeRanges(ranges) {
		from, to := r.from, r.until
		if to <= 0 || int(from) >= totalFrames {
			continue // fora dos limites do material
		}
		if from < 0 {
			from = 0
		}
		if int(to) > totalFrames {
			to = int32(totalFrames)
		}
		out = append(out, [2]float64{
			float64(from) * framesToSec,
			float64(to) * framesToSec,
		})
	}
	return out
}
