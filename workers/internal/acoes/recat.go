package acoes

import (
	"context"
	"time"

	"go.uber.org/zap"

	"radiocheck/internal/metrics"
)

// PrazoRecat é o teto de uma recategorização best-effort. Ela roda com
// context.Background (o contexto do pedido morre quando a resposta é escrita) e
// este prazo — o mesmo de sempre dos handlers de regra e de material.
const PrazoRecat = 30 * time.Second

// RegistrarFalhaRecat torna visível a falha de uma recategorização best-effort
// (disparada pelas ações de regra, override e tipo de material). Antes o erro
// era engolido (`_ =`) e a categoria ficava velha em silêncio — spec 2026-07-14
// §3-T2. O reconciler (projrecon) cura o dado; isto aqui denuncia a causa.
// Usa zap.L() (global) porque as ações não carregam logger próprio.
//
// A métrica (metrics.RecategorizeFailures) é registrada UMA vez, em
// internal/metrics — registrar de novo daqui seria panic no init (CLAUDE.md 6.5).
// Origens em uso: rule_create, rule_update, rule_delete, override_upsert,
// override_delete, material_type_change.
func RegistrarFalhaRecat(origem string, err error) {
	if err == nil {
		return
	}
	metrics.RecategorizeFailures.WithLabelValues(origem).Inc()
	zap.L().Error("recategorização best-effort falhou",
		zap.String("origin", origem), zap.Error(err))
}

// recatEmSegundoPlano roda as recategorizações fora do caminho da resposta, em
// sequência, cada uma com o PRÓPRIO prazo de PrazoRecat e o próprio registro de
// falha (uma que falha não impede a seguinte). Em produção é uma goroutine
// (Servico.EmSegundoPlano nil); os testes injetam um executor que espera.
func (s *Servico) recatEmSegundoPlano(origem string, passos ...func(ctx context.Context) error) {
	s.emSegundoPlano(func() {
		for _, passo := range passos {
			func() {
				ctx, cancel := context.WithTimeout(context.Background(), PrazoRecat)
				defer cancel()
				RegistrarFalhaRecat(origem, passo(ctx))
			}()
		}
	})
}
