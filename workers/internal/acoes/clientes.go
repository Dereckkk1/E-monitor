package acoes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

// MaxRotuloPublico é o teto defensivo do rótulo de público-alvo gravado em
// clients.target_label. A UI sugere 60 chars; aqui só barramos o absurdo (o
// campo é TEXT no banco, sem CHECK).
const MaxRotuloPublico = 200

// NormalizarRotuloPublico apara espaços e colapsa "" em nil (NULL no banco =
// cliente sem rótulo, que é o mesmo significado de string vazia). Devolve false
// quando o rótulo excede MaxRotuloPublico — contado em runes, para um rótulo
// acentuado não ser rejeitado por causa de bytes UTF-8.
func NormalizarRotuloPublico(p **string) bool {
	if *p == nil {
		return true
	}
	s := strings.TrimSpace(**p)
	if s == "" {
		*p = nil
		return true
	}
	if utf8.RuneCountInString(s) > MaxRotuloPublico {
		return false
	}
	*p = &s
	return true
}

// DigitosDoCNPJ devolve só os dígitos do CNPJ ("" quando nil).
func DigitosDoCNPJ(cnpj *string) string {
	if cnpj == nil {
		return ""
	}
	var b strings.Builder
	for _, r := range *cnpj {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ChecarCNPJ devolve uma *Recusa{cnpj_duplicado} quando OUTRO cliente (exceto o
// informado; uuid.Nil na criação) já tem estes 14 dígitos — com o cadastro
// existente em Dados["existente"] ({id, nome}) para quem monta a resposta. Giga
// e FSJ em 2026-10-22 nasceram duplicados. Erro do banco volta como veio (falha
// interna). A edição só deve chamar quando os dígitos MUDAM: duplicata que já
// existe (A e B com os mesmos 14 dígitos) não pode travar a edição de B.
func (s *Servico) ChecarCNPJ(ctx context.Context, cnpj *string, exceto uuid.UUID) error {
	outro, err := s.Clientes.OutroComMesmoCNPJ(ctx, cnpj, exceto)
	if err != nil {
		return err
	}
	if outro == nil {
		return nil
	}
	return &Recusa{
		Codigo:   CodigoCNPJDuplicado,
		Mensagem: fmt.Sprintf("já existe um cliente com este CNPJ: %s (%s)", outro.Name, outro.ID),
		Dados: map[string]any{
			"existente": map[string]any{"id": outro.ID.String(), "nome": outro.Name},
		},
	}
}

// CriarCliente valida, confere o CNPJ, grava e avisa o hub (`cliente.upsert`,
// fora do caminho da resposta). Aparar/limitar o rótulo do público-alvo é daqui:
// o `in` volta normalizado no cliente gravado.
func (s *Servico) CriarCliente(ctx context.Context, in catalog.CreateClientInput) (*catalog.Client, error) {
	if in.Name == "" {
		return nil, entradaInvalida("name is required")
	}
	if !NormalizarRotuloPublico(&in.TargetLabel) {
		return nil, entradaInvalida("target_label must be at most 200 characters")
	}
	if err := s.ChecarCNPJ(ctx, in.CNPJ, uuid.Nil); err != nil {
		return nil, err
	}
	out, err := s.Clientes.Create(ctx, in)
	if err != nil {
		return nil, err
	}
	s.avisarHubDoCliente(out)
	return out, nil
}

// EditarCliente substitui os campos do cadastro (PUT total: quem quer edição
// parcial lê o cliente e remonta a entrada). Valida o nome e o rótulo do
// público-alvo e confere o CNPJ SÓ QUANDO OS 14 DÍGITOS MUDAM — duplicata que já
// existe (A e B com os mesmos 14 dígitos) não pode travar a edição de B. `hub_id`
// não entra (nenhuma rota o grava). NÃO avisa o hub: a edição de cliente nunca
// avisou. nao_encontrado se o cliente não existe.
func (s *Servico) EditarCliente(ctx context.Context, id uuid.UUID, in catalog.UpdateClientInput) (*catalog.Client, error) {
	if in.Name == "" {
		return nil, entradaInvalida("name is required")
	}
	if !NormalizarRotuloPublico(&in.TargetLabel) {
		return nil, entradaInvalida("target_label must be at most 200 characters")
	}
	atual, err := s.Clientes.Get(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, naoEncontrado("cliente não encontrado")
		}
		return nil, err
	}
	if DigitosDoCNPJ(atual.CNPJ) != DigitosDoCNPJ(in.CNPJ) {
		if err := s.ChecarCNPJ(ctx, in.CNPJ, id); err != nil {
			return nil, err
		}
	}
	out, err := s.Clientes.Update(ctx, id, in)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, naoEncontrado("cliente não encontrado")
		}
		return nil, err
	}
	return out, nil
}

// avisarHubDoCliente manda `cliente.upsert` FORA do caminho da resposta (em
// segundo plano, com Servico.EmSegundoPlano).
//
// # Por que fora do caminho, e por que context.Background
//
// A criação do cliente NÃO PODE depender do hub estar de pé. E o contexto do
// pedido morre quando a resposta é escrita: usá-lo cancelaria a emissão no exato
// instante em que ela começa — defeito intermitente e silencioso, a pior
// combinação possível.
//
// # O preço desta escolha, escrito para quem for mexer
//
// Evento perdido é cliente que nunca chega ao hub, e não há nada em tela nenhuma
// dizendo que falta alguém. Quem conserta isso é o retrato periódico do §5 do
// desenho, que não está construído. Até lá, isto é atraso invisível, não erro
// visível. (A edição de cliente NÃO avisa o hub — como a rota nunca avisou.)
func (s *Servico) avisarHubDoCliente(c *catalog.Client) {
	if !s.hubConfigurado() || c == nil {
		return
	}
	// Os campos são copiados AQUI, síncrono, de propósito: a emissão não pode ler
	// `c` depois que o chamador seguiu adiante e talvez o alterou.
	dados := upsertDoCliente(c)
	h := s.Hub
	s.emSegundoPlano(func() { emitirClienteNoHub(h, c.ID, dados) })
}

func upsertDoCliente(c *catalog.Client) hub.ClienteUpsert {
	return hub.ClienteUpsert{
		IDNaPlataforma:  c.ID.String(),
		Nome:            c.Name,
		CNPJ:            c.CNPJ,
		LogoURL:         c.LogoURL,
		ContatoNome:     c.ContactName,
		ContatoEmail:    c.ContactEmail,
		ContatoTelefone: c.Phone,
		Cidade:          c.City,
		UF:              c.State,
	}
}

func emitirClienteNoHub(h AvisoHub, id uuid.UUID, dados hub.ClienteUpsert) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.Emitir(ctx, "cliente.upsert", dados); err != nil {
		// Log e mais nada: não há a quem devolver o erro, e repetir aqui seria
		// inventar uma fila sem durabilidade. Só o `client_id` e o erro; o
		// payload NÃO vai para o log.
		zap.L().Error("hub: cliente.upsert falhou",
			zap.String("client_id", id.String()), zap.Error(err))
	}
}
