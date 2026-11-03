package acoes

import (
	"crypto/rand"
	"errors"
	"math/big"
	"unicode"
)

// Senha inicial gerada pelo servidor (Fatia 4, F4-11: segredo nunca passa pelo
// chat). O MCP não recebe senha: criar_usuario pede ao servidor uma aleatória e
// a entrega à pessoa pelo email de boas-vindas. A rota HTTP continua recebendo a
// senha do admin; nada aqui muda o CriarUsuario.

const (
	// TamanhoSenhaGerada: 24 caracteres de um alfabeto de 57 símbolos são ~140
	// bits de entropia, e ficam bem acima do piso (TamanhoMinimoSenha).
	TamanhoSenhaGerada = 24

	// alfabetoSenha: letras e dígitos sem os que se confundem na tela (0/O, 1/l/I):
	// a pessoa copia a senha de uma página ou de um email. Sem símbolos de
	// pontuação para a cópia não levar um ponto final junto.
	alfabetoSenha = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

	// tentativasDeSenha: a chance de um sorteio de 24 caracteres vir sem uma das
	// três classes (maiúscula, minúscula, dígito) é de ~3%; 64 falhas seguidas
	// não acontecem, e o teto só impede um laço sem fim se o gerador quebrar.
	tentativasDeSenha = 64
)

// SenhaAleatoria sorteia uma senha de TamanhoSenhaGerada caracteres do
// alfabetoSenha com crypto/rand, sem viés (rand.Int, não módulo de byte), e sempre
// com ao menos uma maiúscula, uma minúscula e um dígito.
func SenhaAleatoria() (string, error) {
	limite := big.NewInt(int64(len(alfabetoSenha)))
	for tentativa := 0; tentativa < tentativasDeSenha; tentativa++ {
		buf := make([]byte, TamanhoSenhaGerada)
		for i := range buf {
			n, err := rand.Int(rand.Reader, limite)
			if err != nil {
				return "", err
			}
			buf[i] = alfabetoSenha[n.Int64()]
		}
		if temTresClasses(buf) {
			return string(buf), nil
		}
	}
	return "", errors.New("acoes: não consegui sortear uma senha com maiúscula, minúscula e dígito")
}

func temTresClasses(s []byte) bool {
	var maiuscula, minuscula, digito bool
	for _, b := range s {
		switch r := rune(b); {
		case unicode.IsUpper(r):
			maiuscula = true
		case unicode.IsLower(r):
			minuscula = true
		case unicode.IsDigit(r):
			digito = true
		}
	}
	return maiuscula && minuscula && digito
}

// emissorComDisponibilidade é o opcional que o emissor real implementa
// (emissorDoServico). Um falso de teste que não o tem vale como disponível.
type emissorComDisponibilidade interface{ Disponivel() bool }

// Disponivel diz se o emissor consegue criar o convite: o serviço existe e tem a
// chave de cifra (WELCOME_ENC_KEY). Sem ela o convite não guarda a senha e a
// pessoa não tem como recebê-la.
func (e emissorDoServico) Disponivel() bool { return e.svc != nil && e.svc.Enabled() }

// emissorComEmail é o opcional que o emissor real implementa: se o serviço manda email de
// verdade (SMTP configurado).
type emissorComEmail interface{ EmailLigado() bool }

// EmailLigado diz se o convite sai por email de verdade. O serviço sem SMTP ainda emite o convite
// (o link vale e a tela o mostra ao admin), mas o email fica 'disabled'.
func (e emissorDoServico) EmailLigado() bool { return e.svc != nil && e.svc.MailEnabled() }

// BoasVindasEmailLigado diz se o email de boas-vindas sai de verdade agora: é o que entrega a senha
// de quem o MCP cria. Sem emissor, não; emissor que não diz (um falso de teste) vale como ligado.
func (s *Servico) BoasVindasEmailLigado() bool {
	if s.BoasVindas == nil {
		return false
	}
	if d, ok := s.BoasVindas.(emissorComEmail); ok {
		return d.EmailLigado()
	}
	return true
}

// BoasVindasDisponiveis diz se criar um usuário e entregar a senha pelo email de
// boas-vindas é possível agora. É a condição de criar_usuario (F4-11): sem
// emissor, ou com o serviço desabilitado, o MCP recusa antes de criar alguém cuja
// senha ninguém conhece.
func (s *Servico) BoasVindasDisponiveis() bool {
	if s.BoasVindas == nil {
		return false
	}
	if d, ok := s.BoasVindas.(emissorComDisponibilidade); ok {
		return d.Disponivel()
	}
	return true
}
