package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const hubDono = "6ad27357e7da87aa5b274b11"

func escolhido(hubID string) ClienteResumo {
	return ClienteResumo{ID: uuid.MustParse("b2adf6cd-0fc7-4580-b77a-ab60be742543"), Nome: "CLIENTE BRAVO", HubID: hubID}
}

func outroCliente(nome string) *ClienteResumo {
	return &ClienteResumo{ID: uuid.New(), Nome: nome}
}

func TestEstado_Ligado(t *testing.T) {
	l := EstadoDaLigacao(FatosDaLigacao{Escolhido: escolhido(hubDono), HubApontaEscolhido: true}, hubDono, "Cliente Bravo")
	require.Equal(t, EstadoLigado, l.Estado)
}

// CCGL e Cliente Bravo em 2026-10-22: nenhum dos dois lados ligado.
func TestEstado_FaltaLigar_NenhumLado(t *testing.T) {
	l := EstadoDaLigacao(FatosDaLigacao{Escolhido: escolhido(""), HubSemPonte: true}, hubDono, "Cliente Bravo")
	require.Equal(t, EstadoFaltaLigar, l.Estado)
	require.Contains(t, l.Mensagem, "CLIENTE BRAVO")
	require.Contains(t, l.Mensagem, "Cliente Bravo")
}

// Só o lado do E-monitor ligado: o coletor do hub continuaria recusando.
func TestEstado_FaltaLigar_SoOLadoDoHub(t *testing.T) {
	l := EstadoDaLigacao(FatosDaLigacao{Escolhido: escolhido(hubDono), HubSemPonte: true}, hubDono, "Cliente Bravo")
	require.Equal(t, EstadoFaltaLigar, l.Estado)
}

// Só o lado do hub ligado: o E-monitor responderia 403 ao hub.
func TestEstado_FaltaLigar_SoOLadoLocal(t *testing.T) {
	l := EstadoDaLigacao(FatosDaLigacao{Escolhido: escolhido(""), HubApontaEscolhido: true}, hubDono, "Cliente Bravo")
	require.Equal(t, EstadoFaltaLigar, l.Estado)
}

// Giga e CLIENTE GOLF em 2026-10-22.
func TestEstado_DetentorVazio_EhDuplicado(t *testing.T) {
	f := FatosDaLigacao{Escolhido: escolhido(""), Detentor: outroCliente("Giga + Internet"), DetentorVazio: true, HubApontaEscolhido: true}
	l := EstadoDaLigacao(f, hubDono, "Giga Mais Fibra")
	require.Equal(t, EstadoNoDuplicado, l.Estado)
	require.Contains(t, l.Mensagem, "Giga + Internet")
}

func TestEstado_DetentorEmUso_Bloqueia(t *testing.T) {
	f := FatosDaLigacao{Escolhido: escolhido(""), Detentor: outroCliente("Giga + Internet"), DetentorVazio: false, HubApontaEscolhido: true}
	l := EstadoDaLigacao(f, hubDono, "Giga Mais Fibra")
	require.Equal(t, EstadoBloqueado, l.Estado)
	require.Contains(t, l.Mensagem, "Giga + Internet")
}

func TestEstado_HubApontaParaVazio_EhDuplicado(t *testing.T) {
	f := FatosDaLigacao{Escolhido: escolhido(""), ApontadoPeloHub: outroCliente("Cliente Bravo antiga"), ApontadoVazio: true}
	require.Equal(t, EstadoNoDuplicado, EstadoDaLigacao(f, hubDono, "Cliente Bravo").Estado)
}

// O antigo "outroCliente": o hub aponta para um cliente daqui que está em uso.
func TestEstado_HubApontaParaEmUso_Bloqueia(t *testing.T) {
	f := FatosDaLigacao{Escolhido: escolhido(""), ApontadoPeloHub: outroCliente("Cliente Juliett"), ApontadoVazio: false}
	l := EstadoDaLigacao(f, hubDono, "Cliente Juliett Nobre")
	require.Equal(t, EstadoBloqueado, l.Estado)
	require.Contains(t, l.Mensagem, "Cliente Juliett")
}

func TestEstado_EscolhidoJaEhDeOutroClienteDoHub_Bloqueia(t *testing.T) {
	l := EstadoDaLigacao(FatosDaLigacao{Escolhido: escolhido("outro-hub-id"), HubApontaEscolhido: true}, hubDono, "Cliente Bravo")
	require.Equal(t, EstadoBloqueado, l.Estado)
	require.Contains(t, l.Mensagem, "CLIENTE BRAVO")
}

// Bloqueio vence duplicado: com um lado em uso, o botão não pode aparecer.
func TestEstado_BloqueioVenceDuplicado(t *testing.T) {
	f := FatosDaLigacao{
		Escolhido: escolhido(""),
		Detentor:  outroCliente("Vazio"), DetentorVazio: true,
		ApontadoPeloHub: outroCliente("Em uso"), ApontadoVazio: false,
	}
	require.Equal(t, EstadoBloqueado, EstadoDaLigacao(f, hubDono, "X").Estado)
}
