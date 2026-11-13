package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const hubDono = "6ad27357e7da87aa5b274b11"

func escolhido(hubID string) ClienteResumo {
	return ClienteResumo{ID: uuid.MustParse("b2adf6cd-0fc7-4580-b77a-ab60be742543"), Nome: "LOJAS RENNER", HubID: hubID}
}

func outroCliente(nome string) *ClienteResumo {
	return &ClienteResumo{ID: uuid.New(), Nome: nome}
}

func TestEstado_Ligado(t *testing.T) {
	l := EstadoDaLigacao(FatosDaLigacao{Escolhido: escolhido(hubDono), HubApontaEscolhido: true}, hubDono, "Lojas Renner")
	require.Equal(t, EstadoLigado, l.Estado)
}

// CCGL e Renner em 2026-10-22: nenhum dos dois lados ligado.
func TestEstado_FaltaLigar_NenhumLado(t *testing.T) {
	l := EstadoDaLigacao(FatosDaLigacao{Escolhido: escolhido(""), HubSemPonte: true}, hubDono, "Lojas Renner")
	require.Equal(t, EstadoFaltaLigar, l.Estado)
	require.Contains(t, l.Mensagem, "LOJAS RENNER")
	require.Contains(t, l.Mensagem, "Lojas Renner")
}

// Só o lado do E-monitor ligado: o coletor do hub continuaria recusando.
func TestEstado_FaltaLigar_SoOLadoDoHub(t *testing.T) {
	l := EstadoDaLigacao(FatosDaLigacao{Escolhido: escolhido(hubDono), HubSemPonte: true}, hubDono, "Lojas Renner")
	require.Equal(t, EstadoFaltaLigar, l.Estado)
}

// Só o lado do hub ligado: o E-monitor responderia 403 ao hub.
func TestEstado_FaltaLigar_SoOLadoLocal(t *testing.T) {
	l := EstadoDaLigacao(FatosDaLigacao{Escolhido: escolhido(""), HubApontaEscolhido: true}, hubDono, "Lojas Renner")
	require.Equal(t, EstadoFaltaLigar, l.Estado)
}

// Giga e FSJ em 2026-10-22.
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
	f := FatosDaLigacao{Escolhido: escolhido(""), ApontadoPeloHub: outroCliente("Renner antiga"), ApontadoVazio: true}
	require.Equal(t, EstadoNoDuplicado, EstadoDaLigacao(f, hubDono, "Lojas Renner").Estado)
}

// O antigo "outroCliente": o hub aponta para um cliente daqui que está em uso.
func TestEstado_HubApontaParaEmUso_Bloqueia(t *testing.T) {
	f := FatosDaLigacao{Escolhido: escolhido(""), ApontadoPeloHub: outroCliente("Pilecco"), ApontadoVazio: false}
	l := EstadoDaLigacao(f, hubDono, "Pilecco Nobre")
	require.Equal(t, EstadoBloqueado, l.Estado)
	require.Contains(t, l.Mensagem, "Pilecco")
}

func TestEstado_EscolhidoJaEhDeOutroClienteDoHub_Bloqueia(t *testing.T) {
	l := EstadoDaLigacao(FatosDaLigacao{Escolhido: escolhido("outro-hub-id"), HubApontaEscolhido: true}, hubDono, "Lojas Renner")
	require.Equal(t, EstadoBloqueado, l.Estado)
	require.Contains(t, l.Mensagem, "LOJAS RENNER")
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

func TestEstado_HubClientIDVazio_FaltaLigar(t *testing.T) {
	l := EstadoDaLigacao(FatosDaLigacao{Escolhido: escolhido("qualquer")}, "", "")
	require.Equal(t, EstadoFaltaLigar, l.Estado)
}

// ---------- CNPJ divergente (decisão de 2026-10-22) ----------

func comCNPJ(c ClienteResumo, cnpj string) ClienteResumo { c.CNPJ = cnpj; return c }

func TestEstado_CNPJDivergenteBloqueia(t *testing.T) {
	f := FatosDaLigacao{Escolhido: comCNPJ(escolhido(""), "07714104000107"), CNPJNoHub: "03.984.660/0001-81", HubSemPonte: true}
	l := EstadoDaLigacao(f, hubDono, "Lojas Renner")
	require.Equal(t, EstadoBloqueado, l.Estado)
	require.Contains(t, l.Mensagem, "07.714.104/0001-07")
	require.Contains(t, l.Mensagem, "03.984.660/0001-81")
	require.Contains(t, l.Mensagem, "LOJAS RENNER")
	require.Contains(t, l.Mensagem, "Lojas Renner")
}

// Renner foi consertada à mão com CNPJs diferentes: ligação completa não se desfaz.
func TestEstado_CNPJDivergenteJaLigadoContinuaLigado(t *testing.T) {
	f := FatosDaLigacao{Escolhido: comCNPJ(escolhido(hubDono), "07714104000107"), CNPJNoHub: "03.984.660/0001-81", HubApontaEscolhido: true}
	l := EstadoDaLigacao(f, hubDono, "Lojas Renner")
	require.Equal(t, EstadoLigado, l.Estado)
}

// CNPJ ausente ou incompleto em qualquer lado: não compara, cai no estado de sempre.
func TestEstado_CNPJIncompletoNaoCompara(t *testing.T) {
	casos := [][2]string{
		{"", "03.984.660/0001-81"},
		{"07714104000107", ""},
		{"0771410400010", "03.984.660/0001-81"},
		{"07714104000107", "03.984.660/0001-8"},
	}
	for _, c := range casos {
		f := FatosDaLigacao{Escolhido: comCNPJ(escolhido(""), c[0]), CNPJNoHub: c[1], HubSemPonte: true}
		l := EstadoDaLigacao(f, hubDono, "Lojas Renner")
		require.Equal(t, EstadoFaltaLigar, l.Estado, "%q x %q", c[0], c[1])
	}
}

// Máscara de um lado e só dígitos do outro, mesmos dígitos: é o mesmo CNPJ.
func TestEstado_CNPJComMascaraIgualNaoBloqueia(t *testing.T) {
	f := FatosDaLigacao{Escolhido: comCNPJ(escolhido(""), "07714104000107"), CNPJNoHub: "07.714.104/0001-07", HubSemPonte: true}
	l := EstadoDaLigacao(f, hubDono, "Lojas Renner")
	require.Equal(t, EstadoFaltaLigar, l.Estado)
}
