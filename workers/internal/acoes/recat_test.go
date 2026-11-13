package acoes

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"radiocheck/internal/metrics"
)

func TestRegistrarFalhaRecat(t *testing.T) {
	before := testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("unit_test"))
	RegistrarFalhaRecat("unit_test", errors.New("boom"))
	after := testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("unit_test"))
	require.Equal(t, before+1, after)

	// err == nil não conta.
	RegistrarFalhaRecat("unit_test", nil)
	require.Equal(t, after, testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("unit_test")))
}

// Sem EmSegundoPlano (produção) a recategorização é uma goroutine: a ação volta
// ANTES de ela rodar, e ela roda com context.Background + prazo (não com o
// contexto do pedido, que morre quando a resposta é escrita).
func TestRecatEmSegundoPlano_ProducaoEhGoroutineComPrazoProprio(t *testing.T) {
	s := &Servico{}
	solta := make(chan struct{})
	feito := make(chan time.Duration, 1)

	s.recatEmSegundoPlano("unit_test_goroutine", func(ctx context.Context) error {
		<-solta
		dl, ok := ctx.Deadline()
		require.True(t, ok, "a recategorização tem prazo")
		feito <- time.Until(dl)
		return nil
	})

	// Voltou sem esperar o passo: ele ainda está bloqueado em <-solta.
	select {
	case <-feito:
		t.Fatal("a recategorização rodou no caminho da resposta")
	default:
	}
	close(solta)
	select {
	case restante := <-feito:
		require.Greater(t, restante, 25*time.Second)
		require.LessOrEqual(t, restante, PrazoRecat)
	case <-time.After(5 * time.Second):
		t.Fatal("a recategorização não rodou")
	}
}

// Cada passo tem o PRÓPRIO registro de falha e uma falha não impede o seguinte.
func TestRecatEmSegundoPlano_FalhaDeUmPassoNaoImpedeOSeguinte(t *testing.T) {
	s := &Servico{EmSegundoPlano: sincrono}
	antes := testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("unit_test_passos"))
	var rodou atomic.Int32

	s.recatEmSegundoPlano("unit_test_passos",
		func(context.Context) error { rodou.Add(1); return errors.New("primeiro falha") },
		func(context.Context) error { rodou.Add(1); return nil },
		func(context.Context) error { rodou.Add(1); return errors.New("terceiro falha") },
	)

	require.EqualValues(t, 3, rodou.Load())
	require.Equal(t, antes+2, testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("unit_test_passos")))
}

// O executor padrão (go f()) NÃO deixa um pânico do gatilho derrubar o
// processo: recupera, loga com a pilha e segue. (Se o recover faltasse, o
// pânico numa goroutine mataria o binário de teste inteiro.)
func TestEmSegundoPlano_PadraoRecuperaDePanicELogaComPilha(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	restaura := zap.ReplaceGlobals(zap.New(core))
	defer restaura()

	s := &Servico{} // EmSegundoPlano nil = produção
	s.emSegundoPlano(func() { panic("gatilho quebrou") })

	require.Eventually(t, func() bool { return logs.Len() == 1 }, 5*time.Second, 10*time.Millisecond)
	e := logs.All()[0]
	require.Contains(t, e.Message, "pânico")
	ctx := e.ContextMap()
	require.Equal(t, "gatilho quebrou", ctx["panic"])
	require.NotEmpty(t, ctx["stack"], "a pilha vai no log")

	// E o executor segue funcionando depois do pânico.
	feito := make(chan struct{})
	s.emSegundoPlano(func() { close(feito) })
	select {
	case <-feito:
	case <-time.After(5 * time.Second):
		t.Fatal("o executor parou depois do pânico")
	}
}
