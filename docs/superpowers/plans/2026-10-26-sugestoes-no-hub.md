# Sugestões do E-monitor na fila do hub — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a tela "Sugestões" do E-monitor (`/admin/suggestions`) passa a gravar e ler na fila do hub, sem banco local, e o histórico do Postgres é importado uma vez para o hub.

**Architecture:** um cliente Go isolado (`workers/internal/hub/sugestoes*.go`) fala com a porta `/api/platform/sugestoes` do hub, levando a chave da plataforma e o autor logado nos headers `X-Hub-Autor-*`. Um handler novo (`SuggestionsHubHandler`) assume as mesmas rotas `/v1/internal/suggestions*` e traduz o vocabulário do hub para o JSON que o front já consome. O `SuggestionsHandler` antigo (Postgres) fica no código, testado, mas sai do router. O front muda pouco: o anexo sobe ANTES da criação ou do comentário, a pessoa gestora vê "A fila agora vive no hub", e entram o status `retirada` e o selo de origem. Um CLI novo (`import-suggestions-hub`) lê Postgres + MinIO e manda cada sugestão para `/importar`.

**Tech Stack:** Go 1.26 (chi, pgx v5, testify), React + Vite + TanStack Query (axios), Playwright (`page.route`), Docker Compose na VM (sem CI).

**Fonte da verdade:** spec do hub `C:\tmp\ehub-sugestoes-plataformas\docs\superpowers\specs\2026-10-26-sugestoes-de-todas-as-plataformas-design.md` (commit `f10a69c9`). O contrato está nos §3.3 (porta), §4.3 (anexos) e §5 (importação). **O contrato é FIXO**: o que parecer furo vai para "Perguntas para o plano do hub", no fim deste arquivo, e não vira código.

**Worktree:** `C:\tmp\emonitor-sugestoes-hub`, branch `feat/sugestoes-no-hub`, saída de `origin/master` `c8f93ad`. O repo só tem `master`. A árvore em `Desktop/.../E-monitor` é de outra frente e não se toca.

---

## Antes de começar: as armadilhas deste repo

1. **`go test` com banco ausente sai verde mentindo.** Sem `TEST_DATABASE_URL`, os testes de `internal/api/handlers` e `internal/catalog` saem como `SKIP` com **exit 0**. Os testes NOVOS deste plano nos pacotes `hub`, `handlers` (os `suggestions_hub*`) e `cmd/import-suggestions-hub` **não usam banco, de propósito**: rodam sempre. Só a Task 11 (export do catalog) e a Task 13 (suíte inteira) precisam do banco. Conte o SKIP com `grep -cE -- '--- SKIP' arquivo || true`: o `grep -c` sai 1 quando conta zero.
2. **Banco de teste sem Docker** (Postgres nativo na 5433). Receita da Task 13. Sem `PGCLIENTENCODING=UTF8` a migração 0057 morre. Sem as partições do passado, 52 testes do `catalog` morrem.
3. **Commit:** sempre `git commit -F <arquivo> -- <caminhos>`. `-m` depois do `--` não commita, e crase na mensagem é executada pelo bash. Mensagem terminada em `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`. Nunca `git add -A`.
4. **O E-monitor não tem CI.** Código na `master` não está no ar (memória "E-rádios e E-monitor não publicam sozinhos"). O deploy é manual, na VM (`docs/operations/deploy.md`). E o hub tem que estar no ar ANTES: confira com o `curl` da Task 13.
5. **O 302 do anexo NÃO pode ser seguido pelo `http.Client` padrão.** O Go copia headers não sensíveis num redirect, e `X-Hub-Platform-Key` e `X-Hub-Autor-Email` iriam parar no GCS. A Task 3 desliga o redirect e segue à mão, sem headers.
6. **O front se testa com Playwright + `page.route`** (o front daqui não tem vitest). `npm run lint` é o portão de JS; `npm run build` é o de compilação.

## Mapa de arquivos

| Arquivo | O que é |
| --- | --- |
| Create `workers/internal/hub/sugestoes.go` | tipos da porta + `porta()` + listar/detalhe/criar/comentar/lida/não lidas/importar |
| Create `workers/internal/hub/sugestoes_anexos.go` | subir (upload-url → PUT → confirm) e abrir (302 seguido à mão) |
| Create `workers/internal/hub/sugestoes_vocabulario.go` | `feature↔ideia`, nomes de evento |
| Create `workers/internal/hub/sugestoes_test.go`, `sugestoes_anexos_test.go`, `sugestoes_vocabulario_test.go` | testes com `httptest` |
| Create `workers/internal/api/handlers/suggestions_hub_dto.go` (+ `_test`) | hub → JSON da tela daqui |
| Create `workers/internal/api/handlers/suggestions_hub_cache.go` (+ `_test`) | cache de 30 s do badge |
| Create `workers/internal/api/handlers/suggestions_hub.go` (+ `_test`) | o handler novo |
| Modify `workers/internal/api/router.go:64,729-749` | `SuggestionsHub` no `Deps`; rotas apontam para ele |
| Modify `workers/cmd/api/main.go:679-684` | liga `SuggestionsHub` no lugar de `Suggestions` |
| Create `workers/internal/catalog/suggestions_export.go` (+ `_test`) | leitura completa para a importação |
| Create `workers/cmd/import-suggestions-hub/main.go`, `importa.go`, `importa_test.go` | o CLI |
| Modify `infra/docker/Dockerfiles/workers.Dockerfile` | compila e copia o CLI |
| Modify `frontend/src/api/hooks.js:1509-1537` | `useStageSuggestionAttachment`; comentário com anexos e chave |
| Modify `frontend/src/pages/suggestions/constants.js` | `retirada`, `ORIGIN`, `HUB_PORTAL_URL` |
| Modify `frontend/src/pages/suggestions/SuggestionPills.jsx` | `OriginPill` |
| Create `frontend/src/pages/suggestions/AttachmentItem.jsx` | imagem / arquivo / link |
| Create `frontend/src/pages/suggestions/FilaNoHub.jsx` | o aviso da pessoa gestora |
| Modify `frontend/src/pages/AdminSuggestionsPage.jsx` | gestora → `FilaNoHub` |
| Modify `frontend/src/pages/suggestions/SuggestionCreateModal.jsx`, `SuggestionThread.jsx`, `SuggestionDetail.jsx`, `MinhasSugestoes.jsx` | o fluxo novo de anexo, o selo, os eventos novos |
| Modify `frontend/src/pages/AdminSuggestionsPage.css` | `retirada`, origem, arquivo, link, aviso |
| Create `frontend/e2e/sugestoes-hub.spec.js` | a tela clicada |
| Modify `docs/features/suggestions-board.md` | o desenho novo |

---

### Task 1: O cliente da porta — tipos, headers do autor, erro como erro

**Files:**
- Create: `workers/internal/hub/sugestoes.go`
- Test: `workers/internal/hub/sugestoes_test.go`

- [ ] **Step 1: Escrever os testes que falham**

```go
package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

var autorAna = Autor{Email: " Ana@T.local ", Nome: "Ana Paula Ávila"}

// hubFalso responde uma rota só e guarda o que recebeu.
type hubFalso struct {
	srv     *httptest.Server
	pedidos []*http.Request
	corpos  []string
}

func novoHubFalso(t *testing.T, responde func(w http.ResponseWriter, r *http.Request)) *hubFalso {
	t.Helper()
	h := &hubFalso{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.pedidos = append(h.pedidos, r)
		h.corpos = append(h.corpos, string(b))
		responde(w, r)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func jsonDe(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestPorta_LevaChaveEAutorNosHeaders(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		jsonDe(w, 200, map[string]any{"naoLidas": 3})
	})
	c := New(h.srv.URL, "chave-x")

	n, err := c.SugestoesNaoLidas(context.Background(), autorAna)

	require.NoError(t, err)
	require.Equal(t, 3, n)
	r := h.pedidos[0]
	require.Equal(t, "/api/platform/sugestoes/nao-lidas", r.URL.Path)
	require.Equal(t, "chave-x", r.Header.Get("X-Hub-Platform-Key"))
	require.Equal(t, "ana@t.local", r.Header.Get("X-Hub-Autor-Email"))
	// encodeURIComponent do lado de lá: espaço vira %20, NUNCA "+".
	require.Equal(t, "Ana%20Paula%20%C3%81vila", r.Header.Get("X-Hub-Autor-Nome"))
}

func TestPorta_ErroDoHubViraErroComAMensagemDeLa(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		jsonDe(w, 422, map[string]any{"error": map[string]string{"code": "campo_invalido", "message": "O título passa de 180 caracteres"}})
	})
	_, err := New(h.srv.URL, "k").CriarSugestao(context.Background(), autorAna, NovaSugestaoHub{ChaveEnvio: "chave-123456"})

	e, ok := AsError(err)
	require.True(t, ok)
	require.Equal(t, 422, e.Status)
	require.Equal(t, "O título passa de 180 caracteres", e.Message)
}

func TestPorta_ChaveRecusadaVira503ComFraseDeSuporte(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		jsonDe(w, 401, map[string]any{"error": map[string]string{"code": "invalid_platform_key", "message": "chave de plataforma inválida"}})
	})
	_, err := New(h.srv.URL, "k").SugestoesNaoLidas(context.Background(), autorAna)

	e, _ := AsError(err)
	require.Equal(t, http.StatusServiceUnavailable, e.Status)
	require.Contains(t, e.Message, "chave do hub foi recusada")
}

func TestPorta_HubForaDoArNaoViraSucesso(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {})
	url := h.srv.URL
	h.srv.Close()

	_, err := New(url, "k").DetalheSugestao(context.Background(), autorAna, "6650aaaaaaaaaaaaaaaaaaaa")

	e, ok := AsError(err)
	require.True(t, ok)
	require.Equal(t, http.StatusServiceUnavailable, e.Status)
	require.Contains(t, e.Message, "tente de novo")
}

func TestPorta_200SemOCampoEsperadoEErro(t *testing.T) {
	// Zero não é ausência: um 200 sem `naoLidas` (envelope novo, proxy, SPA)
	// não pode apagar o badge.
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) { jsonDe(w, 200, map[string]any{}) })
	_, err := New(h.srv.URL, "k").SugestoesNaoLidas(context.Background(), autorAna)
	e, _ := AsError(err)
	require.Equal(t, http.StatusBadGateway, e.Status)
}

func TestPorta_SemConfiguracaoNaoSaiDeCasa(t *testing.T) {
	_, err := New("", "").SugestoesNaoLidas(context.Background(), autorAna)
	require.ErrorIs(t, err, ErrNotConfigured)
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/hub/ -run 'TestPorta_' -v`
Expected: FAIL de compilação — `undefined: Autor`, `undefined: NovaSugestaoHub`, `c.SugestoesNaoLidas undefined`.

- [ ] **Step 3: Implementar**

`workers/internal/hub/sugestoes.go`:

```go
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
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/hub/ -run 'TestPorta_' -v`
Expected: `--- PASS` nos 6 testes; `ok  radiocheck/internal/hub`.

- [ ] **Step 5: Commit**

```bash
cd /c/tmp/emonitor-sugestoes-hub
printf '%s\n' "feat(hub): cliente da porta de sugestoes do hub" "" "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" > "$TEMP/msg-t1.txt"
git add -- workers/internal/hub/sugestoes.go workers/internal/hub/sugestoes_test.go
git commit -F "$TEMP/msg-t1.txt" -- workers/internal/hub/sugestoes.go workers/internal/hub/sugestoes_test.go
```

---

### Task 2: Listar, detalhar, criar, comentar e marcar como lida

**Files:**
- Test: `workers/internal/hub/sugestoes_test.go` (acrescentar)

O código já foi escrito na Task 1. Esta task prende o comportamento de cada método.

- [ ] **Step 1: Escrever os testes**

Acrescentar `"strconv"` ao bloco de imports de `sugestoes_test.go` e, ao fim do arquivo:

```go
func TestListarSugestoes_JuntaAsPaginas(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("pagina")
		n, _ := strconv.Atoi(p) // número no JSON, como o hub manda
		jsonDe(w, 200, map[string]any{
			"itens":   []map[string]any{{"id": "id-" + p, "numero": 1, "origem": "hub", "tipo": "ideia", "status": "nova"}},
			"pagina":  n, "paginas": 2, "total": 2, "naoLidas": 0,
		})
	})
	l, err := New(h.srv.URL, "k").ListarSugestoes(context.Background(), autorAna, "concluida")

	require.NoError(t, err)
	require.Len(t, l, 2)
	require.Equal(t, "id-1", l[0].ID)
	require.Equal(t, "id-2", l[1].ID)
	require.Equal(t, "concluida", h.pedidos[0].URL.Query().Get("status"))
	require.Equal(t, "/api/platform/sugestoes", h.pedidos[0].URL.Path)
}

func TestCriarSugestao_MandaOCorpoDoContrato(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		jsonDe(w, 201, map[string]any{"sugestao": map[string]any{"id": "6650aaaaaaaaaaaaaaaaaaaa", "numero": 42, "origem": "e-monitor"}})
	})
	tela := "Campanhas"
	s, err := New(h.srv.URL, "k").CriarSugestao(context.Background(), autorAna, NovaSugestaoHub{
		ChaveEnvio: "chave-123456", Tipo: "ideia", Titulo: "T", Descricao: "D",
		TelaAlvo: &tela, UrgenciaAutor: "alta", AnexoIDs: []string{"a1"},
	})

	require.NoError(t, err)
	require.Equal(t, 42, s.Numero)
	require.Equal(t, http.MethodPost, h.pedidos[0].Method)
	require.JSONEq(t, `{"chaveEnvio":"chave-123456","tipo":"ideia","titulo":"T","descricao":"D",
		"telaAlvo":"Campanhas","urgenciaAutor":"alta","anexoIds":["a1"]}`, h.corpos[0])
}

func TestDetalheSugestao_NaoDecodificaNotas(t *testing.T) {
	// Se o hub um dia vazar `notas`, este lado não tem onde guardá-las.
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		jsonDe(w, 200, map[string]any{"sugestao": map[string]any{"id": "x1", "notas": "segredo do gestor"}})
	})
	s, err := New(h.srv.URL, "k").DetalheSugestao(context.Background(), autorAna, "x1")
	require.NoError(t, err)
	b, _ := json.Marshal(s)
	require.NotContains(t, string(b), "segredo")
}

func TestDetalheSugestao_IdComBarraNaoVazaDoCaminho(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) { jsonDe(w, 404, map[string]any{}) })
	_, err := New(h.srv.URL, "k").DetalheSugestao(context.Background(), autorAna, "../importar")
	e, _ := AsError(err)
	require.Equal(t, 404, e.Status)
	require.Equal(t, "/api/platform/sugestoes/..%2Fimportar", h.pedidos[0].URL.RawPath)
}

func TestComentarSugestao_E_Lida(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/platform/sugestoes/x1/lida" {
			jsonDe(w, 200, map[string]any{"naoLidas": 0})
			return
		}
		jsonDe(w, 201, map[string]any{"sugestao": map[string]any{"id": "x1"}})
	})
	c := New(h.srv.URL, "k")
	_, err := c.ComentarSugestao(context.Background(), autorAna, "x1", NovoComentarioHub{ChaveEnvio: "chave-abcdefgh", Corpo: "oi"})
	require.NoError(t, err)
	require.Equal(t, "/api/platform/sugestoes/x1/comentarios", h.pedidos[0].URL.Path)
	require.JSONEq(t, `{"chaveEnvio":"chave-abcdefgh","corpo":"oi"}`, h.corpos[0])

	n, err := c.MarcarSugestaoLida(context.Background(), autorAna, "x1")
	require.NoError(t, err)
	require.Equal(t, 0, n)
}
```

- [ ] **Step 2: Rodar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/hub/ -run 'TestListar|TestCriar|TestDetalhe|TestComentar' -v`
Expected: `--- PASS` nos 5. Se `TestDetalheSugestao_IdComBarraNaoVazaDoCaminho` falhar por `RawPath` vazio, o `PathEscape` não está sendo aplicado: conserte o código, não o teste.

- [ ] **Step 3: Mutação (prova de que a asserção morde)**

Copie o arquivo (`cp workers/internal/hub/sugestoes.go "$TEMP/sug.go.bak"`), troque `url.PathEscape(id)` por `id` em `DetalheSugestao`, rode o teste e veja FALHAR. Restaure com `cp "$TEMP/sug.go.bak" workers/internal/hub/sugestoes.go && git diff --stat` (esperado: sem diff em `sugestoes.go`). **Nunca restaure com `git checkout --`**: desfaz o que não está commitado.

- [ ] **Step 4: Commit**

```bash
printf '%s\n' "test(hub): prende listar/criar/detalhe/comentar da porta de sugestoes" "" "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" > "$TEMP/msg-t2.txt"
git commit -F "$TEMP/msg-t2.txt" -- workers/internal/hub/sugestoes_test.go
```

---

### Task 3: Anexos — subir em três passos e abrir sem vazar a chave

**Files:**
- Create: `workers/internal/hub/sugestoes_anexos.go`
- Test: `workers/internal/hub/sugestoes_anexos_test.go`

- [ ] **Step 1: Escrever os testes que falham**

```go
package hub

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// bucketFalso faz o papel do GCS: guarda o que chegou e com que headers.
type bucketFalso struct {
	srv     *httptest.Server
	headers []http.Header
	corpo   []byte
	status  int
}

func novoBucket(t *testing.T) *bucketFalso {
	b := &bucketFalso{status: 200}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.headers = append(b.headers, r.Header.Clone())
		if r.Method == http.MethodPut {
			b.corpo, _ = io.ReadAll(r.Body)
			w.WriteHeader(b.status)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNGBYTES"))
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func TestSubirAnexo_TresPassos(t *testing.T) {
	b := novoBucket(t)
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/platform/sugestoes/anexos/upload-url":
			jsonDe(w, 201, map[string]any{"anexoId": "an1", "uploadUrl": b.srv.URL + "/obj?assinado=1"})
		case "/api/platform/sugestoes/anexos/an1/confirm":
			jsonDe(w, 200, map[string]any{"anexo": map[string]any{"id": "an1"}})
		default:
			w.WriteHeader(500)
		}
	})

	id, err := New(h.srv.URL, "chave-x").SubirAnexoSugestao(context.Background(), autorAna, "print.png", "image/png", []byte("12345"))

	require.NoError(t, err)
	require.Equal(t, "an1", id)
	require.JSONEq(t, `{"nome":"print.png","mimeType":"image/png","tamanho":5}`, h.corpos[0])
	require.Equal(t, []byte("12345"), b.corpo)
	require.Equal(t, "image/png", b.headers[0].Get("Content-Type"))
	// O bucket NUNCA vê a chave nem o autor.
	require.Empty(t, b.headers[0].Get("X-Hub-Platform-Key"))
	require.Empty(t, b.headers[0].Get("X-Hub-Autor-Email"))
	require.Len(t, h.pedidos, 2) // upload-url + confirm
}

func TestSubirAnexo_BucketRecusaENaoConfirma(t *testing.T) {
	b := novoBucket(t)
	b.status = 403
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		jsonDe(w, 201, map[string]any{"anexoId": "an1", "uploadUrl": b.srv.URL + "/obj"})
	})
	_, err := New(h.srv.URL, "k").SubirAnexoSugestao(context.Background(), autorAna, "p.png", "image/png", []byte("x"))

	e, _ := AsError(err)
	require.Equal(t, http.StatusBadGateway, e.Status)
	require.Len(t, h.pedidos, 1) // não houve confirm
}

func TestAbrirAnexo_Segue302SemHeaders(t *testing.T) {
	b := novoBucket(t)
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/platform/sugestoes/anexos/an1/arquivo", r.URL.Path)
		require.Equal(t, "1", r.URL.Query().Get("inline"))
		http.Redirect(w, r, b.srv.URL+"/obj?assinado=1", http.StatusFound)
	})

	corpo, ct, err := New(h.srv.URL, "chave-x").AbrirAnexoSugestao(context.Background(), autorAna, "an1")

	require.NoError(t, err)
	defer corpo.Close()
	dados, _ := io.ReadAll(corpo)
	require.Equal(t, "PNGBYTES", string(dados))
	require.Equal(t, "image/png", ct)
	require.Empty(t, b.headers[0].Get("X-Hub-Platform-Key"), "a chave vazou para o bucket no redirect")
	require.Empty(t, b.headers[0].Get("X-Hub-Autor-Email"))
}

func TestAbrirAnexo_404DoHubPassa(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		jsonDe(w, 404, map[string]any{"error": map[string]string{"code": "not_found", "message": "Anexo não encontrado"}})
	})
	_, _, err := New(h.srv.URL, "k").AbrirAnexoSugestao(context.Background(), autorAna, "an1")
	e, _ := AsError(err)
	require.Equal(t, 404, e.Status)
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/hub/ -run 'Anexo' -v`
Expected: FAIL — `undefined: (*Client).SubirAnexoSugestao`.

- [ ] **Step 3: Implementar**

`workers/internal/hub/sugestoes_anexos.go`:

```go
package hub

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

/*
Os anexos de sugestão no hub (spec do hub §4.3): o mesmo fluxo de três passos da
tela do hub — URL assinada → PUT no GCS → confirm —, só que feito AQUI, no
servidor. O arquivo sobe "sem dono"; quem o amarra é a criação ou o comentário,
com `anexoIds`.
*/

// transferencia fala com o BUCKET, nunca com o hub: 60 s porque 10 MB numa rede
// ruim passam dos 10 s do cliente da porta. Sem headers nossos, nunca.
var transferencia = &http.Client{Timeout: 60 * time.Second}

type pedidoDeUpload struct {
	AnexoID   string `json:"anexoId"`
	UploadURL string `json:"uploadUrl"`
}

// SubirAnexoSugestao devolve o id do anexo no hub, pronto para `anexoIds`.
func (c *Client) SubirAnexoSugestao(ctx context.Context, a Autor, nome, mime string, dados []byte) (string, error) {
	var p pedidoDeUpload
	if err := c.porta(ctx, http.MethodPost, "/anexos/upload-url", a,
		map[string]any{"nome": nome, "mimeType": mime, "tamanho": len(dados)}, &p); err != nil {
		return "", err
	}
	if p.AnexoID == "" || p.UploadURL == "" {
		return "", &Error{Status: http.StatusBadGateway, Message: "resposta ilegível da Central de Clientes"}
	}
	if err := putNoBucket(ctx, p.UploadURL, mime, dados); err != nil {
		return "", err
	}
	if err := c.porta(ctx, http.MethodPost, "/anexos/"+url.PathEscape(p.AnexoID)+"/confirm", a, nil, nil); err != nil {
		return "", err
	}
	return p.AnexoID, nil
}

// putNoBucket manda os bytes para a URL assinada.
//
// ⚠️ SÓ o Content-Type, e tem de ser o MESMO que o hub assinou
// (`signedUploadUrl(objectKey, mimeType)`); outro dá 403 SignatureDoesNotMatch.
// A URL assinada já é a credencial: chave da plataforma aqui iria parar no log
// do Google.
func putNoBucket(ctx context.Context, destino, mime string, dados []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, destino, bytes.NewReader(dados))
	if err != nil {
		return &Error{Status: http.StatusInternalServerError, Message: "erro interno"}
	}
	req.Header.Set("Content-Type", mime)
	resp, err := transferencia.Do(req)
	if err != nil {
		return &Error{Status: http.StatusServiceUnavailable, Message: "não consegui enviar o arquivo, tente de novo"}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &Error{Status: http.StatusBadGateway, Message: fmt.Sprintf("o armazenamento recusou o arquivo (%d)", resp.StatusCode)}
	}
	return nil
}

/*
AbrirAnexoSugestao devolve os BYTES do anexo. Quem chama fecha o corpo.

O hub responde 302 para uma URL assinada de 5 min. Ela NÃO vai para o navegador:
é o incidente de 2026-07-13 deste repo (URL pré-assinada com host inalcançável,
imagem quebrada). A tela continua recebendo bytes pelo proxy da API.

⚠️ O redirect é seguido À MÃO. O `http.Client` padrão copia num redirect os
headers que não considera sensíveis — `X-Hub-Platform-Key` e `X-Hub-Autor-Email`
iriam para o GCS. Por isso o primeiro pedido usa `ErrUseLastResponse` e o
segundo sai limpo, pelo cliente de transferência.
*/
func (c *Client) AbrirAnexoSugestao(ctx context.Context, a Autor, id string) (io.ReadCloser, string, error) {
	if !c.Configured() {
		return nil, "", ErrNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+prefixoSugestoes+"/anexos/"+url.PathEscape(id)+"/arquivo?inline=1", nil)
	if err != nil {
		return nil, "", &Error{Status: http.StatusInternalServerError, Message: "erro interno"}
	}
	c.autorNosHeaders(req, a)
	semSeguir := &http.Client{
		Timeout:       c.http.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := semSeguir.Do(req)
	if err != nil {
		return nil, "", &Error{Status: http.StatusServiceUnavailable, Message: "não consegui falar com a Central de Clientes, tente de novo"}
	}
	var destino *url.URL
	if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
		destino, err = resp.Request.URL.Parse(resp.Header.Get("Location"))
	}
	var falha error
	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		falha = erroDaPorta(resp)
	} else if err != nil || destino == nil || resp.Header.Get("Location") == "" {
		falha = &Error{Status: http.StatusBadGateway, Message: "resposta ilegível da Central de Clientes"}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	_ = resp.Body.Close()
	if falha != nil {
		return nil, "", falha
	}

	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, destino.String(), nil)
	if err != nil {
		return nil, "", &Error{Status: http.StatusInternalServerError, Message: "erro interno"}
	}
	resp2, err := transferencia.Do(req2)
	if err != nil {
		return nil, "", &Error{Status: http.StatusServiceUnavailable, Message: "não consegui buscar o arquivo, tente de novo"}
	}
	if resp2.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp2.Body, maxBody))
		_ = resp2.Body.Close()
		return nil, "", &Error{Status: http.StatusBadGateway, Message: fmt.Sprintf("o armazenamento não entregou o arquivo (%d)", resp2.StatusCode)}
	}
	return resp2.Body, resp2.Header.Get("Content-Type"), nil
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/hub/ -run 'Anexo' -v`
Expected: `--- PASS` nos 4.

- [ ] **Step 5: Mutação do vazamento**

Com cópia em `$TEMP`, troque `semSeguir.Do(req)` por `c.http.Do(req)`. Esperado: `TestAbrirAnexo_Segue302SemHeaders` FALHA em "a chave vazou para o bucket no redirect". Restaure da cópia e confira com `git diff --stat`.

- [ ] **Step 6: Commit**

```bash
printf '%s\n' "feat(hub): anexo de sugestao em tres passos e 302 seguido sem headers" "" "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" > "$TEMP/msg-t3.txt"
git add -- workers/internal/hub/sugestoes_anexos.go workers/internal/hub/sugestoes_anexos_test.go
git commit -F "$TEMP/msg-t3.txt" -- workers/internal/hub/sugestoes_anexos.go workers/internal/hub/sugestoes_anexos_test.go
```

---

### Task 4: Vocabulário e importação no cliente

**Files:**
- Create: `workers/internal/hub/sugestoes_vocabulario.go`
- Modify: `workers/internal/hub/sugestoes.go` (acrescentar a importação)
- Test: `workers/internal/hub/sugestoes_vocabulario_test.go`, `workers/internal/hub/sugestoes_test.go`

- [ ] **Step 1: Escrever os testes que falham**

`sugestoes_vocabulario_test.go`:

```go
package hub

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTipo_IdaEVolta(t *testing.T) {
	require.Equal(t, "ideia", TipoParaHub("feature"))
	require.Equal(t, "feature", TipoDoHub("ideia"))
	for _, t2 := range []string{"bug", "melhoria", "duvida"} {
		require.Equal(t, t2, TipoParaHub(t2))
		require.Equal(t, t2, TipoDoHub(t2))
	}
}

func TestEventoDoHub(t *testing.T) {
	require.Equal(t, "created", EventoDoHub("criada"))
	require.Equal(t, "status_changed", EventoDoHub("status"))
	require.Equal(t, "priority_changed", EventoDoHub("prioridade"))
	require.Equal(t, "feedback_given", EventoDoHub("resposta"))
	require.Equal(t, "reopened", EventoDoHub("reaberta"))
	// Os que o E-monitor não tinha passam com o nome do hub; a tela os nomeia.
	require.Equal(t, "retirada", EventoDoHub("retirada"))
	require.Equal(t, "sua_vez", EventoDoHub("sua_vez"))
}
```

Acrescentar a `sugestoes_test.go`:

```go
func TestImportarSugestao_SoAceitaAsDuasAcoes(t *testing.T) {
	acao := "importada"
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/platform/sugestoes/importar", r.URL.Path)
		jsonDe(w, 200, map[string]any{"acao": acao, "sugestaoId": "s1", "numero": 77})
	})
	c := New(h.srv.URL, "k")
	r, err := c.ImportarSugestao(context.Background(), autorAna, SugestaoImportada{IDOriginal: "u1", NumeroOriginal: 17})
	require.NoError(t, err)
	require.Equal(t, 77, r.Numero)
	require.Contains(t, h.corpos[0], `"idOriginal":"u1"`)
	require.Contains(t, h.corpos[0], `"anexoIds":[]`)
	require.Contains(t, h.corpos[0], `"comentarios":[]`)

	acao = "ignorado" // 200 com ação desconhecida NÃO é sucesso
	_, err = c.ImportarSugestao(context.Background(), autorAna, SugestaoImportada{IDOriginal: "u2"})
	e, _ := AsError(err)
	require.Equal(t, http.StatusBadGateway, e.Status)
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/hub/ -run 'TestTipo|TestEvento|TestImportar' -v`
Expected: FAIL — `undefined: TipoParaHub`, `undefined: SugestaoImportada`.

- [ ] **Step 3: Implementar**

`workers/internal/hub/sugestoes_vocabulario.go`:

```go
package hub

// O vocabulário do hub difere do daqui em UM tipo: o E-monitor diz `feature`,
// o hub diz `ideia` (spec do hub §4.1). Status, urgência, prioridade e esforço
// têm os mesmos valores dos dois lados; o hub tem um status a mais, `retirada`.

func TipoParaHub(t string) string {
	if t == "feature" {
		return "ideia"
	}
	return t
}

func TipoDoHub(t string) string {
	if t == "ideia" {
		return "feature"
	}
	return t
}

// Os eventos que o E-monitor já nomeava voltam com o nome daqui, para a
// timeline (`eventPhrase` no front) não mudar. Os outros passam com o nome do
// hub, e o front ganha frase para eles.
var eventosDoHub = map[string]string{
	"criada":     "created",
	"status":     "status_changed",
	"prioridade": "priority_changed",
	"resposta":   "feedback_given",
	"reaberta":   "reopened",
}

func EventoDoHub(t string) string {
	if v, ok := eventosDoHub[t]; ok {
		return v
	}
	return t
}
```

Acrescentar ao fim de `sugestoes.go`:

```go
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
```

- [ ] **Step 4: Rodar o pacote inteiro**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/hub/ -v 2>&1 | tail -5`
Expected: `ok  radiocheck/internal/hub`. Nenhum `FAIL`: os testes de antes do pacote (`codigo`, `conferir`, `emitir`, `ligar`) seguem verdes.

- [ ] **Step 5: Commit**

```bash
printf '%s\n' "feat(hub): vocabulario e importacao de sugestoes no cliente" "" "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" > "$TEMP/msg-t4.txt"
git add -- workers/internal/hub/sugestoes_vocabulario.go workers/internal/hub/sugestoes_vocabulario_test.go
git commit -F "$TEMP/msg-t4.txt" -- workers/internal/hub/sugestoes_vocabulario.go workers/internal/hub/sugestoes_vocabulario_test.go workers/internal/hub/sugestoes.go workers/internal/hub/sugestoes_test.go
```

---

### Task 5: Hub → JSON da tela daqui

**Files:**
- Create: `workers/internal/api/handlers/suggestions_hub_dto.go`
- Test: `workers/internal/api/handlers/suggestions_hub_dto_test.go`

A tela já lê `ref_num`, `type`, `title`, `description`, `status`, `requester_priority`, `dev_priority`, `effort`, `dev_feedback`, `awaiting_author`, `created_by`, `created_by_name`, `comment_count`, `unread`, `created_at`, `updated_at`, `comments[]` (`author_id`, `author_name`, `body`), `attachments[]` (`comment_id`, `content_type`) e `events[]` (`event_type`, `actor_name`, `to_value`). Os mesmos nomes saem daqui. Entram `origin`, `numero_original`, `from_dev`, `kind`, `filename`, `link_url` e `label`.

- [ ] **Step 1: Escrever o teste que falha**

```go
package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/hub"
)

func ptr[T any](v T) *T { return &v }

func TestDetalheDoHub_TraduzParaOVocabularioDaTela(t *testing.T) {
	quando := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	s := &hub.SugestaoHub{
		ID: "s1", Numero: 42, NumeroOriginal: ptr(17), Origem: "e-monitor",
		Tipo: "ideia", Titulo: "T", Descricao: "D", UrgenciaAutor: "alta", Status: "retirada",
		Prioridade: ptr("urgente"), Esforco: ptr("M"), Resposta: ptr("feito"), SuaVez: true,
		Autor: hub.PessoaHub{ID: "u1", Nome: "Ana"}, CreatedAt: quando, UltimaAtividadeEm: quando,
		Comentarios: []hub.ComentarioHub{{ID: "c1", Autor: hub.PessoaHub{ID: "g1", Nome: "Dereck"}, Corpo: "oi", DoGestor: true, CreatedAt: quando}},
		Anexos: []hub.AnexoHub{
			{ID: "a1", Tipo: "arquivo", ComentarioID: ptr("c1"), Nome: ptr("p.png"), MimeType: ptr("image/png"), Tamanho: ptr(int64(10))},
			{ID: "a2", Tipo: "link", URL: ptr("https://x.y/z"), Rotulo: ptr("doc")},
		},
		Eventos: []hub.EventoHub{{ID: "e1", Tipo: "criada", Ator: &hub.PessoaHub{Nome: "Ana"}}, {ID: "e2", Tipo: "status", Para: ptr("retirada")}},
	}

	d := detalheDoHub(s)
	b, _ := json.Marshal(d)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))

	require.Equal(t, float64(42), m["ref_num"])
	require.Equal(t, float64(17), m["numero_original"])
	require.Equal(t, "feature", m["type"])
	require.Equal(t, "retirada", m["status"])
	require.Equal(t, "alta", m["requester_priority"])
	require.Equal(t, "urgente", m["dev_priority"])
	require.Equal(t, "feito", m["dev_feedback"])
	require.Equal(t, true, m["awaiting_author"])
	require.Equal(t, "u1", m["created_by"])
	require.Equal(t, "Ana", m["created_by_name"])
	require.Equal(t, "e-monitor", m["origin"])
	require.Equal(t, float64(1), m["comment_count"])
	require.NotContains(t, string(b), "dev_notes")

	c := m["comments"].([]any)[0].(map[string]any)
	require.Equal(t, "g1", c["author_id"])
	require.Equal(t, "Dereck", c["author_name"])
	require.Equal(t, true, c["from_dev"])

	at := m["attachments"].([]any)
	require.Equal(t, "arquivo", at[0].(map[string]any)["kind"])
	require.Equal(t, "c1", at[0].(map[string]any)["comment_id"])
	require.Equal(t, "image/png", at[0].(map[string]any)["content_type"])
	require.Equal(t, "link", at[1].(map[string]any)["kind"])
	require.Equal(t, "https://x.y/z", at[1].(map[string]any)["link_url"])

	ev := m["events"].([]any)
	require.Equal(t, "created", ev[0].(map[string]any)["event_type"])
	require.Equal(t, "status_changed", ev[1].(map[string]any)["event_type"])
	require.Equal(t, "retirada", ev[1].(map[string]any)["to_value"])
}

func TestLinhaDoHub(t *testing.T) {
	l := linhaDoHub(hub.SugestaoLinhaHub{ID: "s1", Numero: 3, Origem: "hub", Tipo: "bug", Titulo: "T",
		Previa: "prévia…", Status: "nova", UrgenciaAutor: "baixa", Comentarios: 2, NaoLida: true,
		Autor: hub.PessoaHub{ID: "u1", Nome: "Ana"}})
	require.Equal(t, 3, l.RefNum)
	require.Equal(t, "prévia…", l.Description)
	require.Equal(t, 2, l.CommentCount)
	require.True(t, l.Unread)
	require.Equal(t, "hub", l.Origin)
}

func TestDetalheDoHub_ListasVaziasSaemComoArray(t *testing.T) {
	b, _ := json.Marshal(detalheDoHub(&hub.SugestaoHub{ID: "s1"}))
	require.Contains(t, string(b), `"comments":[]`)
	require.Contains(t, string(b), `"attachments":[]`)
	require.Contains(t, string(b), `"events":[]`)
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/api/handlers/ -run 'DoHub' -v`
Expected: FAIL — `undefined: detalheDoHub`.

- [ ] **Step 3: Implementar**

`workers/internal/api/handlers/suggestions_hub_dto.go`:

```go
package handlers

import (
	"time"

	"radiocheck/internal/hub"
)

/*
O JSON que a tela de sugestões daqui já consome, montado a partir do que o hub
devolve. Os nomes são os de `catalog.Suggestion` (ref_num, dev_feedback…), para o
front não reescrever leitura nenhuma; os campos novos são aditivos.

⚠️ Não há `dev_notes` aqui, nem como campo. A nota privada é do gestor e mora no
hub; a porta não a manda, e este tipo não teria onde pô-la.
*/

type sugestaoHubDTO struct {
	ID                string     `json:"id"`
	RefNum            int        `json:"ref_num"`
	NumeroOriginal    *int       `json:"numero_original,omitempty"`
	Origin            string     `json:"origin"`
	CreatedBy         string     `json:"created_by"`
	CreatedByName     string     `json:"created_by_name"`
	Title             string     `json:"title"`
	Description       string     `json:"description"`
	Type              string     `json:"type"`
	TargetScreen      *string    `json:"target_screen,omitempty"`
	RequesterPriority string     `json:"requester_priority"`
	Status            string     `json:"status"`
	DevPriority       *string    `json:"dev_priority,omitempty"`
	Effort            *string    `json:"effort,omitempty"`
	DevFeedback       *string    `json:"dev_feedback,omitempty"`
	AwaitingAuthor    bool       `json:"awaiting_author"`
	ResolvedAt        *time.Time `json:"resolved_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	CommentCount      int        `json:"comment_count"`
	Unread            bool       `json:"unread"`
}

type comentarioHubDTO struct {
	ID           string    `json:"id"`
	SuggestionID string    `json:"suggestion_id"`
	AuthorID     string    `json:"author_id"`
	AuthorName   string    `json:"author_name"`
	Body         string    `json:"body"`
	FromDev      bool      `json:"from_dev"`
	CreatedAt    time.Time `json:"created_at"`
}

// anexoHubDTO: `kind` = "arquivo" (imagem ou outro arquivo) ou "link".
type anexoHubDTO struct {
	ID           string    `json:"id"`
	SuggestionID string    `json:"suggestion_id"`
	CommentID    *string   `json:"comment_id,omitempty"`
	Kind         string    `json:"kind"`
	ContentType  string    `json:"content_type,omitempty"`
	SizeBytes    int64     `json:"size_bytes,omitempty"`
	Filename     string    `json:"filename,omitempty"`
	LinkURL      string    `json:"link_url,omitempty"`
	Label        string    `json:"label,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type eventoHubDTO struct {
	ID           string    `json:"id"`
	SuggestionID string    `json:"suggestion_id"`
	EventType    string    `json:"event_type"`
	ActorName    *string   `json:"actor_name,omitempty"`
	FromValue    *string   `json:"from_value,omitempty"`
	ToValue      *string   `json:"to_value,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type detalheHubDTO struct {
	sugestaoHubDTO
	Comments    []comentarioHubDTO `json:"comments"`
	Attachments []anexoHubDTO      `json:"attachments"`
	Events      []eventoHubDTO     `json:"events"`
}

func texto(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func linhaDoHub(l hub.SugestaoLinhaHub) sugestaoHubDTO {
	return sugestaoHubDTO{
		ID: l.ID, RefNum: l.Numero, Origin: l.Origem,
		CreatedBy: l.Autor.ID, CreatedByName: l.Autor.Nome,
		Title: l.Titulo, Description: l.Previa, Type: hub.TipoDoHub(l.Tipo),
		RequesterPriority: l.UrgenciaAutor, Status: l.Status, DevPriority: l.Prioridade,
		AwaitingAuthor: l.SuaVez, CreatedAt: l.CreatedAt, UpdatedAt: l.UltimaAtividadeEm,
		CommentCount: l.Comentarios, Unread: l.NaoLida,
	}
}

func detalheDoHub(s *hub.SugestaoHub) detalheHubDTO {
	d := detalheHubDTO{
		sugestaoHubDTO: sugestaoHubDTO{
			ID: s.ID, RefNum: s.Numero, NumeroOriginal: s.NumeroOriginal, Origin: s.Origem,
			CreatedBy: s.Autor.ID, CreatedByName: s.Autor.Nome,
			Title: s.Titulo, Description: s.Descricao, Type: hub.TipoDoHub(s.Tipo),
			TargetScreen: s.TelaAlvo, RequesterPriority: s.UrgenciaAutor, Status: s.Status,
			DevPriority: s.Prioridade, Effort: s.Esforco, DevFeedback: s.Resposta,
			AwaitingAuthor: s.SuaVez, ResolvedAt: s.ResolvidaEm,
			CreatedAt: s.CreatedAt, UpdatedAt: s.UltimaAtividadeEm,
			CommentCount: len(s.Comentarios),
		},
		Comments:    make([]comentarioHubDTO, 0, len(s.Comentarios)),
		Attachments: make([]anexoHubDTO, 0, len(s.Anexos)),
		Events:      make([]eventoHubDTO, 0, len(s.Eventos)),
	}
	for _, c := range s.Comentarios {
		d.Comments = append(d.Comments, comentarioHubDTO{
			ID: c.ID, SuggestionID: s.ID, AuthorID: c.Autor.ID, AuthorName: c.Autor.Nome,
			Body: c.Corpo, FromDev: c.DoGestor, CreatedAt: c.CreatedAt,
		})
	}
	for _, a := range s.Anexos {
		x := anexoHubDTO{ID: a.ID, SuggestionID: s.ID, CommentID: a.ComentarioID, CreatedAt: a.CreatedAt}
		if a.Tipo == "link" {
			x.Kind, x.LinkURL, x.Label = "link", texto(a.URL), texto(a.Rotulo)
		} else {
			x.Kind, x.ContentType, x.Filename = "arquivo", texto(a.MimeType), texto(a.Nome)
			if a.Tamanho != nil {
				x.SizeBytes = *a.Tamanho
			}
		}
		d.Attachments = append(d.Attachments, x)
	}
	for _, e := range s.Eventos {
		x := eventoHubDTO{ID: e.ID, SuggestionID: s.ID, EventType: hub.EventoDoHub(e.Tipo),
			FromValue: e.De, ToValue: e.Para, CreatedAt: e.CreatedAt}
		if e.Ator != nil {
			nome := e.Ator.Nome
			x.ActorName = &nome
		}
		d.Events = append(d.Events, x)
	}
	return d
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/api/handlers/ -run 'DoHub' -v`
Expected: `--- PASS` nos 3. Eles não usam banco: nada de SKIP.

- [ ] **Step 5: Commit**

```bash
printf '%s\n' "feat(sugestoes): traduz o detalhe do hub para o JSON da tela" "" "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" > "$TEMP/msg-t5.txt"
git add -- workers/internal/api/handlers/suggestions_hub_dto.go workers/internal/api/handlers/suggestions_hub_dto_test.go
git commit -F "$TEMP/msg-t5.txt" -- workers/internal/api/handlers/suggestions_hub_dto.go workers/internal/api/handlers/suggestions_hub_dto_test.go
```

---

### Task 6: O cache de 30 s do badge

**Files:**
- Create: `workers/internal/api/handlers/suggestions_hub_cache.go`
- Test: `workers/internal/api/handlers/suggestions_hub_cache_test.go`

- [ ] **Step 1: Teste que falha**

```go
package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCacheNaoLidas(t *testing.T) {
	agora := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	c := novoCacheNaoLidas(30 * time.Second)
	c.agora = func() time.Time { return agora }

	_, ok := c.pega("Ana@T.local")
	require.False(t, ok)

	c.guarda("ana@t.local", 4)
	n, ok := c.pega(" ANA@t.local ")
	require.True(t, ok)
	require.Equal(t, 4, n)

	agora = agora.Add(30 * time.Second)
	_, ok = c.pega("ana@t.local")
	require.False(t, ok, "venceu aos 30 s")

	c.guarda("ana@t.local", 1)
	c.esquece("ana@t.local")
	_, ok = c.pega("ana@t.local")
	require.False(t, ok)
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/api/handlers/ -run TestCacheNaoLidas -v`
Expected: FAIL — `undefined: novoCacheNaoLidas`.

- [ ] **Step 3: Implementar**

```go
package handlers

import (
	"strings"
	"sync"
	"time"
)

/*
cacheNaoLidas segura a contagem do badge por autor (spec do hub §4.4).

A sidebar pede `/suggestions/unread-count` de minuto em minuto, em toda aba
aberta de toda pessoa. Sem isto, cada pedido viraria uma ida ao hub, e o teto da
porta de lá é por IP — ou seja, desta plataforma inteira.

Em memória do processo, de propósito: são poucas dezenas de internos (o mapa não
cresce além disso), e perder o cache num restart custa só uma ida ao hub.
Quem ESCREVE (criar, comentar, marcar lida) esquece a entrada do autor, para o
badge não mentir 30 s depois de a própria pessoa agir.
*/
type cacheNaoLidas struct {
	mu    sync.Mutex
	ttl   time.Duration
	agora func() time.Time
	itens map[string]itemNaoLidas
}

type itemNaoLidas struct {
	n   int
	ate time.Time
}

func novoCacheNaoLidas(ttl time.Duration) *cacheNaoLidas {
	return &cacheNaoLidas{ttl: ttl, agora: time.Now, itens: map[string]itemNaoLidas{}}
}

func chaveDoAutor(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func (c *cacheNaoLidas) pega(email string) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.itens[chaveDoAutor(email)]
	if !ok || !c.agora().Before(it.ate) {
		return 0, false
	}
	return it.n, true
}

func (c *cacheNaoLidas) guarda(email string, n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.itens[chaveDoAutor(email)] = itemNaoLidas{n: n, ate: c.agora().Add(c.ttl)}
}

func (c *cacheNaoLidas) esquece(email string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.itens, chaveDoAutor(email))
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/api/handlers/ -run TestCacheNaoLidas -race -v`
Expected: `--- PASS: TestCacheNaoLidas`.

- [ ] **Step 5: Commit**

```bash
printf '%s\n' "feat(sugestoes): cache de 30 s do badge por autor" "" "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" > "$TEMP/msg-t6.txt"
git add -- workers/internal/api/handlers/suggestions_hub_cache.go workers/internal/api/handlers/suggestions_hub_cache_test.go
git commit -F "$TEMP/msg-t6.txt" -- workers/internal/api/handlers/suggestions_hub_cache.go workers/internal/api/handlers/suggestions_hub_cache_test.go
```

---

### Task 7: O handler novo — criar, listar, detalhe

**Files:**
- Create: `workers/internal/api/handlers/suggestions_hub.go`
- Test: `workers/internal/api/handlers/suggestions_hub_test.go`

- [ ] **Step 1: Escrever os testes que falham**

```go
package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
	"radiocheck/internal/hub"
	"radiocheck/internal/users"
)

// ── apoio: sem Postgres, de propósito (o handler não toca no banco daqui) ──

type usuariosFalsos map[uuid.UUID]*users.User

func (u usuariosFalsos) Get(_ context.Context, id uuid.UUID) (*users.User, error) {
	if x, ok := u[id]; ok {
		return x, nil
	}
	return nil, pgx.ErrNoRows
}

var idAna = uuid.MustParse("00000000-0000-0000-0000-0000000000a1")

type portaFalsa struct {
	srv     *httptest.Server
	pedidos []*http.Request
	corpos  []string
}

func novaPortaFalsa(t *testing.T, responde func(w http.ResponseWriter, r *http.Request)) *portaFalsa {
	t.Helper()
	p := &portaFalsa{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		p.pedidos = append(p.pedidos, r)
		p.corpos = append(p.corpos, string(b))
		responde(w, r)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func responde(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func handlerComHub(p *portaFalsa) *SuggestionsHubHandler {
	return NewSuggestionsHubHandler(hub.New(p.srv.URL, "chave-x"),
		usuariosFalsos{idAna: {ID: idAna, Email: "ana@t.local", Name: "Ana Paula"}})
}

func comoAna(r *http.Request) *http.Request {
	return r.WithContext(auth.ContextWithClaims(r.Context(), &auth.Claims{UserID: idAna, Role: "admin"}))
}

func comParam(r *http.Request, nome, valor string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(nome, valor)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

const idHub = "6650aaaaaaaaaaaaaaaaaaaa"

var sugestaoDoHub = map[string]any{"sugestao": map[string]any{
	"id": idHub, "numero": 42, "origem": "e-monitor", "tipo": "ideia", "titulo": "T",
	"descricao": "D", "status": "nova", "urgenciaAutor": "alta",
	"autor": map[string]any{"id": "u1", "nome": "Ana Paula"},
	"notas": "segredo do gestor",
}}

// ── criar ──

func TestHubHandler_Create(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 201, sugestaoDoHub) })
	h := handlerComHub(p)
	corpo := `{"title":" T ","description":" D ","type":"feature","target_screen":"Campanhas",
		"requester_priority":"alta","attachment_ids":["an1"],"client_key":"chave-do-form-1"}`
	w := httptest.NewRecorder()
	h.Create(w, comoAna(httptest.NewRequest("POST", "/suggestions", strings.NewReader(corpo))))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.Equal(t, "ana@t.local", p.pedidos[0].Header.Get("X-Hub-Autor-Email"))
	require.Equal(t, "Ana%20Paula", p.pedidos[0].Header.Get("X-Hub-Autor-Nome"))
	require.JSONEq(t, `{"chaveEnvio":"chave-do-form-1","tipo":"ideia","titulo":"T","descricao":"D",
		"telaAlvo":"Campanhas","urgenciaAutor":"alta","anexoIds":["an1"]}`, p.corpos[0])
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Equal(t, "feature", out["type"])
	require.Equal(t, float64(42), out["ref_num"])
	require.NotContains(t, w.Body.String(), "segredo")
}

func TestHubHandler_Create_ChaveInvalidaGanhaUmaNova(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 201, sugestaoDoHub) })
	w := httptest.NewRecorder()
	handlerComHub(p).Create(w, comoAna(httptest.NewRequest("POST", "/suggestions", strings.NewReader(
		`{"title":"T","description":"D","type":"bug","requester_priority":"baixa","client_key":"x y"}`))))
	require.Equal(t, http.StatusCreated, w.Code)
	var b map[string]any
	_ = json.Unmarshal([]byte(p.corpos[0]), &b)
	require.Regexp(t, `^[A-Za-z0-9_-]{8,64}$`, b["chaveEnvio"])
}

func TestHubHandler_Create_ValidaAntesDeIrAoHub(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 201, sugestaoDoHub) })
	w := httptest.NewRecorder()
	handlerComHub(p).Create(w, comoAna(httptest.NewRequest("POST", "/suggestions", strings.NewReader(
		`{"title":"","description":"D","type":"xpto","requester_priority":"alta"}`))))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.Empty(t, p.pedidos)
}

func TestHubHandler_Create_HubForaDoArNuncaVira201(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {})
	url := p.srv.URL
	p.srv.Close()
	h := NewSuggestionsHubHandler(hub.New(url, "k"), usuariosFalsos{idAna: {ID: idAna, Email: "ana@t.local"}})
	w := httptest.NewRecorder()
	h.Create(w, comoAna(httptest.NewRequest("POST", "/suggestions", strings.NewReader(
		`{"title":"T","description":"D","type":"bug","requester_priority":"alta"}`))))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), `"error"`)
	require.Contains(t, w.Body.String(), "tente de novo")
}

func TestHubHandler_SemHubConfiguradoResponde503(t *testing.T) {
	h := NewSuggestionsHubHandler(hub.New("", ""), usuariosFalsos{idAna: {ID: idAna, Email: "ana@t.local"}})
	w := httptest.NewRecorder()
	h.List(w, comoAna(httptest.NewRequest("GET", "/suggestions", nil)))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ── listar e detalhe ──

func TestHubHandler_List(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {
		responde(w, 200, map[string]any{"pagina": 1, "paginas": 1, "itens": []map[string]any{
			{"id": idHub, "numero": 7, "origem": "plura", "tipo": "ideia", "titulo": "T", "previa": "P",
				"status": "retirada", "urgenciaAutor": "media", "comentarios": 2, "naoLida": true,
				"autor": map[string]any{"id": "u1", "nome": "Ana"}}}})
	})
	w := httptest.NewRecorder()
	handlerComHub(p).List(w, comoAna(httptest.NewRequest("GET", "/suggestions?status=retirada", nil)))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "retirada", p.pedidos[0].URL.Query().Get("status"))
	var out struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out.Data, 1)
	require.Equal(t, float64(7), out.Data[0]["ref_num"])
	require.Equal(t, "feature", out.Data[0]["type"])
	require.Equal(t, "plura", out.Data[0]["origin"])
	require.Equal(t, true, out.Data[0]["unread"])
}

func TestHubHandler_List_StatusForaDoEnumNaoVai(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {
		responde(w, 200, map[string]any{"pagina": 1, "paginas": 1, "itens": []any{}})
	})
	w := httptest.NewRecorder()
	handlerComHub(p).List(w, comoAna(httptest.NewRequest("GET", "/suggestions?status=%24ne", nil)))
	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, p.pedidos[0].URL.Query().Get("status"))
	require.JSONEq(t, `{"data":[]}`, w.Body.String())
}

func TestHubHandler_Get(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 200, sugestaoDoHub) })
	w := httptest.NewRecorder()
	handlerComHub(p).Get(w, comParam(comoAna(httptest.NewRequest("GET", "/suggestions/"+idHub, nil)), "id", idHub))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "/api/platform/sugestoes/"+idHub, p.pedidos[0].URL.Path)
	require.NotContains(t, w.Body.String(), "segredo")
	require.NotContains(t, w.Body.String(), "dev_notes")
}

func TestHubHandler_Get_404DoHubPassa(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {
		responde(w, 404, map[string]any{"error": map[string]string{"code": "not_found", "message": "Sugestão não encontrada"}})
	})
	w := httptest.NewRecorder()
	handlerComHub(p).Get(w, comParam(comoAna(httptest.NewRequest("GET", "/x", nil)), "id", idHub))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.JSONEq(t, `{"error":"Sugestão não encontrada"}`, w.Body.String())
}

func TestHubHandler_IdMalformadoNemSaiDeCasa(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 200, sugestaoDoHub) })
	w := httptest.NewRecorder()
	handlerComHub(p).Get(w, comParam(comoAna(httptest.NewRequest("GET", "/x", nil)), "id", "../importar"))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, p.pedidos)
}

func TestHubHandler_SemClaims401(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {})
	w := httptest.NewRecorder()
	handlerComHub(p).List(w, httptest.NewRequest("GET", "/suggestions", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Empty(t, p.pedidos)
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/api/handlers/ -run 'TestHubHandler_' -v`
Expected: FAIL — `undefined: NewSuggestionsHubHandler`.

- [ ] **Step 3: Implementar (parte 1: estrutura, autor, criar, listar, detalhe)**

`workers/internal/api/handlers/suggestions_hub.go`:

```go
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/auth"
	"radiocheck/internal/hub"
	"radiocheck/internal/users"
)

/*
SuggestionsHubHandler serve `/v1/internal/suggestions*` desde 2026-10-26: a fila
MORA no hub (spec do hub 2026-10-26 §4). As rotas e o JSON são os mesmos que a
tela já consumia; o que mudou é a fonte — a porta `/api/platform/sugestoes`.

O `SuggestionsHandler` de antes (Postgres) continua no código e testado, mas saiu
do router: as tabelas `suggestions*` param de receber sugestão nova e ficam como
arquivo, de onde o `import-suggestions-hub` as leva para o hub.

⚠️ Toda persona aqui é AUTOR. A triagem é só no hub, e a pessoa gestora vê na
tela daqui o aviso "A fila agora vive no hub" (`FilaNoHub.jsx`). Por isso PATCH,
summary e a URL pré-assinada respondem 410 (`Gone`).

⚠️ Sem hub configurado (sem HUB_URL/HUB_PLATFORM_KEY), toda rota responde 503
pela `ErrNotConfigured` — de propósito: gravar no Postgres em silêncio quando a
variável some faria a fila voltar a ter duas fontes sem ninguém ver.
*/

// autorLoader é o pedaço do users.Repo que este handler usa: o e-mail e o nome
// de quem está logado (o JWT daqui não carrega e-mail). Interface para o teste
// não precisar de Postgres.
type autorLoader interface {
	Get(ctx context.Context, id uuid.UUID) (*users.User, error)
}

type SuggestionsHubHandler struct {
	Hub      *hub.Client
	Users    autorLoader
	naoLidas *cacheNaoLidas
}

func NewSuggestionsHubHandler(h *hub.Client, u autorLoader) *SuggestionsHubHandler {
	return &SuggestionsHubHandler{Hub: h, Users: u, naoLidas: novoCacheNaoLidas(30 * time.Second)}
}

// O id do hub é um ObjectId. Conferir aqui faz o malformado nem sair de casa e
// mantém o `Content-Disposition` do proxy livre de texto vindo da URL.
var idDoHub = regexp.MustCompile(`^[a-f0-9]{24}$`)

// A chave de idempotência que o front gera ao abrir o formulário (o formato é o
// que o hub aceita). Sem chave válida, uma nova: o envio funciona, só não é
// reenviável sem duplicar.
var chaveDeEnvioValida = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

func chaveDeEnvio(v string) string {
	if chaveDeEnvioValida.MatchString(v) {
		return v
	}
	return uuid.NewString()
}

const triagemNoHub = "A triagem agora é feita na Central de Clientes (hub)."

func erroJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func escreveErroDoHub(w http.ResponseWriter, err error) {
	if e, ok := hub.AsError(err); ok {
		erroJSON(w, e.Status, e.Message)
		return
	}
	erroJSON(w, http.StatusInternalServerError, "erro interno")
}

// autorOu resolve quem está logado ou responde o erro. 401 só sem sessão: um 401
// por falha do banco jogaria a pessoa para o login (interceptor do axios).
func (h *SuggestionsHubHandler) autorOu(w http.ResponseWriter, r *http.Request) (hub.Autor, bool) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		erroJSON(w, http.StatusUnauthorized, "unauthorized")
		return hub.Autor{}, false
	}
	u, err := h.Users.Get(r.Context(), claims.UserID)
	if err != nil || u == nil || strings.TrimSpace(u.Email) == "" {
		erroJSON(w, http.StatusInternalServerError, "erro interno")
		return hub.Autor{}, false
	}
	return hub.Autor{Email: u.Email, Nome: u.Name}, true
}

func idDaRota(w http.ResponseWriter, r *http.Request, nome string) (string, bool) {
	id := chi.URLParam(r, nome)
	if !idDoHub.MatchString(id) {
		erroJSON(w, http.StatusNotFound, "sugestão não encontrada")
		return "", false
	}
	return id, true
}

// Create — POST /suggestions. Os anexos já subiram (POST /suggestions/attachments)
// e chegam por `attachment_ids`.
func (h *SuggestionsHubHandler) Create(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	var body struct {
		Title             string   `json:"title"`
		Description       string   `json:"description"`
		Type              string   `json:"type"`
		TargetScreen      *string  `json:"target_screen"`
		RequesterPriority string   `json:"requester_priority"`
		AttachmentIDs     []string `json:"attachment_ids"`
		ClientKey         string   `json:"client_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		erroJSON(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	errs := map[string]string{}
	if strings.TrimSpace(body.Title) == "" {
		errs["title"] = "título é obrigatório"
	}
	if strings.TrimSpace(body.Description) == "" {
		errs["description"] = "descrição é obrigatória"
	}
	if !suggestionTypes[body.Type] {
		errs["type"] = "tipo inválido"
	}
	if !suggestionReqPriorities[body.RequesterPriority] {
		errs["requester_priority"] = "prioridade inválida"
	}
	if len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": errs})
		return
	}
	var tela *string
	if body.TargetScreen != nil && strings.TrimSpace(*body.TargetScreen) != "" {
		t := strings.TrimSpace(*body.TargetScreen)
		tela = &t
	}
	s, err := h.Hub.CriarSugestao(r.Context(), a, hub.NovaSugestaoHub{
		ChaveEnvio:    chaveDeEnvio(body.ClientKey),
		Tipo:          hub.TipoParaHub(body.Type),
		Titulo:        strings.TrimSpace(body.Title),
		Descricao:     strings.TrimSpace(body.Description),
		TelaAlvo:      tela,
		UrgenciaAutor: body.RequesterPriority,
		AnexoIDs:      body.AttachmentIDs,
	})
	if err != nil {
		escreveErroDoHub(w, err)
		return
	}
	h.naoLidas.esquece(a.Email)
	writeJSON(w, http.StatusCreated, detalheDoHub(s))
}

// List — GET /suggestions. As do autor, de todas as origens (spec do hub §3.4).
func (h *SuggestionsHubHandler) List(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if !suggestionStatuses[status] && status != "retirada" {
		status = "" // fora do enum é descartado, nunca repassado
	}
	itens, err := h.Hub.ListarSugestoes(r.Context(), a, status)
	if err != nil {
		escreveErroDoHub(w, err)
		return
	}
	out := make([]sugestaoHubDTO, 0, len(itens))
	for _, it := range itens {
		out = append(out, linhaDoHub(it))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// Get — GET /suggestions/{id}. O hub dá 404 se não for do autor. Não marca
// como lida (a tela chama /read ao abrir).
func (h *SuggestionsHubHandler) Get(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	id, ok := idDaRota(w, r, "id")
	if !ok {
		return
	}
	s, err := h.Hub.DetalheSugestao(r.Context(), a, id)
	if err != nil {
		escreveErroDoHub(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detalheDoHub(s))
}
```

> `suggestionTypes`, `suggestionReqPriorities` e `suggestionStatuses` já existem em `suggestions.go`, no mesmo pacote. Não duplicar.

- [ ] **Step 4: Rodar e ver passar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/api/handlers/ -run 'TestHubHandler_' -v`
Expected: `--- PASS` nos 12 testes desta task.

- [ ] **Step 5: Mutação do enum de status**

Com cópia em `$TEMP`, apague a linha `status = "" // fora do enum…`. Esperado: `TestHubHandler_List_StatusForaDoEnumNaoVai` FALHA. Restaure e confira o `git diff --stat`.

- [ ] **Step 6: Commit**

```bash
printf '%s\n' "feat(sugestoes): handler que le e grava a fila no hub (criar, listar, detalhe)" "" "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" > "$TEMP/msg-t7.txt"
git add -- workers/internal/api/handlers/suggestions_hub.go workers/internal/api/handlers/suggestions_hub_test.go
git commit -F "$TEMP/msg-t7.txt" -- workers/internal/api/handlers/suggestions_hub.go workers/internal/api/handlers/suggestions_hub_test.go
```

---

### Task 8: O handler novo — comentar, anexos, lida, badge e as rotas que se foram

**Files:**
- Modify: `workers/internal/api/handlers/suggestions_hub.go`
- Test: `workers/internal/api/handlers/suggestions_hub_test.go` (acrescentar)

- [ ] **Step 1: Escrever os testes que falham**

Acrescentar ao fim de `suggestions_hub_test.go` (somar `"bytes"` e `"mime/multipart"` ao bloco de imports):

```go
func multipartComImagem(t *testing.T, mime string, dados []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="file"; filename="print.png"`}
	h["Content-Type"] = []string{mime}
	part, err := mw.CreatePart(h)
	require.NoError(t, err)
	_, _ = part.Write(dados)
	require.NoError(t, mw.Close())
	return &buf, mw.FormDataContentType()
}

func TestHubHandler_AddComment(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 201, sugestaoDoHub) })
	h := handlerComHub(p)
	h.naoLidas.guarda("ana@t.local", 9)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/x", strings.NewReader(`{"body":" oi ","attachment_ids":["an1"],"client_key":"fala-123456"}`))
	h.AddComment(w, comParam(comoAna(req), "id", idHub))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.Equal(t, "/api/platform/sugestoes/"+idHub+"/comentarios", p.pedidos[0].URL.Path)
	require.JSONEq(t, `{"chaveEnvio":"fala-123456","corpo":"oi","anexoIds":["an1"]}`, p.corpos[0])
	_, ainda := h.naoLidas.pega("ana@t.local")
	require.False(t, ainda, "comentar esquece o cache do badge")
}

func TestHubHandler_AddComment_SoImagemVale_VazioNao(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 201, sugestaoDoHub) })
	h := handlerComHub(p)

	w := httptest.NewRecorder()
	h.AddComment(w, comParam(comoAna(httptest.NewRequest("POST", "/x", strings.NewReader(`{"body":"  "}`))), "id", idHub))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.Empty(t, p.pedidos)

	w = httptest.NewRecorder()
	h.AddComment(w, comParam(comoAna(httptest.NewRequest("POST", "/x", strings.NewReader(`{"body":"","attachment_ids":["an1"]}`))), "id", idHub))
	require.Equal(t, http.StatusCreated, w.Code)
}

// bucket + porta que respondem o fluxo de três passos.
func portaComBucket(t *testing.T) (*portaFalsa, *[]byte) {
	recebido := &[]byte{}
	bucket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			*recebido, _ = io.ReadAll(r.Body)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("BYTES-DO-GCS"))
	}))
	t.Cleanup(bucket.Close)
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/platform/sugestoes/anexos/upload-url":
			responde(w, 201, map[string]any{"anexoId": "an1", "uploadUrl": bucket.URL + "/obj"})
		case strings.HasSuffix(r.URL.Path, "/confirm"):
			responde(w, 200, map[string]any{"anexo": map[string]any{"id": "an1"}})
		case strings.HasSuffix(r.URL.Path, "/arquivo"):
			http.Redirect(w, r, bucket.URL+"/obj?assinado=1", http.StatusFound)
		default:
			responde(w, 201, sugestaoDoHub)
		}
	})
	return p, recebido
}

func TestHubHandler_StageAttachment(t *testing.T) {
	p, recebido := portaComBucket(t)
	corpo, ct := multipartComImagem(t, "image/png", []byte("PNG!"))
	req := httptest.NewRequest("POST", "/suggestions/attachments", corpo)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	handlerComHub(p).StageAttachment(w, comoAna(req))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.JSONEq(t, `{"id":"an1"}`, w.Body.String())
	require.Equal(t, []byte("PNG!"), *recebido)
	require.JSONEq(t, `{"nome":"print.png","mimeType":"image/png","tamanho":4}`, p.corpos[0])
}

func TestHubHandler_StageAttachment_RecusaNaoImagem(t *testing.T) {
	p, _ := portaComBucket(t)
	corpo, ct := multipartComImagem(t, "application/pdf", []byte("%PDF"))
	req := httptest.NewRequest("POST", "/suggestions/attachments", corpo)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	handlerComHub(p).StageAttachment(w, comoAna(req))
	require.Equal(t, http.StatusUnsupportedMediaType, w.Code)
	require.Empty(t, p.pedidos)
}

func TestHubHandler_UploadAttachmentLegado_ViraFalaComAImagem(t *testing.T) {
	p, _ := portaComBucket(t)
	corpo, ct := multipartComImagem(t, "image/png", []byte("PNG!"))
	req := httptest.NewRequest("POST", "/x", corpo)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	handlerComHub(p).UploadAttachment(w, comParam(comoAna(req), "id", idHub))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	ultimo := p.pedidos[len(p.pedidos)-1]
	require.Equal(t, "/api/platform/sugestoes/"+idHub+"/comentarios", ultimo.URL.Path)
	require.Contains(t, p.corpos[len(p.corpos)-1], `"anexoIds":["an1"]`)
}

func TestHubHandler_ProxyAttachment(t *testing.T) {
	p, _ := portaComBucket(t)
	aid := "6650bbbbbbbbbbbbbbbbbbbb"
	w := httptest.NewRecorder()
	handlerComHub(p).ProxyAttachment(w, comParam(comoAna(httptest.NewRequest("GET", "/x", nil)), "aid", aid))

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "BYTES-DO-GCS", w.Body.String())
	require.Equal(t, "image/png", w.Header().Get("Content-Type"))
	require.Equal(t, `inline; filename="`+aid+`"`, w.Header().Get("Content-Disposition"))
	require.Equal(t, "private, max-age=300", w.Header().Get("Cache-Control"))
}

func TestHubHandler_UnreadCount_CacheEEsquece(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {
		responde(w, 200, map[string]any{"naoLidas": 2})
	})
	h := handlerComHub(p)
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		h.UnreadCount(w, comoAna(httptest.NewRequest("GET", "/suggestions/unread-count", nil)))
		require.JSONEq(t, `{"count":2}`, w.Body.String())
	}
	require.Len(t, p.pedidos, 1, "três pedidos, uma ida ao hub")

	w := httptest.NewRecorder()
	h.MarkRead(w, comParam(comoAna(httptest.NewRequest("POST", "/x", nil)), "id", idHub))
	require.Equal(t, http.StatusNoContent, w.Code)

	w = httptest.NewRecorder()
	h.UnreadCount(w, comoAna(httptest.NewRequest("GET", "/suggestions/unread-count", nil)))
	require.Len(t, p.pedidos, 3, "marcar lida esquece o cache: lida + nova contagem")
}

func TestHubHandler_UnreadCount_ErroDoHubNaoViraZero(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	w := httptest.NewRecorder()
	handlerComHub(p).UnreadCount(w, comoAna(httptest.NewRequest("GET", "/x", nil)))
	require.Equal(t, http.StatusBadGateway, w.Code)
	require.NotContains(t, w.Body.String(), `"count"`)
}

func TestHubHandler_TriagemFoiParaOHub(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {})
	h := handlerComHub(p)
	for _, f := range []http.HandlerFunc{h.Gone} {
		w := httptest.NewRecorder()
		f(w, comoAna(httptest.NewRequest("PATCH", "/suggestions/"+idHub, strings.NewReader(`{"status":"concluida"}`))))
		require.Equal(t, http.StatusGone, w.Code)
		require.Contains(t, w.Body.String(), "Central de Clientes")
	}
	require.Empty(t, p.pedidos)
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/api/handlers/ -run 'TestHubHandler_' -v`
Expected: FAIL — `h.AddComment undefined`, `h.StageAttachment undefined`…

- [ ] **Step 3: Implementar (parte 2)**

Somar `"io"` ao bloco de imports de `suggestions_hub.go` e acrescentar ao fim:

```go
// AddComment — POST /suggestions/{id}/comments. Texto, anexos (já subidos) ou
// os dois; vazio dos dois é 422.
func (h *SuggestionsHubHandler) AddComment(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	id, ok := idDaRota(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Body          string   `json:"body"`
		AttachmentIDs []string `json:"attachment_ids"`
		ClientKey     string   `json:"client_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		erroJSON(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	corpo := strings.TrimSpace(body.Body)
	if corpo == "" && len(body.AttachmentIDs) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": map[string]string{"body": "mensagem vazia"}})
		return
	}
	s, err := h.Hub.ComentarSugestao(r.Context(), a, id, hub.NovoComentarioHub{
		ChaveEnvio: chaveDeEnvio(body.ClientKey), Corpo: corpo, AnexoIDs: body.AttachmentIDs,
	})
	if err != nil {
		escreveErroDoHub(w, err)
		return
	}
	h.naoLidas.esquece(a.Email)
	writeJSON(w, http.StatusCreated, detalheDoHub(s))
}

// imagemDoFormulario lê o campo `file` com as MESMAS regras de antes (imagem,
// 10 MB). O hub aceita mais tipos; esta tela continua só com imagem.
func imagemDoFormulario(w http.ResponseWriter, r *http.Request) (nome, mime string, dados []byte, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, suggestionAttachmentMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		erroJSON(w, http.StatusBadRequest, "envio inválido (limite 10MB)")
		return "", "", nil, false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		erroJSON(w, http.StatusBadRequest, "campo 'file' obrigatório")
		return "", "", nil, false
	}
	defer file.Close()
	mime = strings.ToLower(strings.TrimSpace(header.Header.Get("Content-Type")))
	if _, aceito := suggestionImageMIME[mime]; !aceito {
		erroJSON(w, http.StatusUnsupportedMediaType, "formato não suportado (use png, jpeg, webp ou gif)")
		return "", "", nil, false
	}
	dados, err = io.ReadAll(io.LimitReader(file, suggestionAttachmentMaxBytes+1))
	if err != nil {
		erroJSON(w, http.StatusBadRequest, "não consegui ler o arquivo")
		return "", "", nil, false
	}
	if len(dados) > suggestionAttachmentMaxBytes {
		erroJSON(w, http.StatusRequestEntityTooLarge, "imagem acima de 10MB")
		return "", "", nil, false
	}
	return header.Filename, mime, dados, true
}

// StageAttachment — POST /suggestions/attachments. Sobe a imagem "sem dono" no
// hub e devolve `{id}`; a criação ou o comentário a amarram por
// `attachment_ids`. É o desenho que mata o defeito nº 1 do módulo antigo
// (upload falha depois de criar → reenviar duplica).
func (h *SuggestionsHubHandler) StageAttachment(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	nome, mime, dados, ok := imagemDoFormulario(w, r)
	if !ok {
		return
	}
	id, err := h.Hub.SubirAnexoSugestao(r.Context(), a, nome, mime, dados)
	if err != nil {
		escreveErroDoHub(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// UploadAttachment — POST /suggestions/{id}/attachments, a rota de antes. A
// tela nova não a chama mais; continua viva para quem a chamar (spec do hub
// §4.3): no hub, anexo sempre pertence a uma fala, então a imagem vira uma fala
// só com ela.
func (h *SuggestionsHubHandler) UploadAttachment(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	id, ok := idDaRota(w, r, "id")
	if !ok {
		return
	}
	nome, mime, dados, ok := imagemDoFormulario(w, r)
	if !ok {
		return
	}
	anexo, err := h.Hub.SubirAnexoSugestao(r.Context(), a, nome, mime, dados)
	if err != nil {
		escreveErroDoHub(w, err)
		return
	}
	if _, err := h.Hub.ComentarSugestao(r.Context(), a, id, hub.NovoComentarioHub{
		ChaveEnvio: uuid.NewString(), AnexoIDs: []string{anexo},
	}); err != nil {
		escreveErroDoHub(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": anexo})
}

// ProxyAttachment — GET /suggestions/attachments/{aid}. Os BYTES passam pela API
// (com JWT), nunca a URL assinada: é o incidente de 2026-07-13 (host do bucket
// inalcançável pelo navegador) e o motivo do proxy do áudio de evidência.
func (h *SuggestionsHubHandler) ProxyAttachment(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	aid, ok := idDaRota(w, r, "aid")
	if !ok {
		return
	}
	corpo, ct, err := h.Hub.AbrirAnexoSugestao(r.Context(), a, aid)
	if err != nil {
		escreveErroDoHub(w, err)
		return
	}
	defer corpo.Close()
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", `inline; filename="`+aid+`"`)
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = io.Copy(w, corpo) // streaming; cliente que desconecta no meio não tem o que receber
}

// MarkRead — POST /suggestions/{id}/read.
func (h *SuggestionsHubHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	id, ok := idDaRota(w, r, "id")
	if !ok {
		return
	}
	if _, err := h.Hub.MarcarSugestaoLida(r.Context(), a, id); err != nil {
		escreveErroDoHub(w, err)
		return
	}
	h.naoLidas.esquece(a.Email)
	w.WriteHeader(http.StatusNoContent)
}

// UnreadCount — GET /suggestions/unread-count, com o cache de 30 s. Erro do hub
// é erro: devolver `{count:0}` apagaria o badge de quem tem novidade (zero não
// é ausência). O react-query mantém o último valor bom.
func (h *SuggestionsHubHandler) UnreadCount(w http.ResponseWriter, r *http.Request) {
	a, ok := h.autorOu(w, r)
	if !ok {
		return
	}
	if n, ok := h.naoLidas.pega(a.Email); ok {
		writeJSON(w, http.StatusOK, map[string]int{"count": n})
		return
	}
	n, err := h.Hub.SugestoesNaoLidas(r.Context(), a)
	if err != nil {
		escreveErroDoHub(w, err)
		return
	}
	h.naoLidas.guarda(a.Email, n)
	writeJSON(w, http.StatusOK, map[string]int{"count": n})
}

// Gone — PATCH /suggestions/{id}, GET /suggestions/summary e
// GET /suggestions/attachments/{aid}/url. A triagem e os KPIs da Central de
// Comando moram no hub; a URL pré-assinada nunca devia ir ao navegador.
func (h *SuggestionsHubHandler) Gone(w http.ResponseWriter, _ *http.Request) {
	erroJSON(w, http.StatusGone, triagemNoHub)
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/api/handlers/ -run 'TestHubHandler_|DoHub|TestCacheNaoLidas' -race -v 2>&1 | tail -30`
Expected: todos os `TestHubHandler_*` com `--- PASS`. `grep -cE -- '--- SKIP'` dá 0 nessa seleção.

- [ ] **Step 5: Mutação do "erro não vira zero"**

Com cópia em `$TEMP`, troque o bloco de erro de `UnreadCount` por `writeJSON(w, 200, map[string]int{"count": 0}); return`. Esperado: `TestHubHandler_UnreadCount_ErroDoHubNaoViraZero` FALHA. Restaure e confira o `git diff --stat`.

- [ ] **Step 6: Commit**

```bash
printf '%s\n' "feat(sugestoes): comentar, anexos, lida e badge pela porta do hub" "" "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" > "$TEMP/msg-t8.txt"
git commit -F "$TEMP/msg-t8.txt" -- workers/internal/api/handlers/suggestions_hub.go workers/internal/api/handlers/suggestions_hub_test.go
```

---

### Task 9: Ligar no router e no `main.go`

**Files:**
- Modify: `workers/internal/api/router.go:64` e o bloco das linhas 729-749
- Modify: `workers/cmd/api/main.go:679-684`

- [ ] **Step 1: `Deps`**

Em `router.go`, logo depois da linha `Suggestions           *handlers.SuggestionsHandler`, acrescentar:

```go
	// SuggestionsHub: a fila de sugestões mora no hub desde 2026-10-26 (spec do
	// hub 2026-10-26). É quem o router usa; `Suggestions` (Postgres) ficou no
	// código e não é mais roteado.
	SuggestionsHub *handlers.SuggestionsHubHandler
```

- [ ] **Step 2: As rotas**

Substituir o bloco inteiro que começa em `// Central de Sugestões — admin/operator entram;` e termina no `}` do `if d.Suggestions != nil {` por:

```go
				// Central de Sugestões — admin/operator entram. Desde 2026-10-26 a
				// fila MORA no hub: o handler fala com a porta
				// /api/platform/sugestoes e devolve o JSON de sempre. Rotas
				// método a método (sem r.Route), pela mesma razão de /materials
				// acima; as estáticas vêm ANTES de /{id} — inclusive o
				// POST /suggestions/attachments, que é o upload "sem dono".
				// O SuggestionsHandler de antes (Postgres) não é mais roteado.
				// Doc: docs/features/suggestions-board.md.
				if d.SuggestionsHub != nil {
					r.Post("/suggestions", d.SuggestionsHub.Create)
					r.Get("/suggestions", d.SuggestionsHub.List)
					r.Get("/suggestions/summary", d.SuggestionsHub.Gone)
					r.Get("/suggestions/unread-count", d.SuggestionsHub.UnreadCount)
					r.Post("/suggestions/attachments", d.SuggestionsHub.StageAttachment)
					r.Get("/suggestions/attachments/{aid}/url", d.SuggestionsHub.Gone)
					r.Get("/suggestions/attachments/{aid}", d.SuggestionsHub.ProxyAttachment) // bytes pela API — o navegador nunca recebe URL de bucket
					r.Get("/suggestions/{id}", d.SuggestionsHub.Get)
					r.Patch("/suggestions/{id}", d.SuggestionsHub.Gone) // triagem é no hub
					r.Post("/suggestions/{id}/comments", d.SuggestionsHub.AddComment)
					r.Post("/suggestions/{id}/attachments", d.SuggestionsHub.UploadAttachment)
					r.Post("/suggestions/{id}/read", d.SuggestionsHub.MarkRead)
				}
```

- [ ] **Step 3: `main.go`**

Substituir:

```go
		Suggestions: &handlers.SuggestionsHandler{
			Repo:     catalog.NewSuggestions(pool),
			Users:    usersRepo,
			Storage:  s3Client,
			DevEmail: cfg.SuggestionsDevEmail,
		},
```

por:

```go
		// A fila de sugestões mora no hub (spec do hub 2026-10-26). Sem
		// HUB_URL/HUB_PLATFORM_KEY o handler responde 503 em toda rota — de
		// propósito: voltar a gravar no Postgres em silêncio faria a fila ter
		// duas fontes de novo. `cfg.SuggestionsDevEmail` continua sendo lido
		// (o handler antigo, fora do router, e o front ainda o espelham).
		SuggestionsHub: handlers.NewSuggestionsHubHandler(hub.New(cfg.HubURL, cfg.HubPlatformKey), usersRepo),
```

E, antes de `deps := api.Deps{` (linha ~588), acrescentar o aviso de boot:

```go
	if !hub.New(cfg.HubURL, cfg.HubPlatformKey).Configured() {
		logger.Warn("sugestões: HUB_URL/HUB_PLATFORM_KEY ausentes — /suggestions responde 503 até configurar")
	}
```

- [ ] **Step 4: Compilar, vet e conferir import órfão**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go build ./... && go vet ./internal/api/... ./internal/hub/... ./cmd/api/`
Expected: sem saída. Se o compilador acusar `catalog` ou `s3Client` sem uso em `main.go`, **não apague**: ambos continuam usados por outros handlers. O erro só aparece se algo foi apagado a mais.

- [ ] **Step 5: Os testes de router de antes continuam verdes**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/api/ -v 2>&1 | grep -E '^(--- |ok|FAIL)' | tail -20`
Expected: `ok  radiocheck/internal/api`. Conte o SKIP: `go test ./internal/api/ -v 2>&1 | grep -cE -- '--- SKIP' || true`. Anote o número para a Task 13.

- [ ] **Step 6: Commit**

```bash
printf '%s\n' "feat(sugestoes): router e main apontam as rotas de sugestoes para o hub" "" "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" > "$TEMP/msg-t9.txt"
git commit -F "$TEMP/msg-t9.txt" -- workers/internal/api/router.go workers/cmd/api/main.go
```

---

### Task 10: O front — anexo antes, chave de envio, aviso, `retirada`, selo, arquivo e link

**Files:**
- Modify: `frontend/src/api/hooks.js` (bloco das sugestões, linhas ~1509-1537)
- Modify: `frontend/src/pages/suggestions/constants.js`
- Modify: `frontend/src/pages/suggestions/SuggestionPills.jsx`
- Create: `frontend/src/pages/suggestions/AttachmentItem.jsx`
- Create: `frontend/src/pages/suggestions/FilaNoHub.jsx`
- Modify: `frontend/src/pages/AdminSuggestionsPage.jsx`
- Modify: `frontend/src/pages/suggestions/SuggestionCreateModal.jsx`
- Modify: `frontend/src/pages/suggestions/SuggestionThread.jsx`
- Modify: `frontend/src/pages/suggestions/SuggestionDetail.jsx`
- Modify: `frontend/src/pages/suggestions/MinhasSugestoes.jsx`
- Modify: `frontend/src/pages/AdminSuggestionsPage.css`

O teste desta task é o Playwright da Task 11 (TDD invertido por limitação do repo: sem vitest). Escreva a Task 11 primeiro, veja falhar, e volte aqui.

- [ ] **Step 1: `hooks.js`**

Substituir o `useAddSuggestionComment` existente por:

```js
// Comentário: texto e/ou anexos JÁ SUBIDOS (ids de useStageSuggestionAttachment).
// `clientKey` torna o reenvio inofensivo (o hub não duplica a fala).
export function useAddSuggestionComment() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body, attachmentIds, clientKey }) => api.post(`/suggestions/${id}/comments`, {
      body, attachment_ids: attachmentIds?.length ? attachmentIds : undefined, client_key: clientKey,
    }).then(r => r.data),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['suggestion', vars.id] })
      qc.invalidateQueries({ queryKey: ['suggestions'] })
    },
  })
}
```

E, logo depois de `useUploadSuggestionAttachment` (que fica como está), acrescentar:

```js
// Sobe a imagem "sem dono" e devolve { id }. A criação ou o comentário a amarram
// por attachment_ids — subir ANTES é o que impede o reenvio de duplicar a
// sugestão quando o upload falha no meio (a fila mora no hub, ver
// docs/features/suggestions-board.md).
export function useStageSuggestionAttachment() {
  return useMutation({
    mutationFn: (file) => {
      const fd = new FormData()
      fd.append('file', file)
      return api.post('/suggestions/attachments', fd, {
        headers: { 'Content-Type': 'multipart/form-data' },
      }).then(r => r.data)
    },
  })
}
```

- [ ] **Step 2: `constants.js`**

No objeto `STATUS`, depois de `recusada`, acrescentar:

```js
  retirada:     { label: 'Retirada',     hint: 'Quem abriu retirou a sugestão' },
```

Trocar `STATUS_ORDER` por:

```js
export const STATUS_ORDER = ['nova', 'em_analise', 'aceita', 'em_progresso', 'concluida', 'recusada', 'retirada']
```

No fim do arquivo, acrescentar:

```js
// De onde a sugestão veio. A fila mora no hub e junta as três plataformas; a
// daqui (e-monitor) não ganha selo.
export const ORIGIN = {
  hub:   { label: 'via Hub' },
  plura: { label: 'via Plura' },
}

// A Central de Clientes (o portal, não a API). Default funciona sem env, como o
// VITE_SUGGESTIONS_DEV_EMAIL.
export const HUB_PORTAL_URL = (import.meta.env.VITE_HUB_PORTAL_URL || 'https://clientes.emidiastec.com.br').replace(/\/$/, '')
```

- [ ] **Step 3: `SuggestionPills.jsx`**

Trocar o import por `import { STATUS, TYPE, REQ_PRIORITY, DEV_PRIORITY, EFFORT, ORIGIN } from './constants'` e acrescentar no fim:

```jsx
// Selo de origem: só quando a sugestão veio de outra plataforma.
export function OriginPill({ value }) {
  const meta = ORIGIN[value]
  if (!meta) return null
  return <span className="sug-pill sug-pill--origin sug-pill--sm" title="Veio de outra plataforma">{meta.label}</span>
}
```

- [ ] **Step 4: `AttachmentItem.jsx` (novo)**

```jsx
import api from '../../api/client'
import AttachmentImage from './AttachmentImage'

// Um anexo da sugestão, como o hub o guarda: imagem (miniatura + lightbox),
// outro arquivo (baixa pelo proxy) ou link. Os de antes não traziam `kind`:
// valem como arquivo.
// ⚠️ A "miniatura" é a imagem INTEIRA pelo proxy de bytes, como sempre foi. Não
// peça `?thumb=1`: o que sobe por aqui não tem WebP (o hub degrada para
// `temThumb: false`) e o pedido de miniatura dá 404.
export const isImage = (a) => (a?.kind ?? 'arquivo') === 'arquivo' && (a?.content_type || '').startsWith('image/')

function hostOf(u) {
  try { return new URL(u).host } catch { return u }
}

// Baixa pelos BYTES da API (com JWT), nunca por URL de bucket — o mesmo motivo
// do AttachmentImage.
async function baixar(att) {
  const r = await api.get(`/suggestions/attachments/${att.id}`, { responseType: 'blob' })
  const url = URL.createObjectURL(r.data)
  const a = document.createElement('a')
  a.href = url
  a.download = att.filename || 'anexo'
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export default function AttachmentItem({ att, onOpenImage, large }) {
  if (att.kind === 'link') {
    return (
      <a className="sug-att-link" href={att.link_url} target="_blank" rel="noopener noreferrer">
        {att.label || hostOf(att.link_url)}
      </a>
    )
  }
  if (isImage(att)) {
    return (
      <button type="button" className={`sug-att-thumb${large ? ' sug-att-thumb--lg' : ''}`} onClick={() => onOpenImage?.(att)}>
        <AttachmentImage att={att} />
      </button>
    )
  }
  return (
    <button type="button" className="sug-att-file" onClick={() => { baixar(att).catch(() => {}) }}>
      {att.filename || 'arquivo'}
    </button>
  )
}
```

- [ ] **Step 5: `FilaNoHub.jsx` (novo) e `AdminSuggestionsPage.jsx`**

`FilaNoHub.jsx`:

```jsx
import { HUB_PORTAL_URL } from './constants'

// O que a pessoa gestora vê aqui desde que a fila mora no hub. A Central de
// Comando (CentralDeComando.jsx) continua no código; só deixou de ser a tela.
export default function FilaNoHub() {
  return (
    <div className="sug-page sug-page--author">
      <section className="sug-hubnotice" role="status">
        <h1 className="sug-h1">A fila agora vive no hub</h1>
        <p className="sug-sub">
          As sugestões do E-monitor, da Plura e do hub caem numa fila só, na Central de Clientes. A triagem é feita lá.
        </p>
        <a className="btn btn-primary" href={`${HUB_PORTAL_URL}/admin/sugestoes`} target="_blank" rel="noopener noreferrer">
          Abrir a fila no hub
        </a>
      </section>
    </div>
  )
}
```

`AdminSuggestionsPage.jsx` inteiro:

```jsx
import { useAuth } from '../contexts/AuthContext'
import FilaNoHub from './suggestions/FilaNoHub'
import MinhasSugestoes from './suggestions/MinhasSugestoes'
import './AdminSuggestionsPage.css'

// Uma rota, duas faces. O e-mail do dev vem por env (espelha o
// SUGGESTIONS_DEV_EMAIL do backend). Desde 2026-10-26 a fila mora no hub: o dev
// tria lá, e aqui vê só o aviso com o link. A CentralDeComando continua no
// código, fora da tela.
const DEV_EMAIL = (import.meta.env.VITE_SUGGESTIONS_DEV_EMAIL || 'tatico3@hubradios.com').toLowerCase()

export default function AdminSuggestionsPage() {
  const { user } = useAuth()
  const isDev = (user?.email || '').toLowerCase() === DEV_EMAIL
  return isDev ? <FilaNoHub /> : <MinhasSugestoes />
}
```

- [ ] **Step 6: `SuggestionCreateModal.jsx`**

Trocar a primeira linha de import por `import { useState, useEffect, useRef } from 'react'`, e o import dos hooks por `import { useCreateSuggestion, useStageSuggestionAttachment } from '../../api/hooks'`.

Substituir as linhas de `const createM` até o fim de `handleSubmit` por:

```jsx
  const createM = useCreateSuggestion()
  const stageM = useStageSuggestionAttachment()
  const busy = createM.isPending || stageM.isPending
  // A chave do envio nasce com o formulário: tentar de novo depois de um erro é
  // o MESMO envio, e o hub não cria a sugestão duas vezes.
  const [clientKey] = useState(() => crypto.randomUUID())
  // Arquivo → id já subido. Reenviar não sobe a mesma imagem de novo.
  const staged = useRef(new Map())

  useEffect(() => {
    const onKey = (e) => { if (e.key === 'Escape' && !busy) onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [busy, onClose])

  async function handleSubmit(e) {
    e.preventDefault()
    if (busy) return
    setError('')
    if (!title.trim() || !description.trim()) {
      setError('Título e descrição são obrigatórios.')
      return
    }
    try {
      const ids = []
      for (const f of files) {
        if (!staged.current.has(f)) staged.current.set(f, (await stageM.mutateAsync(f)).id)
        ids.push(staged.current.get(f))
      }
      const created = await createM.mutateAsync({
        title: title.trim(),
        type,
        target_screen: targetScreen || null,
        description: description.trim(),
        requester_priority: priority,
        attachment_ids: ids,
        client_key: clientKey,
      })
      onCreated?.(created)
      onClose()
    } catch (err) {
      // O texto digitado fica no formulário: nada se perde quando o hub não responde.
      setError(err?.response?.data?.error || 'Não consegui salvar. Tenta de novo.')
    }
  }
```

- [ ] **Step 7: `SuggestionThread.jsx`**

Trocar os imports do topo por:

```jsx
import { useRef, useState } from 'react'
import { useAddSuggestionComment, useStageSuggestionAttachment } from '../../api/hooks'
import AttachmentItem from './AttachmentItem'
import ClipboardPasteZone from './ClipboardPasteZone'
import { timeAgo, initialOf } from './utils'
```

Substituir do `const [error, setError]` até o fim de `submit` por:

```jsx
  const [error, setError] = useState('')
  const addM = useAddSuggestionComment()
  const stageM = useStageSuggestionAttachment()
  const busy = addM.isPending || stageM.isPending
  const [clientKey, setClientKey] = useState(() => crypto.randomUUID())
  const staged = useRef(new Map())

  async function submit(e) {
    e.preventDefault()
    if (busy) return
    if (!body.trim() && !files.length) return
    setError('')
    try {
      const ids = []
      for (const f of files) {
        if (!staged.current.has(f)) staged.current.set(f, (await stageM.mutateAsync(f)).id)
        ids.push(staged.current.get(f))
      }
      await addM.mutateAsync({ id: suggestionId, body: body.trim(), attachmentIds: ids, clientKey })
      setBody(''); setFiles([])
      staged.current = new Map()
      setClientKey(crypto.randomUUID()) // a próxima fala é outro envio
    } catch (err) {
      setError(err?.response?.data?.error || 'Não consegui enviar. Tenta de novo.')
    }
  }
```

Dentro do `comments.map`, trocar a linha `{!fromRequester && <span className="sug-msg-tag">dev</span>}` por:

```jsx
                  {(c.from_dev ?? !fromRequester) && <span className="sug-msg-tag">dev</span>}
```

E trocar o bloco `{atts.map((a, i) => (` … `))}` por:

```jsx
                    {atts.map((a) => (
                      <AttachmentItem key={a.id} att={a} onOpenImage={onOpenImage} />
                    ))}
```

- [ ] **Step 8: `SuggestionDetail.jsx`**

Imports: trocar a linha das pills por `import { StatusPill, TypePill, ReqPriorityPill, DevPriorityPill, EffortPill, OriginPill } from './SuggestionPills'`, trocar `import AttachmentImage from './AttachmentImage'` por `import AttachmentItem, { isImage } from './AttachmentItem'`.

Em `eventPhrase`, antes do `default:`, acrescentar:

```jsx
    case 'retirada':         return 'retirou a sugestão'
    case 'esforco':          return 'definiu o esforço'
    case 'sua_vez':          return ev.to_value === 'true' ? 'passou a vez para quem abriu' : 'retomou a vez'
    case 'comentario':       return 'comentou'
    case 'editada':          return 'editou a sugestão'
    case 'anexo_removido':   return 'removeu um anexo'
    case 'apoio':            return 'apoiou'
```

Depois de `const rootAtts = …`, acrescentar:

```jsx
  const rootImages = useMemo(() => rootAtts.filter(isImage), [rootAtts])
```

No cabeçalho, depois de `<StatusPill value={s.status} size="sm" />`, acrescentar:

```jsx
                <OriginPill value={s.origin} />
                {s.numero_original && s.origin === 'e-monitor' && (
                  <span className="sug-ref" title="O número desta sugestão antes da fila ir para o hub">antes #{s.numero_original}</span>
                )}
```

Trocar o bloco da grade de anexos da descrição (`<div className="sug-att-grid">` … `</div>`) por:

```jsx
                <div className="sug-att-grid">
                  {rootAtts.map((a) => (
                    <AttachmentItem key={a.id} att={a} large onOpenImage={(x) => openLightbox(x, rootImages)} />
                  ))}
                </div>
```

E, no `<SuggestionThread …/>`, trocar `onOpenImage={(a) => openLightbox(a, attsByComment[a.comment_id] || [])}` por:

```jsx
                              onOpenImage={(a) => openLightbox(a, (attsByComment[a.comment_id] || []).filter(isImage))}
```

- [ ] **Step 9: `MinhasSugestoes.jsx`**

Trocar o import das pills por `import { StatusPill, TypePill, ReqPriorityPill, OriginPill } from './SuggestionPills'` e, depois de `<StatusPill value={s.status} size="sm" />` no card, acrescentar `<OriginPill value={s.origin} />`.

- [ ] **Step 10: CSS (`AdminSuggestionsPage.css`)**

Depois da linha `.sug-status--recusada …`:

```css
.sug-status--retirada     { background: #f1f5f9; color: #94a3b8; }
```

Depois da linha `.sug-pill--awaiting …`:

```css
.sug-pill--origin { background: #f0f9ff; color: #0369a1; border-color: #bae6fd; }
```

No fim do arquivo:

```css
/* anexos que não são imagem (vindos do hub) */
.sug-att-file, .sug-att-link { display: inline-flex; align-items: center; max-width: 220px; padding: 6px 10px;
  border: 1px solid var(--c-border); border-radius: var(--radius-md); background: var(--c-surface);
  color: var(--c-text-2); font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; cursor: pointer; }
.sug-att-link { text-decoration: underline; }

/* aviso da pessoa gestora: a fila mora no hub */
.sug-hubnotice { display: flex; flex-direction: column; align-items: flex-start; gap: 12px; max-width: 560px;
  margin: 48px auto; padding: 28px; border: 1px solid var(--c-border); border-radius: var(--radius-lg);
  background: var(--c-surface); }
```

> Só tokens que o arquivo já usa (`--c-border`, `--c-surface`, `--c-text-2`, `--radius-md`, `--radius-lg`). Token inventado compila para nada, calado (memória "Módulo CSS: o ponto cego"). Confira cada um com `grep -c -- '--radius-md' frontend/src/pages/AdminSuggestionsPage.css || true`: tem de dar maior que zero.

- [ ] **Step 11: Lint e build**

Run: `cd /c/tmp/emonitor-sugestoes-hub/frontend && npm run lint && npm run build 2>&1 | tail -3`
Expected: lint sem erro (nenhum import órfão: `CentralDeComando` saiu do import da página, mas o arquivo fica); build termina com `✓ built in`.

- [ ] **Step 12: Commit**

```bash
cd /c/tmp/emonitor-sugestoes-hub
F="frontend/src/api/hooks.js frontend/src/pages/suggestions/constants.js frontend/src/pages/suggestions/SuggestionPills.jsx frontend/src/pages/suggestions/AttachmentItem.jsx frontend/src/pages/suggestions/FilaNoHub.jsx frontend/src/pages/AdminSuggestionsPage.jsx frontend/src/pages/suggestions/SuggestionCreateModal.jsx frontend/src/pages/suggestions/SuggestionThread.jsx frontend/src/pages/suggestions/SuggestionDetail.jsx frontend/src/pages/suggestions/MinhasSugestoes.jsx frontend/src/pages/AdminSuggestionsPage.css"
printf '%s\n' "feat(sugestoes): tela le do hub — anexo antes, aviso da fila, retirada, selo de origem" "" "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" > "$TEMP/msg-t10.txt"
git add -- $F
git commit -F "$TEMP/msg-t10.txt" -- $F
```

---

### Task 11: A tela clicada (Playwright)

**Files:**
- Create: `frontend/e2e/sugestoes-hub.spec.js`

Escreva ANTES da Task 10 e veja falhar. Depois da Task 10, tem de passar.

- [ ] **Step 1: O spec**

```js
import { test, expect } from '@playwright/test'

/**
 * A tela de sugestões depois que a fila foi para o hub (spec do hub 2026-10-26
 * §4). A API daqui é falseada com `page.route` no JSON que o
 * SuggestionsHubHandler devolve; o que se prova é a TELA: a ordem
 * anexo → criação, a chave de envio, o aviso da pessoa gestora, `retirada`, o
 * selo de origem e o rascunho que sobrevive ao hub fora do ar.
 */

const ID = '6650aaaaaaaaaaaaaaaaaaaa'
const LISTA = /\/v1\/internal\/suggestions(\?.*)?$/
const UNREAD = /\/v1\/internal\/suggestions\/unread-count$/
const STAGE = /\/v1\/internal\/suggestions\/attachments$/
const DETALHE = new RegExp(`/v1/internal/suggestions/${ID}$`)
const LIDA = new RegExp(`/v1/internal/suggestions/${ID}/read$`)
const SUMMARY = /\/v1\/internal\/suggestions\/summary$/

const LINHA = {
  id: ID, ref_num: 42, origin: 'hub', type: 'feature', title: 'Filtro por cliente', description: 'prévia',
  status: 'retirada', requester_priority: 'media', awaiting_author: false, created_by: 'u1',
  created_by_name: 'QA', comment_count: 0, unread: false,
  created_at: '2026-10-26T12:00:00Z', updated_at: '2026-10-26T12:00:00Z',
}
const DETALHE_JSON = { ...LINHA, description: 'Quero filtrar.', comments: [], attachments: [], events: [] }

const PNG = Buffer.from('89504e470d0a1a0a0000000d4948445200000001000000010806000000' +
  '1f15c4890000000d49444154789c6360000002000154a24f5d0000000049454e44ae426082', 'hex')

function sessao(page, email = 'qa@teste.local') {
  return page.addInitScript((e) => {
    sessionStorage.setItem('rc_token', 'jwt.de.teste')
    sessionStorage.setItem('rc_user', JSON.stringify({
      id: '00000000-0000-0000-0000-000000000009', name: 'QA', email: e, role: 'admin', client_id: null,
    }))
  }, email)
}

const json = (r, status, body) => r.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })

async function rotasBase(page, pedidos) {
  await page.route(UNREAD, r => json(r, 200, { count: 0 }))
  await page.route(LIDA, r => r.fulfill({ status: 204 }))
  await page.route(DETALHE, r => json(r, 200, DETALHE_JSON))
  await page.route(SUMMARY, r => { pedidos.push('summary'); return json(r, 410, { error: 'x' }) })
}

test('autor vê retirada e o selo de origem', async ({ page }) => {
  const pedidos = []
  await sessao(page)
  await rotasBase(page, pedidos)
  await page.route(LISTA, r => json(r, 200, { data: [LINHA] }))
  await page.goto('/admin/suggestions')

  await expect(page.getByText('Filtro por cliente')).toBeVisible()
  await expect(page.locator('.sug-card').getByText('Retirada')).toBeVisible()
  await expect(page.locator('.sug-card').getByText('via Hub')).toBeVisible()
})

test('o anexo sobe ANTES e a criação leva o id e a chave', async ({ page }) => {
  const ordem = []
  let corpoCriacao = null
  await sessao(page)
  await rotasBase(page, [])
  await page.route(STAGE, r => { ordem.push('stage'); return json(r, 201, { id: 'an1' }) })
  await page.route(LISTA, r => {
    if (r.request().method() === 'POST') {
      ordem.push('create')
      corpoCriacao = r.request().postDataJSON()
      return json(r, 201, DETALHE_JSON)
    }
    return json(r, 200, { data: [] })
  })
  await page.goto('/admin/suggestions')

  await page.getByRole('button', { name: /nova sugestão/i }).first().click()
  await page.locator('#sug-title').fill('Filtro por cliente')
  await page.locator('#sug-desc').fill('Quero filtrar.')
  await page.locator('.sug-modal input[type=file]').setInputFiles({ name: 'print.png', mimeType: 'image/png', buffer: PNG })
  await page.getByRole('button', { name: 'Enviar sugestão' }).click()

  await expect.poll(() => ordem).toEqual(['stage', 'create'])
  expect(corpoCriacao.attachment_ids).toEqual(['an1'])
  expect(corpoCriacao.client_key).toMatch(/^[A-Za-z0-9_-]{8,64}$/)
  expect(corpoCriacao.type).toBe('melhoria')
})

test('hub fora do ar: a mensagem aparece e o texto fica', async ({ page }) => {
  const chaves = []
  await sessao(page)
  await rotasBase(page, [])
  await page.route(LISTA, r => {
    if (r.request().method() === 'POST') {
      chaves.push(r.request().postDataJSON().client_key)
      return json(r, 503, { error: 'não consegui falar com a Central de Clientes, tente de novo' })
    }
    return json(r, 200, { data: [] })
  })
  await page.goto('/admin/suggestions')

  await page.getByRole('button', { name: /nova sugestão/i }).first().click()
  await page.locator('#sug-title').fill('Filtro por cliente')
  await page.locator('#sug-desc').fill('Quero filtrar.')
  await page.getByRole('button', { name: 'Enviar sugestão' }).click()
  await expect(page.getByRole('alert')).toContainText('tente de novo')
  await expect(page.locator('#sug-title')).toHaveValue('Filtro por cliente')

  await page.getByRole('button', { name: 'Enviar sugestão' }).click()
  await expect.poll(() => chaves.length).toBe(2)
  expect(chaves[0]).toBe(chaves[1]) // tentar de novo é o MESMO envio
})

test('pessoa gestora vê que a fila vive no hub, sem chamar o summary', async ({ page }) => {
  const pedidos = []
  await sessao(page, 'tatico3@hubradios.com')
  await rotasBase(page, pedidos)
  await page.route(LISTA, r => json(r, 200, { data: [] }))
  await page.goto('/admin/suggestions')

  await expect(page.getByRole('heading', { name: 'A fila agora vive no hub' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Abrir a fila no hub' }))
    .toHaveAttribute('href', 'https://clientes.emidiastec.com.br/admin/sugestoes')
  expect(pedidos).toEqual([])
})
```

- [ ] **Step 2: Rodar ANTES da Task 10 e ver falhar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/frontend && npx playwright test e2e/sugestoes-hub.spec.js`
Expected: FAIL nos 4. Não há "Retirada" nem "via Hub"; a ordem vira `['create']`, sem stage; a pessoa gestora vê a Central de Comando.

- [ ] **Step 3: Depois da Task 10, rodar e ver passar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/frontend && npx playwright test e2e/sugestoes-hub.spec.js`
Expected: `4 passed`. Depois rode a pasta inteira (`npx playwright test`): os specs de antes (`hub-sso`, `campanha-*`, `clientes-cnpj`) continuam passando.

- [ ] **Step 4: Abrir e clicar de verdade** (memória "Clicar antes de dizer pronto")

Com o Vite no ar (`npm run dev -- --port 5174`), abra `/admin/suggestions` num Chrome com as mesmas rotas falseadas (`page.pause()` no segundo teste, ou um script `.mjs` próprio). Confira três coisas: o selo cabe no card a 390 px sem quebrar a linha das pills; a miniatura colada aparece no modal antes de enviar; o aviso da pessoa gestora não fica encostado no topo.

- [ ] **Step 5: Commit**

```bash
printf '%s\n' "test(sugestoes): a tela que le do hub, clicada com page.route" "" "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" > "$TEMP/msg-t11.txt"
git add -- frontend/e2e/sugestoes-hub.spec.js
git commit -F "$TEMP/msg-t11.txt" -- frontend/e2e/sugestoes-hub.spec.js
```

---

### Task 12: Importação — a leitura completa do Postgres

**Files:**
- Create: `workers/internal/catalog/suggestions_export.go`
- Test: `workers/internal/catalog/suggestions_export_test.go`

Este teste USA banco. Antes, monte-o com a receita da Task 13, Step 1.

- [ ] **Step 1: Teste que falha**

```go
package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSuggestions_ExportAll_TrazTudoQueAImportacaoPrecisa(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewSuggestions(pool)
	author := insertSuggestionTestUser(t, ctx, pool)
	sug := newTestSuggestion(t, ctx, repo, author)

	notas := "nota privada"
	_, err := repo.Update(ctx, sug.ID, UpdateSuggestionInput{ActorID: author, DevNotes: &notas})
	require.NoError(t, err)
	c, err := repo.AddComment(ctx, sug.ID, author, "primeira fala")
	require.NoError(t, err)
	_, err = repo.AddAttachment(ctx, AddAttachmentInput{SuggestionID: sug.ID, CommentID: &c.ID,
		StorageKey: "suggestions/x/a.png", ContentType: "image/png", SizeBytes: 10, UploadedBy: author})
	require.NoError(t, err)

	all, err := repo.ExportAll(ctx)
	require.NoError(t, err)
	var achada *SuggestionExport
	for i := range all {
		if all[i].ID == sug.ID {
			achada = &all[i]
		}
	}
	require.NotNil(t, achada)
	require.Equal(t, "nota privada", *achada.DevNotes)
	require.NotNil(t, achada.CreatedByEmail)
	require.Len(t, achada.Comments, 1)
	require.Equal(t, "primeira fala", achada.Comments[0].Body)
	require.Equal(t, *achada.CreatedByEmail, *achada.Comments[0].AuthorEmail)
	require.Len(t, achada.Attachments, 1)
	require.Equal(t, c.ID, *achada.Attachments[0].CommentID)
	require.Equal(t, "suggestions/x/a.png", achada.Attachments[0].StorageKey)
	// Quem ENVIOU o anexo: a importação não pode atribuí-lo a outra pessoa.
	require.Equal(t, *achada.CreatedByEmail, *achada.Attachments[0].UploaderEmail)
	require.False(t, achada.Attachments[0].CreatedAt.IsZero())
}
```

- [ ] **Step 2: Rodar e ver falhar (com banco)**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/catalog/ -run TestSuggestions_ExportAll -v`
Expected: FAIL — `repo.ExportAll undefined`. Se aparecer `SKIP: TEST_DATABASE_URL not set`, **pare**: o banco não está montado (Task 13, Step 1).

- [ ] **Step 3: Implementar**

```go
package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
)

/*
ExportAll lê o módulo de sugestões INTEIRO para o `import-suggestions-hub`
(spec do hub 2026-10-26 §5): toda sugestão, com a nota privada, o e-mail e o nome
de quem escreveu cada coisa, e onde cada anexo mora no bucket.

Só a importação chama isto. As telas leem do hub desde 2026-10-26.
*/

type SuggestionExport struct {
	Suggestion
	Comments    []SuggestionCommentExport
	Attachments []SuggestionAttachmentExport
}

type SuggestionCommentExport struct {
	ID          uuid.UUID
	Body        string
	CreatedAt   time.Time
	AuthorEmail *string
	AuthorName  *string
}

// UploaderEmail/UploaderName: quem ENVIOU (nil se a pessoa foi apagada). A
// importação usa isso para nunca atribuir o anexo a outra pessoa.
type SuggestionAttachmentExport struct {
	ID            uuid.UUID
	CommentID     *uuid.UUID
	StorageKey    string
	ContentType   string
	SizeBytes     int64
	CreatedAt     time.Time
	UploaderEmail *string
	UploaderName  *string
}

func (s *Suggestions) ExportAll(ctx context.Context) ([]SuggestionExport, error) {
	// u.name ANTES de u.email: é a ordem do scanSuggestionNamed.
	rows, err := s.pool.Query(ctx, `
		SELECT `+suggestionSelectCols+`, u.name, u.email
		FROM suggestions s
		LEFT JOIN users u ON u.id = s.created_by
		ORDER BY s.ref_num ASC`)
	if err != nil {
		return nil, err
	}
	var out []SuggestionExport
	pos := map[uuid.UUID]int{}
	for rows.Next() {
		var e SuggestionExport
		if err := scanSuggestionNamed(rows, &e.Suggestion); err != nil {
			rows.Close()
			return nil, err
		}
		pos[e.ID] = len(out)
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	crows, err := s.pool.Query(ctx, `
		SELECT c.id, c.suggestion_id, c.body, c.created_at, u.email, u.name
		FROM suggestion_comments c
		LEFT JOIN users u ON u.id = c.author_id
		ORDER BY c.created_at ASC, c.id ASC`)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var c SuggestionCommentExport
		var sid uuid.UUID
		if err := crows.Scan(&c.ID, &sid, &c.Body, &c.CreatedAt, &c.AuthorEmail, &c.AuthorName); err != nil {
			crows.Close()
			return nil, err
		}
		if i, ok := pos[sid]; ok {
			out[i].Comments = append(out[i].Comments, c)
		}
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return nil, err
	}

	arows, err := s.pool.Query(ctx, `
		SELECT a.id, a.suggestion_id, a.comment_id, a.storage_key, a.content_type, a.size_bytes,
		       a.created_at, u.email, u.name
		FROM suggestion_attachments a
		LEFT JOIN users u ON u.id = a.uploaded_by
		ORDER BY a.created_at ASC, a.id ASC`)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var a SuggestionAttachmentExport
		var sid uuid.UUID
		if err := arows.Scan(&a.ID, &sid, &a.CommentID, &a.StorageKey, &a.ContentType, &a.SizeBytes,
			&a.CreatedAt, &a.UploaderEmail, &a.UploaderName); err != nil {
			return nil, err
		}
		if i, ok := pos[sid]; ok {
			out[i].Attachments = append(out[i].Attachments, a)
		}
	}
	return out, arows.Err()
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./internal/catalog/ -run 'TestSuggestions_' -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: `--- PASS: TestSuggestions_ExportAll_TrazTudoQueAImportacaoPrecisa` e os `TestSuggestions_*` de antes, todos PASS, nenhum SKIP.

- [ ] **Step 5: Commit**

```bash
printf '%s\n' "feat(catalog): leitura completa das sugestoes para a importacao no hub" "" "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" > "$TEMP/msg-t12.txt"
git add -- workers/internal/catalog/suggestions_export.go workers/internal/catalog/suggestions_export_test.go
git commit -F "$TEMP/msg-t12.txt" -- workers/internal/catalog/suggestions_export.go workers/internal/catalog/suggestions_export_test.go
```

---

### Task 13: Importação — o CLI `import-suggestions-hub`

**Files:**
- Create: `workers/cmd/import-suggestions-hub/importa.go`
- Create: `workers/cmd/import-suggestions-hub/importa_test.go`
- Create: `workers/cmd/import-suggestions-hub/main.go`
- Create: `workers/cmd/import-suggestions-hub/estado.go`
- Create: `workers/cmd/import-suggestions-hub/estado_test.go`
- Modify: `infra/docker/Dockerfiles/workers.Dockerfile`

- [ ] **Step 1: Teste que falha (sem banco, sem rede)**

`importa_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

type objetosFalsos map[string][]byte

func (o objetosFalsos) Get(_ context.Context, key string) (io.ReadCloser, string, int64, error) {
	b := o[key]
	return io.NopCloser(bytes.NewReader(b)), "image/png", int64(len(b)), nil
}

type subida struct {
	autor hub.Autor
	nome  string
}

type portaFalsa struct {
	subidas   []subida
	importada *hub.SugestaoImportada
	autor     hub.Autor
}

func (p *portaFalsa) SubirAnexoSugestao(_ context.Context, a hub.Autor, nome, _ string, _ []byte) (string, error) {
	p.subidas = append(p.subidas, subida{a, nome})
	return "an" + string(rune('0'+len(p.subidas))), nil
}

func (p *portaFalsa) ImportarSugestao(_ context.Context, a hub.Autor, s hub.SugestaoImportada) (*hub.RespostaImportar, error) {
	p.autor, p.importada = a, &s
	return &hub.RespostaImportar{Acao: "importada", Numero: 99}, nil
}

func s(v string) *string { return &v }

func TestImportaUma(t *testing.T) {
	cID := uuid.New()
	quando := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	sug := catalog.SuggestionExport{
		Suggestion: catalog.Suggestion{
			ID: uuid.New(), RefNum: 17, Title: strings.Repeat("t", 200), Description: "D", Type: "feature",
			RequesterPriority: "alta", Status: "concluida", DevNotes: s("privado"), DevFeedback: s("feito"),
			CreatedAt: quando, CreatedByEmail: s("ana@t.local"), CreatedByName: s("Ana"),
		},
		Comments: []catalog.SuggestionCommentExport{{ID: cID, Body: "fala do dev", CreatedAt: quando,
			AuthorEmail: s("tatico3@hubradios.com"), AuthorName: s("Dereck")}},
		Attachments: []catalog.SuggestionAttachmentExport{
			{ID: uuid.New(), StorageKey: "suggestions/a/raiz.png", ContentType: "image/png", UploaderEmail: s("ANA@t.local")},
			{ID: uuid.New(), CommentID: &cID, StorageKey: "suggestions/a/fala.png", ContentType: "image/png", UploaderEmail: s("tatico3@hubradios.com")},
		},
	}
	p := &portaFalsa{}
	r, err := importaUma(context.Background(), p, objetosFalsos{"suggestions/a/raiz.png": []byte("1"), "suggestions/a/fala.png": []byte("2")}, sug)

	require.NoError(t, err)
	require.Equal(t, 99, r.Numero)
	require.Equal(t, "ana@t.local", p.autor.Email)
	// Cada anexo sobe com os headers de QUEM O ENVIOU (o hub só amarra anexo de
	// quem o enviou, e atribuir a outra pessoa falsificaria o registro).
	require.Equal(t, "ANA@t.local", p.subidas[0].autor.Email)
	require.Equal(t, "raiz.png", p.subidas[0].nome)
	require.Equal(t, "tatico3@hubradios.com", p.subidas[1].autor.Email)
	imp := p.importada
	require.Equal(t, "ideia", imp.Tipo)
	require.Equal(t, 17, imp.NumeroOriginal)
	require.Equal(t, sug.ID.String(), imp.IDOriginal)
	require.Equal(t, "privado", *imp.Notas)
	require.Equal(t, "feito", *imp.Resposta)
	require.Equal(t, []string{"an1"}, imp.AnexoIDs)
	require.Len(t, imp.Comentarios, 1)
	require.Equal(t, []string{"an2"}, imp.Comentarios[0].AnexoIDs)
	require.Equal(t, cID.String(), imp.Comentarios[0].IDOriginal)
	require.Equal(t, "Dereck", imp.Comentarios[0].Autor.Nome)
	require.Len(t, []rune(imp.Titulo), 180)
	require.True(t, strings.HasSuffix(imp.Titulo, "…"))
}

// O dev anexou um print direto na sugestão de outra pessoa: NÃO vai para o topo
// em nome do autor. Vira uma fala sintética, sem texto, de quem enviou.
func TestImportaUma_AnexoDeOutraPessoaNaRaizViraFalaSintetica(t *testing.T) {
	aID := uuid.New()
	quando := time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)
	p := &portaFalsa{}
	_, err := importaUma(context.Background(), p, objetosFalsos{"k": []byte("1")}, catalog.SuggestionExport{
		Suggestion: catalog.Suggestion{ID: uuid.New(), Type: "bug", Status: "nova", CreatedAt: quando.Add(-time.Hour),
			CreatedByEmail: s("ana@t.local"), CreatedByName: s("Ana")},
		Attachments: []catalog.SuggestionAttachmentExport{{ID: aID, StorageKey: "k", ContentType: "image/png",
			CreatedAt: quando, UploaderEmail: s("tatico3@hubradios.com"), UploaderName: s("Dereck")}},
	})
	require.NoError(t, err)
	require.Equal(t, "tatico3@hubradios.com", p.subidas[0].autor.Email)
	require.Empty(t, p.importada.AnexoIDs)
	require.Len(t, p.importada.Comentarios, 1)
	f := p.importada.Comentarios[0]
	require.Equal(t, "anexo-"+aID.String(), f.IDOriginal)
	require.Equal(t, "tatico3@hubradios.com", f.Autor.Email)
	require.Equal(t, "", f.Corpo)
	require.Equal(t, quando, f.CreatedAt)
	require.Equal(t, []string{"an1"}, f.AnexoIDs)
}

func TestImportaUma_FalasSaemEmOrdemDeData(t *testing.T) {
	cID := uuid.New()
	t0 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	p := &portaFalsa{}
	_, err := importaUma(context.Background(), p, objetosFalsos{"k": []byte("1")}, catalog.SuggestionExport{
		Suggestion: catalog.Suggestion{ID: uuid.New(), Type: "bug", Status: "nova", CreatedByEmail: s("ana@t.local")},
		Comments:   []catalog.SuggestionCommentExport{{ID: cID, Body: "depois", CreatedAt: t0.Add(2 * time.Hour), AuthorEmail: s("ana@t.local")}},
		Attachments: []catalog.SuggestionAttachmentExport{{ID: uuid.New(), StorageKey: "k", ContentType: "image/png",
			CreatedAt: t0.Add(time.Hour), UploaderEmail: s("tatico3@hubradios.com")}},
	})
	require.NoError(t, err)
	require.Len(t, p.importada.Comentarios, 2)
	require.Equal(t, "", p.importada.Comentarios[0].Corpo) // a sintética, 1h
	require.Equal(t, "depois", p.importada.Comentarios[1].Corpo)
}

func TestImportaUma_AutorApagadoViraMarcador(t *testing.T) {
	p := &portaFalsa{}
	_, err := importaUma(context.Background(), p, objetosFalsos{}, catalog.SuggestionExport{
		Suggestion: catalog.Suggestion{ID: uuid.New(), Type: "bug", Status: "nova"},
	})
	require.NoError(t, err)
	require.Equal(t, "autor-removido@e-monitor.invalid", p.autor.Email)
	require.Equal(t, "Pessoa removida", p.autor.Nome)
}

func TestImportaUma_AnexoDeFalaInexistenteVaiParaARaiz(t *testing.T) {
	fantasma := uuid.New()
	p := &portaFalsa{}
	_, err := importaUma(context.Background(), p, objetosFalsos{"k": []byte("1")}, catalog.SuggestionExport{
		Suggestion: catalog.Suggestion{ID: uuid.New(), Type: "bug", Status: "nova", CreatedByEmail: s("ana@t.local")},
		Attachments: []catalog.SuggestionAttachmentExport{{ID: uuid.New(), CommentID: &fantasma, StorageKey: "k",
			ContentType: "image/png", UploaderEmail: s("ana@t.local")}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"an1"}, p.importada.AnexoIDs)
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./cmd/import-suggestions-hub/ -v`
Expected: FAIL — `undefined: importaUma`.

- [ ] **Step 3: `importa.go`**

```go
package main

import (
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/google/uuid"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

// Quem escreveu e foi apagado daqui (`created_by` NULL) entra no hub com um
// autor marcador: a porta exige um e-mail, e "ninguém" não é e-mail.
//
// ⚠️ `.invalid`, nunca um domínio real: o hub suprime todo aviso para e-mail
// `.invalid` (hub 21713a74). Com `@e-monitor.online`, a resposta do gestor
// sairia por e-mail para uma caixa que alguém pode ter.
const (
	autorRemovidoEmail = "autor-removido@e-monitor.invalid"
	autorRemovidoNome  = "Pessoa removida"
)

// Os tetos do hub (`LIMITES` do vocabulário de lá). Mais longo que isso o hub
// recusaria a sugestão inteira; cortar e marcar com "…" perde menos.
const (
	limiteTitulo = 180
	limiteTexto  = 8000
	// O maior anexo que o hub aceita (arquivo, 25 MB). As imagens daqui têm 10.
	limiteAnexo = 25 << 20
)

type leitorDeObjetos interface {
	Get(ctx context.Context, key string) (io.ReadCloser, string, int64, error)
}

type portaDoHub interface {
	SubirAnexoSugestao(ctx context.Context, a hub.Autor, nome, mime string, dados []byte) (string, error)
	ImportarSugestao(ctx context.Context, a hub.Autor, s hub.SugestaoImportada) (*hub.RespostaImportar, error)
}

func autorDe(email, nome *string) hub.Autor {
	if email == nil || strings.TrimSpace(*email) == "" {
		return hub.Autor{Email: autorRemovidoEmail, Nome: autorRemovidoNome}
	}
	n := ""
	if nome != nil {
		n = strings.TrimSpace(*nome)
	}
	if n == "" {
		n = *email
	}
	return hub.Autor{Email: *email, Nome: n}
}

func corta(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func lerObjeto(ctx context.Context, o leitorDeObjetos, key string) ([]byte, error) {
	corpo, _, _, err := o.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer corpo.Close()
	dados, err := io.ReadAll(io.LimitReader(corpo, limiteAnexo+1))
	if err != nil {
		return nil, err
	}
	if len(dados) > limiteAnexo {
		return nil, fmt.Errorf("maior que %d MB", limiteAnexo>>20)
	}
	return dados, nil
}

func mesmaPessoa(a, b hub.Autor) bool {
	return strings.EqualFold(strings.TrimSpace(a.Email), strings.TrimSpace(b.Email))
}

/*
importaUma sobe os anexos e manda a sugestão.

⚠️ Todo anexo sobe com os headers de QUEM O ENVIOU (`uploaded_by`), nunca de
outra pessoa: atribuir o print do dev ao autor falsificaria o registro. O
anexo vai para onde aquela pessoa é dona — o topo, se foi o autor da sugestão;
a fala, se foi quem falou. Quando não é dono de nada ali (o dev anexou direto na
sugestão de alguém), o anexo vira uma FALA SINTÉTICA sem texto, daquela pessoa,
na data do anexo, com `idOriginal` estável `anexo-<id>` (o hub aceita fala vazia
com anexo, e a reimportação não a duplica).

Idempotente do lado do hub. Os anexos de uma reimportação que dá `ja-importada`
ficam "sem dono" lá até o `limpaUploadsOrfaos` (24 h); o arquivo de estado do
`main.go` evita subi-los à toa.
*/
func importaUma(ctx context.Context, porta portaDoHub, objetos leitorDeObjetos, s catalog.SuggestionExport) (*hub.RespostaImportar, error) {
	autor := autorDe(s.CreatedByEmail, s.CreatedByName)
	autorDaFala := map[uuid.UUID]hub.Autor{}
	for _, c := range s.Comments {
		autorDaFala[c.ID] = autorDe(c.AuthorEmail, c.AuthorName)
	}

	raiz := []string{}
	porFala := map[uuid.UUID][]string{}
	var sinteticas []hub.ComentarioImportado
	for _, a := range s.Attachments {
		quemEnviou := autorDe(a.UploaderEmail, a.UploaderName)
		dados, err := lerObjeto(ctx, objetos, a.StorageKey)
		if err != nil {
			return nil, fmt.Errorf("anexo %s (%s): %w", a.ID, a.StorageKey, err)
		}
		id, err := porta.SubirAnexoSugestao(ctx, quemEnviou, path.Base(a.StorageKey), a.ContentType, dados)
		if err != nil {
			return nil, fmt.Errorf("anexo %s: %w", a.ID, err)
		}
		if a.CommentID != nil {
			if dono, ok := autorDaFala[*a.CommentID]; ok && mesmaPessoa(dono, quemEnviou) {
				porFala[*a.CommentID] = append(porFala[*a.CommentID], id)
				continue
			}
		} else if mesmaPessoa(autor, quemEnviou) {
			raiz = append(raiz, id)
			continue
		}
		// Fala inexistente, ou anexo de quem não é dono do lugar: fala sintética.
		// Um anexo cuja fala sumiu mas que é do autor vai para o topo.
		if a.CommentID != nil && mesmaPessoa(autor, quemEnviou) {
			if _, existe := autorDaFala[*a.CommentID]; !existe {
				raiz = append(raiz, id)
				continue
			}
		}
		sinteticas = append(sinteticas, hub.ComentarioImportado{
			IDOriginal: "anexo-" + a.ID.String(),
			Autor:      hub.AutorJSON{Email: quemEnviou.Email, Nome: quemEnviou.Nome},
			Corpo:      "", CreatedAt: a.CreatedAt, AnexoIDs: []string{id},
		})
	}

	falas := make([]hub.ComentarioImportado, 0, len(s.Comments)+len(sinteticas))
	for _, c := range s.Comments {
		q := autorDaFala[c.ID]
		ids := porFala[c.ID]
		if ids == nil {
			ids = []string{}
		}
		falas = append(falas, hub.ComentarioImportado{
			IDOriginal: c.ID.String(), Autor: hub.AutorJSON{Email: q.Email, Nome: q.Nome},
			Corpo: corta(c.Body, limiteTexto), CreatedAt: c.CreatedAt, AnexoIDs: ids,
		})
	}
	falas = append(falas, sinteticas...)
	// A conversa do hub é lida em ordem de data: a sintética entra onde o anexo
	// foi feito, não no fim.
	sort.SliceStable(falas, func(i, j int) bool { return falas[i].CreatedAt.Before(falas[j].CreatedAt) })

	return porta.ImportarSugestao(ctx, autor, hub.SugestaoImportada{
		IDOriginal: s.ID.String(), NumeroOriginal: int(s.RefNum),
		Tipo: hub.TipoParaHub(s.Type), Titulo: corta(s.Title, limiteTitulo), Descricao: corta(s.Description, limiteTexto),
		TelaAlvo: s.TargetScreen, UrgenciaAutor: s.RequesterPriority, Status: s.Status,
		Prioridade: s.DevPriority, Esforco: s.Effort, Resposta: s.DevFeedback, Notas: s.DevNotes,
		SuaVez: s.AwaitingAuthor, ResolvidaEm: s.ResolvedAt, CreatedAt: s.CreatedAt,
		AnexoIDs: raiz, Comentarios: falas,
	})
}
```

- [ ] **Step 4: `main.go`**

```go
// import-suggestions-hub leva as sugestões do Postgres daqui para a fila do hub,
// uma vez (spec do hub 2026-10-26 §5). Lê suggestions + comentários + anexos
// (bytes do MinIO) e manda cada uma para POST /api/platform/sugestoes/importar.
//
// Idempotente: o hub reconhece a sugestão pelo id daqui e responde `ja-importada`.
// Para não subir bytes à toa numa segunda rodada, o CLI guarda um arquivo de
// estado local (id daqui → id do hub, fora do git) e PULA o que já foi. `--only`
// força uma sugestão mesmo marcada. Sem o arquivo (container recriado), a
// reimportação continua segura: o hub dá `ja-importada`, e os anexos subidos à
// toa somem em 24 h pelo `limpaUploadsOrfaos` de lá.
//
// Uma sugestão cujo corpo passe de 2 MB (só texto: os bytes vão pelo GCS) leva
// 413 `payload_too_large` do hub: entra como falha no relatório e o CLI segue.
//
// Roda DEPOIS de a tela já ler do hub (o deploy deste branch): antes disso, o
// que alguém criasse no meio ficaria órfão no Postgres.
//
// Usage (na VM, dentro do container da API, que já tem as envs):
//
//	docker compose exec api import-suggestions-hub --dry-run
//	docker compose exec api import-suggestions-hub
//	docker compose exec api import-suggestions-hub --only <uuid>
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/catalog"
	"radiocheck/internal/config"
	"radiocheck/internal/hub"
	"radiocheck/internal/storage"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "só lista o que seria importado; não sobe nada")
	only := flag.String("only", "", "importa só esta sugestão (uuid), mesmo já marcada no estado")
	caminhoEstado := flag.String("estado", "/tmp/import-suggestions-hub.estado.json",
		"arquivo local id-daqui → id-do-hub; o que está nele é pulado")
	flag.Parse()

	est, err := carregaEstado(*caminhoEstado)
	if err != nil {
		log.Fatalf("estado: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	hc := hub.NewComTimeout(cfg.HubURL, cfg.HubPlatformKey, 60*time.Second)
	if !*dryRun && !hc.Configured() {
		log.Fatal("HUB_URL/HUB_PLATFORM_KEY não configurados")
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer pool.Close()
	store, err := storage.New(ctx, cfg.S3Endpoint, cfg.S3PublicEndpoint, cfg.S3Bucket,
		cfg.S3Region, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	lista, err := catalog.NewSuggestions(pool).ExportAll(ctx)
	if err != nil {
		log.Fatalf("leitura: %v", err)
	}

	var importadas, jaExistiam, seriam, puladas int
	var falhas []string
	for _, s := range lista {
		if *only != "" && s.ID.String() != *only {
			continue
		}
		if *only == "" && est.ja(s.ID.String()) {
			puladas++
			continue
		}
		if *dryRun {
			seriam++
			fmt.Printf("seria importada #%d %s (%d falas, %d anexos, status %s)\n",
				s.RefNum, s.ID, len(s.Comments), len(s.Attachments), s.Status)
			continue
		}
		r, err := importaUma(ctx, hc, store, s)
		if err != nil {
			// Inclui o 413 de corpo acima de 2 MB: registra e segue.
			falhas = append(falhas, fmt.Sprintf("#%d %s: %v", s.RefNum, s.ID, err))
			continue
		}
		if r.Acao == "importada" {
			importadas++
			fmt.Printf("importada #%d → #%d do hub\n", s.RefNum, r.Numero)
		} else {
			jaExistiam++
		}
		if err := est.marca(s.ID.String(), r.SugestaoID); err != nil {
			// A importação valeu; só a próxima rodada não vai saber pular esta.
			log.Printf("estado: não gravei %s: %v", s.ID, err)
		}
	}

	fmt.Printf("\nlidas: %d · importadas: %d · já existiam: %d · puladas (estado local): %d · falharam: %d",
		len(lista), importadas, jaExistiam, puladas, len(falhas))
	if *dryRun {
		fmt.Printf(" · seriam importadas: %d (dry-run)", seriam)
	}
	fmt.Println()
	for _, f := range falhas {
		fmt.Println("  FALHOU", f)
	}
	if len(falhas) > 0 {
		os.Exit(1)
	}
}
```

> Confira a ordem dos argumentos de `storage.New` contra a assinatura real em `workers/internal/storage/` (`New(ctx, endpoint, publicEndpoint, bucket, region, accessKey, secretKey)`) e contra a chamada em `cmd/api/main.go:114`. Se as duas divergirem, vale a de `main.go`, que está em produção.

- [ ] **Step 4b: O arquivo de estado (`estado.go` + `estado_test.go`)**

`workers/cmd/import-suggestions-hub/estado_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEstado_SobreviveEntreRodadas(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "estado.json")

	e, err := carregaEstado(caminho)
	require.NoError(t, err, "arquivo inexistente é estado vazio, não erro")
	require.False(t, e.ja("u1"))

	require.NoError(t, e.marca("u1", "6650aaaaaaaaaaaaaaaaaaaa"))

	deNovo, err := carregaEstado(caminho)
	require.NoError(t, err)
	require.True(t, deNovo.ja("u1"))
	require.False(t, deNovo.ja("u2"))
}

func TestEstado_ArquivoCorrompidoEErro(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "estado.json")
	require.NoError(t, os.WriteFile(caminho, []byte("{nao é json"), 0o600))
	_, err := carregaEstado(caminho)
	require.Error(t, err, "estado ilegível não pode virar 'nada importado' e reimportar tudo")
}
```

`workers/cmd/import-suggestions-hub/estado.go`:

```go
package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
)

// estado é o registro local do que já foi importado: id daqui → id do hub. Fica
// FORA do git (padrão: /tmp do container). Serve para não subir bytes de novo;
// a garantia de não duplicar é do hub (`ja-importada`), não deste arquivo.
type estado struct {
	caminho    string
	Importadas map[string]string `json:"importadas"`
}

func carregaEstado(caminho string) (*estado, error) {
	e := &estado{caminho: caminho, Importadas: map[string]string{}}
	b, err := os.ReadFile(caminho)
	if errors.Is(err, fs.ErrNotExist) {
		return e, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, e); err != nil {
		return nil, err
	}
	if e.Importadas == nil {
		e.Importadas = map[string]string{}
	}
	return e, nil
}

func (e *estado) ja(id string) bool { _, ok := e.Importadas[id]; return ok }

// marca grava a cada sugestão, por arquivo temporário + rename: um Ctrl+C no
// meio não deixa um JSON pela metade (que o próximo carregaEstado recusaria).
func (e *estado) marca(id, sugestaoID string) error {
	e.Importadas[id] = sugestaoID
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	tmp := e.caminho + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, e.caminho)
}
```

- [ ] **Step 5: Dockerfile**

Em `infra/docker/Dockerfiles/workers.Dockerfile`, depois da linha `RUN … go build -o /out/backfill-anatel ./cmd/backfill-anatel`:

```dockerfile
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/import-suggestions-hub ./cmd/import-suggestions-hub
```

E, depois da linha `COPY --from=builder /out/backfill-anatel  /usr/local/bin/backfill-anatel`:

```dockerfile
COPY --from=builder /out/import-suggestions-hub  /usr/local/bin/import-suggestions-hub
```

- [ ] **Step 6: Rodar e ver passar**

Run: `cd /c/tmp/emonitor-sugestoes-hub/workers && go test ./cmd/import-suggestions-hub/ -v && go build ./cmd/import-suggestions-hub/ && go vet ./cmd/import-suggestions-hub/`
Expected: `--- PASS` nos 7 testes (5 de `importa`, 2 de `estado`); build e vet sem saída. Apague o binário que o `go build` deixou: `rm -f import-suggestions-hub import-suggestions-hub.exe`.

- [ ] **Step 6b: Mutação da atribuição**

Com cópia em `$TEMP`, troque `porta.SubirAnexoSugestao(ctx, quemEnviou,` por `porta.SubirAnexoSugestao(ctx, autor,`. Esperado: `TestImportaUma_AnexoDeOutraPessoaNaRaizViraFalaSintetica` FALHA, porque o print do dev subiria em nome da Ana. Restaure da cópia e confira o `git diff --stat`.

- [ ] **Step 7: Commit**

```bash
cd /c/tmp/emonitor-sugestoes-hub
F="workers/cmd/import-suggestions-hub/importa.go workers/cmd/import-suggestions-hub/importa_test.go workers/cmd/import-suggestions-hub/main.go workers/cmd/import-suggestions-hub/estado.go workers/cmd/import-suggestions-hub/estado_test.go infra/docker/Dockerfiles/workers.Dockerfile"
printf '%s\n' "feat(sugestoes): CLI que importa o historico do Postgres para o hub" "" "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" > "$TEMP/msg-t13.txt"
git add -- $F
git commit -F "$TEMP/msg-t13.txt" -- $F
```

---

### Task 14: Documento, suíte inteira com banco, e a ordem de produção

**Files:**
- Modify: `docs/features/suggestions-board.md`

- [ ] **Step 1: Montar o banco de teste** (memórias "E-monitor: banco de teste sem Docker" e "partições do banco de teste")

```bash
cd /c/tmp/emonitor-sugestoes-hub
export PATH="/c/Program Files/PostgreSQL/16/bin:$PATH"
export PGPASSWORD=postgres
export PGCLIENTENCODING=UTF8
PSQL="psql -h 127.0.0.1 -p 5433 -U postgres -v ON_ERROR_STOP=1 -q"
$PSQL -d postgres -c "DROP DATABASE IF EXISTS radiocheck_test WITH (FORCE)"
$PSQL -d postgres -c "CREATE DATABASE radiocheck_test"
for f in $(ls migrations/*.up.sql | sort); do $PSQL -d radiocheck_test -f "$f" || { echo "MORREU em $f"; break; }; done
$PSQL -d radiocheck_test -c "SELECT ensure_month_partitions(6)"
$PSQL -d radiocheck_test <<'SQL'
DO $$
DECLARE parent text; prefix text; m date; partname text; i int;
BEGIN
  FOREACH parent IN ARRAY ARRAY['detections','stream_health_events','detection_campaigns'] LOOP
    prefix := CASE parent WHEN 'stream_health_events' THEN 'stream_health' ELSE parent END;
    FOR i IN 1..24 LOOP
      m := (date_trunc('month', CURRENT_DATE) - (i || ' months')::interval)::date;
      partname := prefix || '_' || to_char(m, 'YYYY_MM');
      IF to_regclass(partname) IS NULL THEN
        EXECUTE format('CREATE TABLE %I PARTITION OF %I FOR VALUES FROM (%L) TO (%L)',
          partname, parent, m, (m + interval '1 month')::date);
      END IF;
    END LOOP;
  END LOOP;
END $$;
SQL
export TEST_DATABASE_URL="postgres://postgres:postgres@127.0.0.1:5433/radiocheck_test?sslmode=disable"
```

Expected: nenhum "MORREU em". Se o `DO $$` acusar nome de partição diferente, confira o padrão real com `$PSQL -d radiocheck_test -c "\dt detections_*"` e ajuste o `prefix`.

- [ ] **Step 2: Suíte inteira dos workers, contando o SKIP**

```bash
cd /c/tmp/emonitor-sugestoes-hub/workers
go test -v ./... > "$TEMP/emonitor-suite.txt" 2>&1; echo "exit=$?"
grep -cE -- '^--- FAIL' "$TEMP/emonitor-suite.txt" || true
grep -cE -- '^\s*--- SKIP' "$TEMP/emonitor-suite.txt" || true
grep -cE -- '^\s*--- PASS' "$TEMP/emonitor-suite.txt" || true
```

Expected: `exit=0`, FAIL = 0. O SKIP fica no piso da master: os testes que pulam por outro motivo que não o banco. Para saber o piso, rode a mesma contagem na master num worktree limpo; o desta branch não pode passar dele. Se o SKIP passar de ~10, o `TEST_DATABASE_URL` não pegou. **Não reporte verde sem os três números.**

- [ ] **Step 3: Front inteiro**

Run: `cd /c/tmp/emonitor-sugestoes-hub/frontend && npm run lint && npm run build 2>&1 | tail -2 && npx playwright test`
Expected: lint limpo, `✓ built in`, Playwright com todos passando (os 4 novos + os de antes).

- [ ] **Step 4: Atualizar `docs/features/suggestions-board.md`**

No frontmatter, `ultima-verificacao: 2026-10-26`. Somar a `codigo-relacionado:` as linhas `workers/internal/hub/sugestoes.go`, `workers/internal/hub/sugestoes_anexos.go`, `workers/internal/api/handlers/suggestions_hub.go`, `workers/cmd/import-suggestions-hub/` e `frontend/src/pages/suggestions/FilaNoHub.jsx`. Logo abaixo do título, inserir:

```markdown
> **Desde 2026-10-26 a fila MORA NO HUB.** Esta tela grava e lê pela porta
> `/api/platform/sugestoes` da Central de Clientes (spec do hub
> `docs/superpowers/specs/2026-10-26-sugestoes-de-todas-as-plataformas-design.md`).
> O que está abaixo desta caixa descreve o módulo ANTIGO (Postgres), que ficou no
> código e nas tabelas como arquivo; o que mudou:
>
> - **Handler:** `SuggestionsHubHandler` (`handlers/suggestions_hub.go`) atende as
>   mesmas rotas e o mesmo JSON. Traduz `feature↔ideia`; `dev_feedback` vem da
>   `resposta` do hub. `SuggestionsHandler` (Postgres) saiu do router.
> - **Quem é o autor:** e-mail e nome lidos do banco (o JWT não tem e-mail) vão nos
>   headers `X-Hub-Autor-Email` / `X-Hub-Autor-Nome`, com a `HUB_PLATFORM_KEY`.
> - **Persona dev:** não há mais Central de Comando aqui — `FilaNoHub.jsx` mostra
>   "A fila agora vive no hub" com o link. PATCH, `/summary` e
>   `/attachments/{aid}/url` respondem **410**.
> - **Anexo:** a tela sobe a imagem ANTES (`POST /suggestions/attachments` →
>   upload "sem dono" no hub: upload-url → PUT no GCS → confirm) e a criação ou o
>   comentário a amarram por `attachment_ids`. A exibição continua pelo proxy de
>   bytes; o 302 do hub é seguido no servidor, SEM headers (o `http.Client`
>   padrão copiaria a chave para o GCS).
> - **Idempotência:** a tela manda `client_key` (gerada ao abrir o formulário);
>   tentar de novo é o mesmo envio.
> - **Badge:** `unread-count` com cache de 30 s por autor, em memória.
> - **Sem HUB_URL/HUB_PLATFORM_KEY** toda rota responde 503 — de propósito.
> - **Status novo:** `retirada`. **Selo de origem:** "via Hub" / "via Plura".
> - **Histórico:** `import-suggestions-hub` (CLI no container da API) leva tudo
>   do Postgres + MinIO para o hub, uma vez. Todo anexo sobe em nome de quem o
>   ENVIOU; o print que o dev pôs na sugestão de alguém vira uma fala sem texto
>   dele (`anexo-<id>`). Autor apagado vira `autor-removido@e-monitor.invalid`
>   (o hub não manda aviso para `.invalid`). Um arquivo de estado local faz a
>   2ª rodada pular o que já foi. Ver o cabeçalho de
>   `workers/cmd/import-suggestions-hub/main.go`.
```

- [ ] **Step 5: Commit do doc**

```bash
cd /c/tmp/emonitor-sugestoes-hub
printf '%s\n' "docs(sugestoes): a fila mora no hub" "" "Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" > "$TEMP/msg-t14.txt"
git commit -F "$TEMP/msg-t14.txt" -- docs/features/suggestions-board.md
git log --oneline origin/master..HEAD
```

Expected: os commits desta branch, um por task.

- [ ] **Step 6: A ordem de produção (é o Dereck quem faz; anote, não execute)**

1. **O hub primeiro.** A porta tem que existir. Sonda sem segredo, de qualquer máquina:
   `curl -s -o /dev/null -w '%{http_code}\n' -H 'X-Hub-Platform-Key: x' -H 'X-Hub-Autor-Email: a@b.c' https://api-clientes.emidiastec.com.br/api/platform/sugestoes/nao-lidas`
   `401` = a porta existe (a chave falsa foi recusada). `404` = o hub ainda não tem o deploy: **pare aqui**.
2. **Deploy do E-monitor** (manual, `docs/operations/deploy.md`): backend (`docker compose build api && docker compose up -d api`) e front. Nenhuma env nova: `HUB_URL` e `HUB_PLATFORM_KEY` já estão no bloco `environment:` explícito do compose (linhas 206-207).
3. **Clicar:** criar uma sugestão com print pelo E-monitor, ver no hub com o selo "E-monitor", responder lá, ver a resposta e o badge aqui.
4. **Importar:** `docker compose exec api import-suggestions-hub --dry-run`, conferir o número, rodar sem `--dry-run`, guardar o relatório. `exit 1` = alguma falhou; repita só essa com `--only <uuid>`.

---

## Self-review (feito)

- **Cobertura do spec:** §4.1 handler troca de fonte e tradução (Tasks 1, 5, 7, 8); §4.2 `retirada`, selo, face do gestor sem apagar a `CentralDeComando` (Task 10); §4.3 anexos em três passos no servidor, proxy com 302 seguido no servidor, a rota antiga `/{id}/attachments` vira fala com a imagem (Tasks 3, 8); §4.4 badge com cache de 30 s (Tasks 6, 8); §5 importação com relatório, idempotente, cada anexo em nome de quem o enviou (fala sintética quando não é dono do lugar), estado local para pular o já importado, 413 registrado sem parar (Tasks 12, 13); §6 falhas: hub fora do ar → 503 e o rascunho fica (Tasks 7, 11), reenvio com a mesma chave (Task 11), 401 → frase de suporte (Task 1), 404 em vez de 403 (o hub decide; o handler repassa), 200 sem o campo ≠ sucesso (Tasks 1, 4); §7 testes do adaptador em Go e a tela em Playwright (Tasks 1-11).
- **Placeholders:** nenhum TBD. Os dois pontos que dependem do ambiente são conferências com comando exato: a ordem dos argumentos de `storage.New` (Task 13) e o prefixo das partições (Task 14).
- **Consistência de nomes:** `SubirAnexoSugestao`, `AbrirAnexoSugestao`, `ImportarSugestao`, `SugestoesNaoLidas`, `MarcarSugestaoLida`, `ListarSugestoes`, `DetalheSugestao`, `CriarSugestao` e `ComentarSugestao` são usados com a mesma assinatura nas Tasks 1-4, 7, 8 e 13. `detalheDoHub`/`linhaDoHub` na Task 5 = uso nas Tasks 7-8. `SuggestionExport.CreatedByEmail/CreatedByName` (campos embutidos de `catalog.Suggestion`) = uso na Task 13. `useStageSuggestionAttachment` na Task 10 = uso no modal e na thread.
- **Desvio consciente do spec:** o §4.3 fala da plataforma "receber o arquivo como hoje" e comentar com `anexoIds`. A tela daqui passou a subir ANTES (stage + `attachment_ids`), e a rota de antes continua, traduzida como o §4.3 descreve. Assim a imagem fica na descrição ou na mesma fala do texto, em vez de virar uma fala a mais, e o reenvio não duplica. O contrato do hub não muda.

## Respondidas pelo plano do hub (commit 61f59eaf)

1. **Anexo da raiz enviado por outra pessoa:** não sobe em nome do autor, porque isso falsificaria quem enviou. Vira uma fala sintética sem texto, de quem enviou: `idOriginal` `anexo-<attachment_id>`, `corpo: ''`, `createdAt` = data do anexo, e o anexo subido com os headers dessa pessoa. O hub aceita fala vazia com anexo. Só os anexos do próprio autor ficam nos `anexoIds` do topo. Aplicado nas Tasks 12 (`UploaderEmail`/`UploaderName`/`CreatedAt` no export) e 13 (`importaUma`, com dois testes novos e mutação).
2. **Autor apagado:** `autor-removido@e-monitor.invalid`, nome "Pessoa removida". O hub suprime aviso para todo e-mail `.invalid` (hub `21713a74`); `@e-monitor.online` mandaria e-mail de verdade. Aplicado na Task 13.
3. **Órfãos numa reimportação:** o `limpaUploadsOrfaos` do hub recolhe anexo `ready` sem dono depois de 24 h pelo `updatedAt`. Para não subir bytes à toa, o CLI guarda um arquivo de estado local fora do git (`--estado`, padrão `/tmp/import-suggestions-hub.estado.json`) e pula o que já foi importado; `--only` força. Aplicado na Task 13 (`estado.go`).
4. **`sua_vez`:** confirmado: `de`/`para` são as strings `"true"`/`"false"`. A frase da Task 10 fica como está.
5. **`numeroOriginal`:** fica no topo do detalhe (`sugestao.numeroOriginal`, number|null); `origemRef` não sai na resposta. Os tipos da Task 1 já leem assim.
6. **Miniatura:** o confirm sem WebP degrada para `temThumb: false`, e a Central do hub mostra a imagem inteira (plano do hub, Task 10, Step 6b). Aqui a imagem vem pelo proxy de bytes, como hoje, e nunca com `?thumb=1`, que dá 404 sem miniatura. Nota no `AttachmentItem.jsx` (Task 10).
7. **Teto do `/importar`:** 2 MB (o resto da porta, 256 KB). Uma sugestão por chamada, sem partir. Se passar, o hub responde 413 `payload_too_large`, que o `erroDaPorta` repassa com a mensagem; o CLI registra no relatório e segue. Aplicado na Task 13 (`main.go`).
