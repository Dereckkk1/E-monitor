package catalog

import (
	"fmt"

	"github.com/google/uuid"
)

/*
A ligação entre um cliente daqui e o cliente do hub dono de um código (spec do
hub 2026-10-22). Quatro estados, e a tela, o botão e a barreira do salvar leem
TODOS desta função — duas cópias da regra divergiriam na primeira mudança.

⚠️ Ponte AUSENTE agora é estado (`faltaLigar`), não "segue". Foi por ali que
Giga, CCGL, Renner e FSJ passaram verdes em 2026-10-22 e ficaram dias sem
chegar ao hub.
*/
const (
	EstadoLigado      = "ligado"
	EstadoFaltaLigar  = "faltaLigar"
	EstadoNoDuplicado = "ligacaoNoDuplicado"
	EstadoBloqueado   = "bloqueado"
)

type ClienteResumo struct {
	ID    uuid.UUID
	Nome  string
	HubID string // "" quando a coluna é NULL
}

/*
FatosDaLigacao é o que o banco daqui sabe, cruzado com o que o hub respondeu.

  - Detentor: OUTRO cliente daqui com `hub_id` = dono do código.
  - ApontadoPeloHub: o cliente daqui cujo id o hub tem gravado, quando não é o
    escolhido e existe.
  - HubSemPonte: o hub não tem id daqui, tem lixo, ou aponta para id que não
    existe aqui. Os três dão no mesmo: falta o lado do hub.
*/
type FatosDaLigacao struct {
	Escolhido          ClienteResumo
	Detentor           *ClienteResumo
	DetentorVazio      bool
	ApontadoPeloHub    *ClienteResumo
	ApontadoVazio      bool
	HubSemPonte        bool
	HubApontaEscolhido bool
}

type Ligacao struct {
	Estado   string `json:"estado"`
	Mensagem string `json:"mensagem"`
}

// EstadoDaLigacao: bloqueio vence duplicado, duplicado vence falta.
func EstadoDaLigacao(f FatosDaLigacao, hubClientID, nomeNoHub string) Ligacao {
	c := f.Escolhido
	if c.HubID != "" && c.HubID != hubClientID {
		return Ligacao{EstadoBloqueado, fmt.Sprintf(
			"%s já está ligado a outro cliente do hub. Confira se escolheu o cliente certo.", c.Nome)}
	}
	if f.Detentor != nil && !f.DetentorVazio {
		return Ligacao{EstadoBloqueado, fmt.Sprintf(
			"A ligação com %s do hub está em %q, que tem cadastros. Fale com um admin antes de mudar.",
			nomeNoHub, f.Detentor.Nome)}
	}
	if f.ApontadoPeloHub != nil && !f.ApontadoVazio {
		return Ligacao{EstadoBloqueado, fmt.Sprintf(
			"Esse código é de %s, que no hub está ligado a %q. Escolha esse cliente acima ou fale com um admin.",
			nomeNoHub, f.ApontadoPeloHub.Nome)}
	}
	if f.Detentor != nil {
		return Ligacao{EstadoNoDuplicado, fmt.Sprintf(
			"A ligação com %s do hub está em %q, que não tem nada cadastrado. Vamos passá-la para %s.",
			nomeNoHub, f.Detentor.Nome, c.Nome)}
	}
	if f.ApontadoPeloHub != nil {
		return Ligacao{EstadoNoDuplicado, fmt.Sprintf(
			"O hub liga %s a %q, que não tem nada cadastrado. Vamos passar a ligação para %s.",
			nomeNoHub, f.ApontadoPeloHub.Nome, c.Nome)}
	}
	if c.HubID != hubClientID || !f.HubApontaEscolhido {
		return Ligacao{EstadoFaltaLigar, fmt.Sprintf(
			"%s ainda não está ligado a %s do hub. Sem isso a campanha não chega lá.", c.Nome, nomeNoHub)}
	}
	return Ligacao{Estado: EstadoLigado}
}
