package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

/*
A porta de sugestões do hub (spec do hub 2026-10-26 §3.3). Desde a virada, a
fila de sugestões do E-monitor MORA no hub; este arquivo só fala com ela. A
tradução para o JSON que a tela daqui consome fica no handler
(`handlers/suggestions_hub_dto.go`).

⚠️ Toda chamada leva quem é a pessoa logada AQUI, em dois headers. O hub confia
neles porque a chave autenticou esta plataforma — ou seja, é ESTE lado que
responde por eles: o e-mail sai sempre do banco (o JWT daqui não carrega
e-mail), nunca de algo que o navegador mandou.

⚠️ O vocabulário que trafega é o DO HUB (`ideia`, `urgenciaAutor`, `resposta`).
Nenhum tipo daqui carrega `notas`: a nota privada do gestor não sai pela porta,
e se um dia saísse por engano, este lado não a decodificaria.
*/

const prefixoSugestoes = "/api/platform/sugestoes"

// Autor é a pessoa logada aqui, em nome de quem o hub age.
type Autor struct {
	Email string
	Nome  string
}

// PessoaHub é como o hub descreve uma pessoa (usuário dele ou autor externo).
type PessoaHub struct {
	ID   string `json:"id"`
	Nome string `json:"nome"`
}

// SugestaoLinhaHub é um item da lista do hub (recorte `minhas`).
type SugestaoLinhaHub struct {
	ID                string    `json:"id"`
	Numero            int       `json:"numero"`
	Origem            string    `json:"origem"`
	Tipo              string    `json:"tipo"`
	Titulo            string    `json:"titulo"`
	Previa            string    `json:"previa"`
	Status            string    `json:"status"`
	Prioridade        *string   `json:"prioridade"`
	UrgenciaAutor     string    `json:"urgenciaAutor"`
	SuaVez            bool      `json:"suaVez"`
	Autor             PessoaHub `json:"autor"`
	Comentarios       int       `json:"comentarios"`
	Anexos            int       `json:"anexos"`
	NaoLida           bool      `json:"naoLida"`
	UltimaAtividadeEm time.Time `json:"ultimaAtividadeEm"`
	CreatedAt         time.Time `json:"createdAt"`
}

type listaHub struct {
	Itens   []SugestaoLinhaHub `json:"itens"`
	Pagina  int                `json:"pagina"`
	Paginas int                `json:"paginas"`
}

type ComentarioHub struct {
	ID        string    `json:"id"`
	Autor     PessoaHub `json:"autor"`
	Corpo     string    `json:"corpo"`
	DoGestor  bool      `json:"doGestor"`
	CreatedAt time.Time `json:"createdAt"`
}

// AnexoHub: `tipo` é `arquivo` ou `link`. Arquivo traz nome/mime/tamanho; link
// traz url/rótulo. A chave do bucket nunca vem.
type AnexoHub struct {
	ID           string    `json:"id"`
	Tipo         string    `json:"tipo"`
	ComentarioID *string   `json:"comentarioId"`
	Nome         *string   `json:"nome"`
	MimeType     *string   `json:"mimeType"`
	Tamanho      *int64    `json:"tamanho"`
	URL          *string   `json:"url"`
	Rotulo       *string   `json:"rotulo"`
	EnviadoPor   string    `json:"enviadoPor"`
	CreatedAt    time.Time `json:"createdAt"`
}

type EventoHub struct {
	ID        string     `json:"id"`
	Tipo      string     `json:"tipo"`
	Ator      *PessoaHub `json:"ator"`
	De        *string    `json:"de"`
	Para      *string    `json:"para"`
	CreatedAt time.Time  `json:"createdAt"`
}

// SugestaoHub é o detalhe que a porta devolve (sem `notas`, por contrato).
type SugestaoHub struct {
	ID                string          `json:"id"`
	Numero            int             `json:"numero"`
	NumeroOriginal    *int            `json:"numeroOriginal"`
	Origem            string          `json:"origem"`
	Tipo              string          `json:"tipo"`
	Titulo            string          `json:"titulo"`
	Descricao         string          `json:"descricao"`
	TelaAlvo          *string         `json:"telaAlvo"`
	UrgenciaAutor     string          `json:"urgenciaAutor"`
	Status            string          `json:"status"`
	Prioridade        *string         `json:"prioridade"`
	Esforco           *string         `json:"esforco"`
	Resposta          *string         `json:"resposta"`
	SuaVez            bool            `json:"suaVez"`
	Autor             PessoaHub       `json:"autor"`
	ResolvidaEm       *time.Time      `json:"resolvidaEm"`
	CreatedAt         time.Time       `json:"createdAt"`
	UltimaAtividadeEm time.Time       `json:"ultimaAtividadeEm"`
	Comentarios       []ComentarioHub `json:"comentarios"`
	Anexos            []AnexoHub      `json:"anexos"`
	Eventos           []EventoHub     `json:"eventos"`
}

// NovaSugestaoHub é o corpo do `POST /`. `ChaveEnvio` torna o reenvio inofensivo.
type NovaSugestaoHub struct {
	ChaveEnvio    string   `json:"chaveEnvio"`
	Tipo          string   `json:"tipo"`
	Titulo        string   `json:"titulo"`
	Descricao     string   `json:"descricao"`
	TelaAlvo      *string  `json:"telaAlvo,omitempty"`
	UrgenciaAutor string   `json:"urgenciaAutor"`
	AnexoIDs      []string `json:"anexoIds,omitempty"`
}

type NovoComentarioHub struct {
	ChaveEnvio string   `json:"chaveEnvio"`
	Corpo      string   `json:"corpo"`
	AnexoIDs   []string `json:"anexoIds,omitempty"`
}

type envelopeSugestao struct {
	Sugestao *SugestaoHub `json:"sugestao"`
}

// autorNosHeaders põe a chave e o autor. Única função que escreve esses headers.
func (c *Client) autorNosHeaders(req *http.Request, a Autor) {
	req.Header.Set("X-Hub-Platform-Key", c.platformKey)
	req.Header.Set("X-Hub-Autor-Email", strings.ToLower(strings.TrimSpace(a.Email)))
	// O hub faz `decodeURIComponent`. PathEscape e não QueryEscape: este troca
	// espaço por "+", que o decodeURIComponent NÃO desfaz ("Ana+Paula").
	req.Header.Set("X-Hub-Autor-Nome", url.PathEscape(strings.TrimSpace(a.Nome)))
}

// porta faz uma chamada JSON à porta de sugestões. `corpo` nil = sem corpo;
// `saida` nil = descarta a resposta. Qualquer coisa fora de 2xx é ERRO — nunca
// um sucesso vazio.
func (c *Client) porta(ctx context.Context, metodo, caminho string, a Autor, corpo, saida any) error {
	if !c.Configured() {
		return ErrNotConfigured
	}
	var leitor io.Reader
	if corpo != nil {
		b, err := json.Marshal(corpo)
		if err != nil {
			return &Error{Status: http.StatusInternalServerError, Message: "erro interno"}
		}
		leitor = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, metodo, c.baseURL+prefixoSugestoes+caminho, leitor)
	if err != nil {
		return &Error{Status: http.StatusInternalServerError, Message: "erro interno"}
	}
	if corpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.autorNosHeaders(req, a)

	resp, err := c.http.Do(req)
	if err != nil {
		return &Error{Status: http.StatusServiceUnavailable,
			Message: "não consegui falar com a Central de Clientes, tente de novo"}
	}
	// Drena antes de fechar: a conexão só volta ao pool keep-alive lida até o fim
	// (ver o comentário longo do `Emitir`).
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return erroDaPorta(resp)
	}
	if saida == nil {
		return nil
	}
	// O teto de leitura é o da RESPOSTA (1 MiB): o corpo de ida do /importar pode
	// ter até 2 MB do lado do hub e não passa por aqui.
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(saida); err != nil {
		return &Error{Status: http.StatusBadGateway, Message: "resposta ilegível da Central de Clientes"}
	}
	return nil
}

// erroDaPorta traduz a resposta de erro do hub (`{error:{code,message}}`).
//
// O eixo é o mesmo do `ConferirCodigo`: o que é da PESSOA (404, validação)
// passa com o status e a frase do hub, que é legível por gente; o que é NOSSO
// (chave, teto, hub quebrado) vira 503/502 com frase de suporte.
func erroDaPorta(resp *http.Response) error {
	var corpo struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&corpo)
	msg := strings.TrimSpace(corpo.Error.Message)
	switch resp.StatusCode {
	case http.StatusNotFound:
		if msg == "" {
			msg = "sugestão não encontrada"
		}
		return &Error{Status: http.StatusNotFound, Message: msg}
	case http.StatusBadRequest, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		if msg == "" {
			msg = "a Central de Clientes recusou o pedido"
		}
		return &Error{Status: resp.StatusCode, Message: msg}
	case http.StatusUnauthorized:
		return &Error{Status: http.StatusServiceUnavailable, Message: "a chave do hub foi recusada — avise o suporte"}
	case http.StatusTooManyRequests:
		return &Error{Status: http.StatusServiceUnavailable, Message: "a Central de Clientes está ocupada, tente de novo em instantes"}
	default:
		return &Error{Status: http.StatusBadGateway, Message: fmt.Sprintf("a Central de Clientes respondeu %d", resp.StatusCode)}
	}
}

// Teto de páginas da lista do autor (50 por página no hub). Uma pessoa com mais
// de mil sugestões é laço, não uso.
const maxPaginasSugestoes = 20

// ListarSugestoes traz TODAS as sugestões do autor (de todas as origens),
// página a página. `status` vazio = todas.
func (c *Client) ListarSugestoes(ctx context.Context, a Autor, status string) ([]SugestaoLinhaHub, error) {
	todas := []SugestaoLinhaHub{}
	for pagina := 1; pagina <= maxPaginasSugestoes; pagina++ {
		q := url.Values{}
		q.Set("pagina", strconv.Itoa(pagina))
		if status != "" {
			q.Set("status", status)
		}
		var l listaHub
		if err := c.porta(ctx, http.MethodGet, "?"+q.Encode(), a, nil, &l); err != nil {
			return nil, err
		}
		if l.Itens == nil {
			return nil, &Error{Status: http.StatusBadGateway, Message: "resposta ilegível da Central de Clientes"}
		}
		todas = append(todas, l.Itens...)
		if pagina >= l.Paginas {
			break
		}
	}
	return todas, nil
}

func (c *Client) sugestaoDe(ctx context.Context, metodo, caminho string, a Autor, corpo any) (*SugestaoHub, error) {
	var env envelopeSugestao
	if err := c.porta(ctx, metodo, caminho, a, corpo, &env); err != nil {
		return nil, err
	}
	if env.Sugestao == nil || env.Sugestao.ID == "" {
		return nil, &Error{Status: http.StatusBadGateway, Message: "resposta ilegível da Central de Clientes"}
	}
	return env.Sugestao, nil
}

// DetalheSugestao: 404 do hub quando a sugestão não é do autor.
func (c *Client) DetalheSugestao(ctx context.Context, a Autor, id string) (*SugestaoHub, error) {
	return c.sugestaoDe(ctx, http.MethodGet, "/"+url.PathEscape(id), a, nil)
}

func (c *Client) CriarSugestao(ctx context.Context, a Autor, s NovaSugestaoHub) (*SugestaoHub, error) {
	return c.sugestaoDe(ctx, http.MethodPost, "", a, s)
}

func (c *Client) ComentarSugestao(ctx context.Context, a Autor, id string, cm NovoComentarioHub) (*SugestaoHub, error) {
	return c.sugestaoDe(ctx, http.MethodPost, "/"+url.PathEscape(id)+"/comentarios", a, cm)
}

type respostaNaoLidas struct {
	NaoLidas *int `json:"naoLidas"`
}

func (c *Client) naoLidasDe(ctx context.Context, metodo, caminho string, a Autor) (int, error) {
	var r respostaNaoLidas
	if err := c.porta(ctx, metodo, caminho, a, nil, &r); err != nil {
		return 0, err
	}
	if r.NaoLidas == nil {
		return 0, &Error{Status: http.StatusBadGateway, Message: "resposta ilegível da Central de Clientes"}
	}
	return *r.NaoLidas, nil
}

// MarcarSugestaoLida devolve quantas ainda restam não lidas.
func (c *Client) MarcarSugestaoLida(ctx context.Context, a Autor, id string) (int, error) {
	return c.naoLidasDe(ctx, http.MethodPost, "/"+url.PathEscape(id)+"/lida", a)
}

func (c *Client) SugestoesNaoLidas(ctx context.Context, a Autor) (int, error) {
	return c.naoLidasDe(ctx, http.MethodGet, "/nao-lidas", a)
}

// ── Importação única (spec do hub §5) ──────────────────────────────────────

type AutorJSON struct {
	Email string `json:"email"`
	Nome  string `json:"nome"`
}

type ComentarioImportado struct {
	IDOriginal string    `json:"idOriginal"`
	Autor      AutorJSON `json:"autor"`
	Corpo      string    `json:"corpo"`
	CreatedAt  time.Time `json:"createdAt"`
	AnexoIDs   []string  `json:"anexoIds"`
}

// SugestaoImportada é o corpo de `POST /importar`. É o ÚNICO tipo daqui com
// `Notas`: a nota privada do E-monitor vai para a nota privada do hub.
type SugestaoImportada struct {
	IDOriginal     string                `json:"idOriginal"`
	NumeroOriginal int                   `json:"numeroOriginal"`
	Tipo           string                `json:"tipo"`
	Titulo         string                `json:"titulo"`
	Descricao      string                `json:"descricao"`
	TelaAlvo       *string               `json:"telaAlvo"`
	UrgenciaAutor  string                `json:"urgenciaAutor"`
	Status         string                `json:"status"`
	Prioridade     *string               `json:"prioridade"`
	Esforco        *string               `json:"esforco"`
	Resposta       *string               `json:"resposta"`
	Notas          *string               `json:"notas"`
	SuaVez         bool                  `json:"suaVez"`
	ResolvidaEm    *time.Time            `json:"resolvidaEm"`
	CreatedAt      time.Time             `json:"createdAt"`
	AnexoIDs       []string              `json:"anexoIds"`
	Comentarios    []ComentarioImportado `json:"comentarios"`
}

type RespostaImportar struct {
	Acao       string `json:"acao"`
	SugestaoID string `json:"sugestaoId"`
	Numero     int    `json:"numero"`
}

// ImportarSugestao manda UMA sugestão. `a` é o autor da sugestão; o de cada
// comentário vai no corpo. Só `importada` e `ja-importada` são sucesso.
func (c *Client) ImportarSugestao(ctx context.Context, a Autor, s SugestaoImportada) (*RespostaImportar, error) {
	if s.AnexoIDs == nil {
		s.AnexoIDs = []string{}
	}
	if s.Comentarios == nil {
		s.Comentarios = []ComentarioImportado{}
	}
	for i := range s.Comentarios {
		if s.Comentarios[i].AnexoIDs == nil {
			s.Comentarios[i].AnexoIDs = []string{}
		}
	}
	var r RespostaImportar
	if err := c.porta(ctx, http.MethodPost, "/importar", a, s, &r); err != nil {
		return nil, err
	}
	if r.Acao != "importada" && r.Acao != "ja-importada" {
		return nil, &Error{Status: http.StatusBadGateway,
			Message: fmt.Sprintf("a Central de Clientes respondeu acao %q na importação", r.Acao)}
	}
	return &r, nil
}
