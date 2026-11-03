package mcp

// Task 17 (Fatia 4): criar_usuario, editar_usuario e desativar_usuario. As três
// são `critico`: a chamada NUNCA executa, grava um pedido pendente, e quem executa
// é a aprovação do dono em /pendentes (b.aprovar, sobre a lista REAL das
// escritas). Cada tool prova: (1) o pedido não escreve, (2) aprovar executa e
// devolve o formato do obter_usuario, (3) o efeito é o da rota HTTP, (4) cada
// recusa no pedido E na aprovação, (5) a auditoria com meta.mcp.pendente_id,
// (6) o segredo (senha gerada, link e token das boas-vindas) nunca aparece no
// resultado, nos args gravados, no preview nem no audit_log, (7) rebaixar ou
// desativar revoga as credenciais do MCP da pessoa (trigger da 0070).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/acoes"
	"radiocheck/internal/api/handlers"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/mcp/tokens"
	"radiocheck/internal/users"
	"radiocheck/internal/welcome"
)

const (
	toolCriarUsuario     = "criar_usuario"
	toolEditarUsuario    = "editar_usuario"
	toolDesativarUsuario = "desativar_usuario"

	portalDosUsuarios = "https://portal.teste/admin/users"
)

// ── fixtures ────────────────────────────────────────────────────────────

// cenarioUsuarios: três clientes com nome conhecido (os previews nomeiam a
// carteira pelo nome, não pelo id).
type cenarioUsuarios struct{ cA, cB, cC uuid.UUID }

const (
	nomeAlfa = "Alfa Mídia"
	nomeBeta = "Beta Mídia"
	nomeGama = "Gama Mídia"
)

func semearClientesDeUsuario(t *testing.T, b *bancadaTool) cenarioUsuarios {
	t.Helper()
	return cenarioUsuarios{
		cA: mcptest.Cliente(t, b.ctx, b.pool, nomeAlfa),
		cB: mcptest.Cliente(t, b.ctx, b.pool, nomeBeta),
		cC: mcptest.Cliente(t, b.ctx, b.pool, nomeGama),
	}
}

// novoUsuarioDe cria um usuário pelo repositório: papel "admin" ou "viewer" (esse
// exige carteira); ativo=false desativa depois de criar.
func novoUsuarioDe(t *testing.T, b *bancadaTool, papel string, ativo bool, carteira ...uuid.UUID) *users.User {
	t.Helper()
	repo := users.NewRepo(b.pool)
	in := users.CreateInput{
		Email: fmt.Sprintf("%s-%s@usuarios.teste", papel, uuid.NewString()[:8]), PasswordHash: "x", Role: papel,
		Name: "Pessoa " + uuid.NewString()[:4],
	}
	if len(carteira) > 0 {
		in.ClientID = &carteira[0]
	}
	u, err := repo.Create(b.ctx, in)
	require.NoError(t, err)
	if len(carteira) > 1 {
		require.NoError(t, repo.SetClients(b.ctx, u.ID, carteira))
	}
	if !ativo {
		_, err = b.pool.Exec(b.ctx, `UPDATE users SET is_active = false WHERE id = $1`, u.ID)
		require.NoError(t, err)
	}
	u, err = repo.Get(b.ctx, u.ID)
	require.NoError(t, err)
	return u
}

// tokenMCPDe dá à pessoa uma conexão de IA viva (token pessoal).
func tokenMCPDe(t *testing.T, b *bancadaTool, id uuid.UUID) {
	t.Helper()
	_, err := b.pool.Exec(b.ctx, `INSERT INTO mcp_tokens (user_id, tipo, token_hash, nome, expires_at)
		VALUES ($1, 'pessoal', $2, 'n', NOW() + INTERVAL '1 day')`, id, "h-"+uuid.NewString())
	require.NoError(t, err)
}

func tokensVivosDe(t *testing.T, b *bancadaTool, id uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_tokens WHERE user_id = $1 AND revoked_at IS NULL`, id).Scan(&n))
	return n
}

// linhaDoUsuario é o usuário sem id, email e datas (comparável entre fixtures
// gêmeas): papel, cliente principal, nome, telefone, ativo, preferências, apagado
// e a carteira inteira na ordem em que o repositório a lê.
func linhaDoUsuario(t *testing.T, b *bancadaTool, id uuid.UUID) string {
	t.Helper()
	u, err := users.NewRepo(b.pool).Get(b.ctx, id)
	require.NoError(t, err)
	principal, tel := "-", "-"
	if u.ClientID != nil {
		principal = u.ClientID.String()
	}
	if u.Phone != nil {
		tel = "'" + *u.Phone + "'"
	}
	return fmt.Sprintf("%s|principal=%s|%s|tel=%s|ativo=%t|alertas=%t|posvenda=%t|apagado=%t|carteira=%v",
		u.Role, principal, u.Name, tel, u.IsActive, u.ReceiveAlertEmails, u.ReceivePostSaleEmails, u.DeletedAt != nil, u.ClientIDs)
}

func contarUsuarios(t *testing.T, b *bancadaTool) int {
	t.Helper()
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM users`).Scan(&n))
	return n
}

func idDoUsuarioPorEmail(t *testing.T, b *bancadaTool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT id FROM users WHERE LOWER(email) = LOWER($1) AND deleted_at IS NULL`, email).Scan(&id))
	return id
}

func exigeAuditoriaDeUsuario(t *testing.T, b *bancadaTool, tool string, id uuid.UUID, pendenteID string) {
	t.Helper()
	l := b.exigeAuditoria(t, "mcp.escrita."+tool, "user", &id)
	require.Equal(t, pendenteID, l.Meta["mcp"].(map[string]any)["pendente_id"])
	require.Equal(t, "critico", l.Meta["nivel"])
	require.Equal(t, tool, l.Meta["tool"])
}

// exigeIgualAoObterUsuario: o resultado da aprovação é, valor por valor, o do
// obter_usuario lido depois.
func exigeIgualAoObterUsuario(t *testing.T, b *bancadaTool, id uuid.UUID, res map[string]any) {
	t.Helper()
	lido := b.chamar(t, "obter_usuario", map[string]any{"usuario_id": id.String()})
	require.Equal(t, lido["usuario"], res["usuario"], "mesmo formato (e mesmos valores) do obter_usuario")
}

func strsDosIDs(ids ...uuid.UUID) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

// ── as boas-vindas de verdade (cifra + convite no banco), com correio falso ──

type correioFalso struct {
	mu       sync.Mutex
	enviados []envio
	erro     error
}

type envio struct {
	Para    []string
	Assunto string
	HTML    string
	Texto   string
}

func (c *correioFalso) Send(_ context.Context, to []string, subject, html, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.erro != nil {
		return c.erro
	}
	c.enviados = append(c.enviados, envio{Para: to, Assunto: subject, HTML: html, Texto: text})
	return nil
}

func (c *correioFalso) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.enviados)
}

// ligarBoasVindas troca o emissor do serviço da bancada pelo welcome.Service
// real (a mesma cifra AES-GCM e o mesmo convite no banco da produção), com um
// correio que só registra. correioLigado=false é "sem SMTP" (email_status disabled).
func ligarBoasVindas(t *testing.T, b *bancadaTool, correioLigado bool) (*welcome.Service, *correioFalso) {
	t.Helper()
	chave := make([]byte, 32)
	_, err := rand.Read(chave)
	require.NoError(t, err)
	cifra, err := welcome.NewCipher(hex.EncodeToString(chave))
	require.NoError(t, err)
	correio := &correioFalso{}
	svc := welcome.New(welcome.Config{
		Repo: welcome.NewRepo(b.pool), Cipher: cifra, Mailer: correio, MailEnabled: correioLigado,
		BaseURL: "https://portal.teste", Log: zap.NewNop(),
	})
	b.E.Acoes.BoasVindas = acoes.BoasVindasDoServico(svc)
	return svc, correio
}

// conviteDe devolve o token do convite mais recente da pessoa e a senha que a
// página de boas-vindas mostra a quem abre o link (a decifrada).
func conviteDe(t *testing.T, b *bancadaTool, svc *welcome.Service, id uuid.UUID) (token, senha string) {
	t.Helper()
	require.NoError(t, b.pool.QueryRow(b.ctx,
		`SELECT token FROM user_welcome_invites WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1`, id).Scan(&token))
	res, err := svc.Resolve(b.ctx, token)
	require.NoError(t, err)
	return token, res.Password
}

var reLinkDeBoasVindas = regexp.MustCompile(`https://portal\.teste/boasvindas/[^\s"'<>)]+`)

// textoDoEmail normaliza o corpo para comparar o email da rota com o da tool: o
// email da pessoa e o link (token) mudam de uma fixture para outra.
func textoDoEmail(e envio, emailDaPessoa string) string {
	t := strings.ReplaceAll(e.Texto, emailDaPessoa, "<email>")
	t = reLinkDeBoasVindas.ReplaceAllString(t, "<link>")
	return e.Assunto + "\n" + t
}

func htmlDoEmail(e envio, emailDaPessoa string) string {
	h := strings.ReplaceAll(e.HTML, emailDaPessoa, "<email>")
	return reLinkDeBoasVindas.ReplaceAllString(h, "<link>")
}

// todoOAudit e todasAsPendentes: tudo o que ficou gravado, como texto, para
// varrer atrás de segredo.
func todoOAudit(t *testing.T, b *bancadaTool) string {
	t.Helper()
	var s string
	require.NoError(t, b.pool.QueryRow(b.ctx,
		`SELECT COALESCE(string_agg(action || ' ' || resource || ' ' || COALESCE(resource_id::text, '') || ' ' || metadata::text, E'\n'), '') FROM audit_log`).Scan(&s))
	return s
}

func todasAsPendentes(t *testing.T, b *bancadaTool) string {
	t.Helper()
	var s string
	require.NoError(t, b.pool.QueryRow(b.ctx,
		`SELECT COALESCE(string_agg(to_jsonb(p)::text, E'\n'), '') FROM mcp_acoes_pendentes p`).Scan(&s))
	return s
}

// pendenteDireta grava o pedido SEM passar pelo Preview do pedido (a bancada
// escreveria a recusa antes): prova o que só a aprovação enxerga.
func pendenteDireta(t *testing.T, b *bancadaTool, tool string, args map[string]any) string {
	t.Helper()
	// Os args canônicos são os do In real da tool: passa pelo mesmo decodificador.
	idx, err := indexarEscritas(escritasDe(b.cfg))
	require.NoError(t, err)
	_, ok := idx[tool]
	require.True(t, ok, tool)
	bruto := []byte(mustJSON(t, args))
	pend, _, err := b.E.Pendentes.Criar(b.ctx, NovaPendente{UserID: b.admin, TokenID: b.p.TokenID, Tool: tool, Args: bruto,
		Motivo: "teste", Preview: previa{Resumo: "r"}}, b.E.agora())
	require.NoError(t, err)
	return pend.ID.String()
}

// ═══════════════════════════════════════════════════════════════════════
// criar_usuario
// ═══════════════════════════════════════════════════════════════════════

func argsCriarUsuario(nome, email, papel string, clientes ...uuid.UUID) map[string]any {
	a := map[string]any{"nome": nome, "email": email, "papel": papel}
	if len(clientes) > 0 {
		a["cliente_ids"] = strsDosIDs(clientes...)
	}
	return a
}

func TestCriarUsuario_PedidoNaoEscreveEAprovarCria(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearClientesDeUsuario(t, b)
	svc, correio := ligarBoasVindas(t, b, true)
	props := propriedades(esquemaDa(t, b.cfg, toolCriarUsuario))
	require.NotContains(t, props, "confirmacao", "crítico nunca recebe token")
	for _, proibido := range []string{"senha", "password", "segredo", "secret", "token"} {
		for nome := range props {
			require.NotContains(t, strings.ToLower(nome), proibido, "o segredo nunca passa pelo chat")
		}
	}
	antes := contarUsuarios(t, b)
	args := argsCriarUsuario("Maria Souza", "  Maria.Souza@Agencia.Teste ", "cliente", c.cA, c.cB)
	args["telefone"] = "(11) 99999-0000"
	args["recebe_alertas"] = false

	out := b.pedePendente(t, toolCriarUsuario, args)

	require.Contains(t, out["motivo"], "manda email para fora e dá acesso")
	require.Equal(t, portalDosUsuarios, out["links"].(map[string]any)["tela"])
	require.Contains(t, out["aprovar_em"], "/pendentes?id="+out["pendente_id"].(string))
	require.Contains(t, out["aviso"], "Nada aconteceu ainda")
	require.Equal(t, antes, contarUsuarios(t, b), "o pedido não cria ninguém")
	require.Zero(t, correio.total(), "o pedido não manda email")
	pv := out["preview"].(map[string]any)
	require.Contains(t, pv["resumo"], "Maria Souza")
	require.Contains(t, pv["resumo"], "maria.souza@agencia.teste", "o email é mostrado como será gravado (minúsculas, sem espaço)")
	texto := mustJSON(t, pv)
	require.Contains(t, texto, nomeAlfa)
	require.Contains(t, texto, nomeBeta)
	require.NotContains(t, texto, nomeGama, "só a carteira pedida")
	require.Contains(t, mustJSON(t, pv["efeitos"]), "email de boas-vindas para maria.souza@agencia.teste com o link e a senha inicial")
	require.Contains(t, mustJSON(t, pv["mudancas"]), "(11) 99999-0000")
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, toolCriarUsuario, pid)
	criada := b.auditoria(t, "mcp.pendente.criada")
	require.Equal(t, "user", criada[0].Recurso)
	require.Nil(t, criada[0].RecursoID, "o usuário ainda não existe")

	ap := b.aprovar(t, pid)

	res := ap.resultadoJSON(t)
	id := idDoUsuarioPorEmail(t, b, "maria.souza@agencia.teste")
	require.Equal(t, antes+1, contarUsuarios(t, b))
	require.Equal(t, id.String(), res["usuario"].(map[string]any)["id"])
	exigeIgualAoObterUsuario(t, b, id, res)
	require.NotContains(t, res, "aviso", "o resultado é exatamente o do obter_usuario: os avisos vão nas ressalvas")
	require.Len(t, ap.Ressalvas, 1)
	require.Contains(t, ap.Ressalvas[0], "enviada à pessoa por email", "com o email enviado a única ressalva é a informativa")
	require.NotContains(t, ap.Ressalvas[0], "não tem a senha")
	u := res["usuario"].(map[string]any)
	require.Equal(t, "cliente", u["papel"])
	require.Equal(t, "ativo", u["situacao"])
	require.Equal(t, false, u["recebe_alertas"])
	require.Len(t, u["clientes"], 2)
	require.Equal(t, "sent", u["boas_vindas"].(map[string]any)["status_email"])
	require.Equal(t, 1, correio.total())
	require.Equal(t, []string{"maria.souza@agencia.teste"}, correio.enviados[0].Para)
	var criadoPor *uuid.UUID
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT created_by FROM user_welcome_invites WHERE user_id = $1`, id).Scan(&criadoPor))
	require.NotNil(t, criadoPor)
	require.Equal(t, b.admin, *criadoPor, "o convite é de quem aprovou")
	exigeAuditoriaDeUsuario(t, b, toolCriarUsuario, id, pid)
	require.Equal(t, "executada", b.estadoDaPendente(t, pid))
	_, senha := conviteDe(t, b, svc, id)
	require.Len(t, senha, acoes.TamanhoSenhaGerada)
}

func TestCriarUsuario_AdministradorSemCarteira(t *testing.T) {
	b := novaBancadaTool(t)
	_, correio := ligarBoasVindas(t, b, true)
	args := argsCriarUsuario("Pedro Admin", "pedro@e-midias.teste", "admin")
	args["recebe_pos_venda"] = true

	out := b.pedePendente(t, toolCriarUsuario, args)

	ef := mustJSON(t, out["preview"].(map[string]any)["efeitos"])
	require.Contains(t, ef, "administrador")
	require.Contains(t, ef, "todos os clientes", "o preview diz o alcance de um admin")
	ap := b.aprovar(t, out["pendente_id"].(string))
	u := ap.resultadoJSON(t)["usuario"].(map[string]any)
	require.Equal(t, "admin", u["papel"])
	require.Empty(t, u["clientes"])
	require.Equal(t, true, u["recebe_pos_venda"])
	require.Equal(t, true, u["recebe_alertas"], "o padrão da coluna")
	require.Equal(t, 1, correio.total())
}

// O efeito é o da rota: duas fixtures, uma pela rota (com a senha explícita, como o
// admin a digitaria), outra pela aprovação. Tudo igual, menos o hash da senha.
func TestCriarUsuario_ParidadeComARota(t *testing.T) {
	casos := []struct {
		nome  string
		papel string // vocabulário da tool
		rota  string // vocabulário da rota
		cart  func(c cenarioUsuarios) []uuid.UUID
		extra map[string]any // campos da tool
		corpo map[string]any // campos extras da rota
	}{
		{"admin com preferências", "admin", "admin", func(cenarioUsuarios) []uuid.UUID { return nil },
			map[string]any{"recebe_alertas": false, "recebe_pos_venda": true},
			map[string]any{"receive_alert_emails": false, "receive_post_sale_emails": true}},
		{"cliente com carteira de duas e telefone", "cliente", "client", func(c cenarioUsuarios) []uuid.UUID { return []uuid.UUID{c.cA, c.cB} },
			map[string]any{"telefone": "(11) 3333-4444"}, map[string]any{"phone": "(11) 3333-4444"}},
		{"cliente de um cliente só", "cliente", "client", func(c cenarioUsuarios) []uuid.UUID { return []uuid.UUID{c.cC} },
			nil, nil},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			b := novaBancadaTool(t)
			c := semearClientesDeUsuario(t, b)
			svc, correio := ligarBoasVindas(t, b, true)
			cart := caso.cart(c)
			emailRota, emailTool := "via-rota@paridade.teste", "via-tool@paridade.teste"

			corpo := map[string]any{"email": emailRota, "password": "senha-da-rota-123", "name": "Fulana de Tal", "role": caso.rota, "send_welcome": true}
			if len(cart) > 0 {
				ids := make([]string, len(cart))
				for i, id := range cart {
					ids[i] = id.String()
				}
				corpo["client_ids"] = ids
			}
			for k, v := range caso.corpo {
				corpo[k] = v
			}
			h := handlers.NewUsersHandler(users.NewRepo(b.pool), svc)
			chamarHandlerCorpo(t, h.Create, http.MethodPost, "/admin/users", corpo, nil, b.admin)

			args := argsCriarUsuario("Fulana de Tal", emailTool, caso.papel, cart...)
			for k, v := range caso.extra {
				args[k] = v
			}
			out := b.pedePendente(t, toolCriarUsuario, args)
			b.aprovar(t, out["pendente_id"].(string))

			idRota, idTool := idDoUsuarioPorEmail(t, b, emailRota), idDoUsuarioPorEmail(t, b, emailTool)
			require.Equal(t, linhaDoUsuario(t, b, idRota), linhaDoUsuario(t, b, idTool), "mesmo usuário e mesma carteira")
			require.Equal(t, 2, correio.total())
			rota, tool := correio.enviados[0], correio.enviados[1]
			require.Equal(t, []string{emailRota}, rota.Para)
			require.Equal(t, []string{emailTool}, tool.Para)
			require.Equal(t, textoDoEmail(rota, emailRota), textoDoEmail(tool, emailTool), "o mesmo email de boas-vindas")
			require.Equal(t, htmlDoEmail(rota, emailRota), htmlDoEmail(tool, emailTool))
			var stR, stT string
			var revR, revT, abrR, abrT bool
			var porR, porT *uuid.UUID
			q := `SELECT email_status, revoked_at IS NULL, opened_at IS NULL, created_by FROM user_welcome_invites WHERE user_id = $1`
			require.NoError(t, b.pool.QueryRow(b.ctx, q, idRota).Scan(&stR, &revR, &abrR, &porR))
			require.NoError(t, b.pool.QueryRow(b.ctx, q, idTool).Scan(&stT, &revT, &abrT, &porT))
			require.Equal(t, stR, stT)
			require.Equal(t, revR, revT)
			require.Equal(t, abrR, abrT)
			require.Equal(t, porR, porT, "o convite é do mesmo ator")
			// A diferença é só a senha: a da rota é a digitada; a da tool, a gerada, e é
			// a que a página de boas-vindas entrega e a que abre a conta.
			_, senhaTool := conviteDe(t, b, svc, idTool)
			_, senhaRota := conviteDe(t, b, svc, idRota)
			require.Equal(t, "senha-da-rota-123", senhaRota)
			require.NotEqual(t, senhaRota, senhaTool)
			var hash string
			require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT password_hash FROM users WHERE id = $1`, idTool).Scan(&hash))
			require.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte(senhaTool)), "a senha do convite é a que abre a conta")
		})
	}
}

// A senha gerada, o link e o token das boas-vindas não aparecem em lugar nenhum
// que o MCP devolve ou grava: nem no pedido, nem nos args da pendente, nem no
// preview, nem no resultado, nem no audit_log (F4-11).
func TestCriarUsuario_SegredoNuncaVaza(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearClientesDeUsuario(t, b)
	svc, _ := ligarBoasVindas(t, b, true)

	out := b.pedePendente(t, toolCriarUsuario, argsCriarUsuario("Sem Vazar", "sem.vazar@paridade.teste", "cliente", c.cA))
	pid := out["pendente_id"].(string)
	respostaDoPedido := mustJSON(t, out)
	pendenteAntes := todasAsPendentes(t, b)

	ap := b.aprovar(t, pid)

	id := idDoUsuarioPorEmail(t, b, "sem.vazar@paridade.teste")
	token, senha := conviteDe(t, b, svc, id)
	link := svc.Link(token)
	require.Len(t, senha, acoes.TamanhoSenhaGerada)
	require.NotEmpty(t, token)
	resultado := string(ap.ResultadoJSON)
	lido := mustJSON(t, b.chamar(t, "obter_usuario", map[string]any{"usuario_id": id.String()}))
	for nome, texto := range map[string]string{
		"resposta do pedido": respostaDoPedido, "pendente antes da aprovação": pendenteAntes,
		"pendente depois": todasAsPendentes(t, b), "resultado da aprovação": resultado, "obter_usuario": lido,
		"audit_log": todoOAudit(t, b), "estado da pendente aprovada": mustJSON(t, ap.Pendente),
	} {
		require.NotContains(t, texto, senha, "a senha gerada vazou em: "+nome)
		require.NotContains(t, texto, token, "o token das boas-vindas vazou em: "+nome)
		require.NotContains(t, texto, link, "o link das boas-vindas vazou em: "+nome)
		require.NotContains(t, texto, "/boasvindas/", "nenhum link de boas-vindas em: "+nome)
	}
	// Os args gravados são os que a pessoa pediu, sem campo de segredo.
	var args map[string]any
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT args FROM mcp_acoes_pendentes WHERE id = $1`, pid).Scan(&args))
	require.ElementsMatch(t, []string{"nome", "email", "papel", "cliente_ids"}, keysDe(args))
}

func keysDe(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestCriarUsuario_ChamadasDistintasGeramSenhasDistintas(t *testing.T) {
	b := novaBancadaTool(t)
	svc, _ := ligarBoasVindas(t, b, true)
	vistas := map[string]bool{}
	for i := 0; i < 5; i++ {
		email := fmt.Sprintf("u%d@senhas.teste", i)
		out := b.pedePendente(t, toolCriarUsuario, argsCriarUsuario("Pessoa", email, "admin"))
		b.aprovar(t, out["pendente_id"].(string))
		_, senha := conviteDe(t, b, svc, idDoUsuarioPorEmail(t, b, email))
		require.False(t, vistas[senha])
		vistas[senha] = true
	}
}

func TestCriarUsuario_EmailNaoSaiuAvisaQueAPessoaNaoTemASenha(t *testing.T) {
	// Sem SMTP na VM a tool recusa antes (TestCriarUsuario_SemSMTPRecusa…): aqui é o envio que
	// FALHA depois de a pessoa já ter sido criada.
	casos := []struct {
		nome          string
		correioLigado bool
		erro          error
		status        string
	}{
		{"o envio falhou", true, fmt.Errorf("smtp: conexão recusada"), "failed"},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			b := novaBancadaTool(t)
			c := semearClientesDeUsuario(t, b)
			_, correio := ligarBoasVindas(t, b, caso.correioLigado)
			correio.erro = caso.erro
			email := "sem.email@falha.teste"

			out := b.pedePendente(t, toolCriarUsuario, argsCriarUsuario("Sem Email", email, "cliente", c.cA))
			ap := b.aprovar(t, out["pendente_id"].(string))

			id := idDoUsuarioPorEmail(t, b, email)
			res := ap.resultadoJSON(t)
			require.Equal(t, caso.status, res["usuario"].(map[string]any)["boas_vindas"].(map[string]any)["status_email"])
			require.NotContains(t, res, "aviso", "o resultado segue sendo o do obter_usuario")
			texto := textoDasRessalvas(ap.Ressalvas)
			require.Contains(t, texto, "o email não saiu")
			require.Contains(t, texto, "não tem a senha")
			require.Contains(t, texto, "redefina pela tela")
			require.Equal(t, ap.Ressalvas, b.ressalvasGravadas(t, out["pendente_id"].(string)), "o aviso fica gravado no pedido")
			exigeIgualAoObterUsuario(t, b, id, res)
			require.Zero(t, correio.total())
			require.NotContains(t, string(ap.ResultadoJSON), "smtp", "o erro do SMTP não sai")
			require.NotContains(t, texto, "smtp")
			require.NotContains(t, todoOAudit(t, b), "smtp")
		})
	}
}

// Agência com 2+ clientes: o usuário nasce com o primeiro e a carteira inteira vem numa segunda
// gravação. Se ela falha, o usuário JÁ existe e o convite não sai: o pedido fica `falhou` (a pessoa
// não deve aprovar de novo, o e-mail já está em uso) com o texto que diz o que ficou feito — não o
// "a execução caiu" genérico, que não diria que existe um usuário sem senha para conferir.
func TestCriarUsuario_CarteiraQueFalhaDeixaOPedidoFalhouComOTextoDoMeioEstado(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearClientesDeUsuario(t, b)
	_, correio := ligarBoasVindas(t, b, true)
	for _, q := range []string{
		`CREATE OR REPLACE FUNCTION tg_teste_carteira_falha_mcp() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN RAISE EXCEPTION 'carteira de teste recusada'; END $$`,
		fmt.Sprintf(`CREATE TRIGGER tg_teste_carteira_falha_mcp BEFORE INSERT ON user_clients
		 FOR EACH ROW WHEN (NEW.client_id = '%s') EXECUTE FUNCTION tg_teste_carteira_falha_mcp()`, c.cB),
	} {
		_, err := b.pool.Exec(b.ctx, q)
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		b.pool.Exec(b.ctx, `DROP TRIGGER IF EXISTS tg_teste_carteira_falha_mcp ON user_clients`)
		b.pool.Exec(b.ctx, `DROP FUNCTION IF EXISTS tg_teste_carteira_falha_mcp()`)
	})
	email := "agencia@meioestado.teste"
	out := b.pedePendente(t, toolCriarUsuario, argsCriarUsuario("Agência", email, "cliente", c.cA, c.cB))
	pid := out["pendente_id"].(string)

	_, err := b.tentarAprovar(t, pid)

	require.ErrorIs(t, err, ErrExecucaoInterrompida)
	texto := mensagemDaInterrupcao(err)
	require.Contains(t, texto, email)
	require.Contains(t, texto, "foi criado")
	require.Contains(t, texto, "NÃO foi enviado")
	require.Contains(t, texto, "a pessoa não tem a senha")
	require.NotContains(t, texto, "recusada", "o erro do banco não chega à pessoa")
	require.Equal(t, "falhou", b.estadoDaPendente(t, pid))
	require.Equal(t, texto, *erroDaPendente(t, b, pid))
	require.NotEqual(t, msgInterrompida, texto)
	id := idDoUsuarioPorEmail(t, b, email)
	require.NotEqual(t, uuid.Nil, id, "o usuário existe")
	require.Zero(t, correio.total(), "o convite não saiu")
	require.Empty(t, b.auditoria(t, "mcp.escrita.criar_usuario"))
}

// O aviso "a pessoa não tem a senha" não pode morrer no momento do clique: quem recarrega
// /pendentes?id= e a IA, que consulta minhas_pendentes depois, leem a mesma ressalva gravada.
func TestCriarUsuario_AvisoDoEmailVoltaPorHTTPEPorMinhasPendentes(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearClientesDeUsuario(t, b)
	_, correio := ligarBoasVindas(t, b, true)
	correio.erro = fmt.Errorf("smtp: conexão recusada")
	out := b.pedePendente(t, toolCriarUsuario, argsCriarUsuario("Sem Email", "sem.email@volta.teste", "cliente", c.cA))
	pid := out["pendente_id"].(string)
	ap := b.aprovar(t, pid)
	require.Contains(t, textoDasRessalvas(ap.Ressalvas), "a pessoa não tem a senha")

	apr, err := NovasAprovacoes(b.cfg)
	require.NoError(t, err)
	h := &PendentesHTTP{Tokens: tokens.NovoStore(b.pool, []byte(mcptest.SegredoTeste)), Pendentes: b.E.Pendentes, Aprovacoes: apr, Log: zap.NewNop()}
	corpo := chamarHandlerCorpo(t, h.Listar, http.MethodGet, "/mcp/pendentes?id="+pid, nil, nil, b.admin)
	var lista map[string]any
	require.NoError(t, json.Unmarshal(corpo, &lista), string(corpo))
	doHTTP := lista["destaque"].(map[string]any)["ressalvas"].([]any)
	require.Contains(t, mustJSON(t, doHTTP), "a pessoa não tem a senha", "a página do pedido mostra o aviso de novo")

	pelaIA := itens(t, b.chamar(t, "minhas_pendentes", map[string]any{"estado": "executada"}))
	require.Len(t, pelaIA, 1)
	require.Contains(t, mustJSON(t, pelaIA[0]["ressalvas"]), "a pessoa não tem a senha", "a IA lê o aviso em minhas_pendentes")
	require.Contains(t, mustJSON(t, pelaIA[0]["ressalvas"]), "redefina pela tela")
}

// quebrarReleituraDeUsuario faz SÓ a releitura do usuário (montarUsuario) falhar: a
// leitura dos convites vai para um pool fechado. A criação e o convite usam outros
// repositórios e seguem funcionando — a escrita acontece, o resultado fica mínimo.
func quebrarReleituraDeUsuario(t *testing.T, b *bancadaTool) {
	t.Helper()
	b.E.Leitura.BoasVindas = welcome.NewRepo(b.poolFechado(t))
}

// O furo que as ressalvas fecham: o aviso "a pessoa não tem a senha" (F4-11, o crítico) não
// pode depender de a releitura do usuário ter dado certo. Com a releitura quebrada o resultado
// é só {usuario:{id}} — e as duas ressalvas (a do email e a de conferir) chegam ao Aprovado,
// ao pedido gravado e à resposta HTTP do /aprovar.
func TestCriarUsuario_AvisoDoEmailSobreviveAReleituraQueFalha(t *testing.T) {
	for _, caso := range []struct {
		nome       string
		erroEmail  error
		naRessalva []string
		fora       string
	}{
		{"email falhou", fmt.Errorf("smtp: conexão recusada"), []string{"o email não saiu", "não tem a senha", "não consegui reler o usuário", "obter_usuario"}, ""},
		{"email saiu", nil, []string{"enviada à pessoa por email", "não consegui reler o usuário"}, "não tem a senha"},
	} {
		for _, via := range []string{"aprovação", "rota HTTP"} {
			t.Run(caso.nome+"/"+via, func(t *testing.T) {
				b := novaBancadaTool(t)
				c := semearClientesDeUsuario(t, b)
				_, correio := ligarBoasVindas(t, b, true)
				correio.erro = caso.erroEmail
				quebrarReleituraDeUsuario(t, b)
				email := "releitura@falha.teste"
				out := b.pedePendente(t, toolCriarUsuario, argsCriarUsuario("Sem Releitura", email, "cliente", c.cA))
				pid := out["pendente_id"].(string)

				var res map[string]any
				var ressalvas []string
				if via == "aprovação" {
					ap := b.aprovar(t, pid)
					res, ressalvas = ap.resultadoJSON(t), ap.Ressalvas
				} else {
					corpo := b.aprovarPelaRota(t, pid)
					res = corpo["resultado"].(map[string]any)
					for _, r := range corpo["ressalvas"].([]any) {
						ressalvas = append(ressalvas, r.(string))
					}
				}

				id := idDoUsuarioPorEmail(t, b, email) // a escrita aconteceu
				require.Equal(t, map[string]any{"usuario": map[string]any{"id": id.String()}}, res, "a releitura caiu: só o id")
				texto := textoDasRessalvas(ressalvas)
				for _, trecho := range caso.naRessalva {
					require.Contains(t, texto, trecho)
				}
				if caso.fora != "" {
					require.NotContains(t, texto, caso.fora)
				}
				require.Equal(t, ressalvas, b.ressalvasGravadas(t, pid), "o pedido grava o que a pessoa leu")
				require.Equal(t, "executada", b.estadoDaPendente(t, pid))
			})
		}
	}
}

func TestCriarUsuario_EmailDeUsuarioApagadoPodeSerReusado(t *testing.T) {
	b := novaBancadaTool(t)
	_, _ = ligarBoasVindas(t, b, true)
	velho := novoUsuarioDe(t, b, "admin", true)
	require.NoError(t, users.NewRepo(b.pool).SoftDelete(b.ctx, velho.ID))

	out := b.pedePendente(t, toolCriarUsuario, argsCriarUsuario("Volta", velho.Email, "admin"))
	ap := b.aprovar(t, out["pendente_id"].(string))

	require.NotEqual(t, velho.ID.String(), ap.resultadoJSON(t)["usuario"].(map[string]any)["id"])
}

func TestCriarUsuario_RecusasNoPedido(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearClientesDeUsuario(t, b)
	_, correio := ligarBoasVindas(t, b, true)
	existente := novoUsuarioDe(t, b, "admin", true)
	base := argsCriarUsuario("Pessoa Nova", "nova@recusas.teste", "cliente", c.cA)
	com := func(chave string, valor any) map[string]any {
		a := map[string]any{}
		for k, v := range base {
			a[k] = v
		}
		if valor == nil {
			delete(a, chave)
		} else {
			a[chave] = valor
		}
		return a
	}
	antes := contarUsuarios(t, b)

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		texto  string // trecho esperado da mensagem
	}{
		"nome vazio":                          {com("nome", "   "), "[entrada_invalida]", "nome"},
		"nome com 201 caracteres":             {com("nome", strings.Repeat("é", 201)), "[entrada_invalida]", "200"},
		"telefone com 41 caracteres":          {com("telefone", strings.Repeat("1", 41)), "[entrada_invalida]", "40"},
		"email vazio":                         {com("email", " "), "[entrada_invalida]", "email"},
		"email sem arroba":                    {com("email", "maria.agencia.teste"), "[entrada_invalida]", "email"},
		"email com espaço no meio":            {com("email", "maria souza@agencia.teste"), "[entrada_invalida]", "email"},
		"email com nome de exibição":          {com("email", "Maria <maria@agencia.teste>"), "[entrada_invalida]", "email"},
		"email sem domínio":                   {com("email", "maria@"), "[entrada_invalida]", "email"},
		"papel vazio":                         {com("papel", ""), "[entrada_invalida]", "admin ou cliente"},
		"papel em inglês":                     {com("papel", "client"), "[entrada_invalida]", "admin ou cliente"},
		"papel operator":                      {com("papel", "operator"), "[entrada_invalida]", "admin ou cliente"},
		"cliente sem carteira":                {com("cliente_ids", nil), "[client_id_required]", "cliente_ids"},
		"cliente com lista vazia":             {com("cliente_ids", []any{}), "[client_id_required]", "listar_clientes"},
		"admin com carteira":                  {com("papel", "admin"), "[client_id_not_allowed_for_admin]", "cliente_ids"},
		"cliente_id inválido":                 {com("cliente_ids", []any{"xyz"}), "[entrada_invalida]", "cliente_ids"},
		"cliente inexistente":                 {com("cliente_ids", strsDosIDs(c.cA, uuid.New())), "[client_not_found]", "não existe"},
		"email já em uso":                     {com("email", existente.Email), "[email_taken]", existente.Email},
		"email em uso com outra caixa":        {com("email", strings.ToUpper(existente.Email)), "[email_taken]", "já existe"},
		"email em uso com espaços nas pontas": {com("email", "  "+existente.Email+"  "), "[email_taken]", "já existe"},
	} {
		texto := b.chamarErro(t, toolCriarUsuario, caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		require.Contains(t, texto, caso.texto, nome)
	}
	require.Equal(t, antes, contarUsuarios(t, b))
	require.Zero(t, correio.total())
	require.Zero(t, b.totalAuditorias(t), "recusa não deixa pedido nem auditoria")
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_acoes_pendentes`).Scan(&n))
	require.Zero(t, n)

	// Os limites que passam: 200 caracteres de nome (contados em caracteres, não em bytes).
	b.pedePendente(t, toolCriarUsuario, com("nome", strings.Repeat("é", 200)))
	b.pedePendente(t, toolCriarUsuario, com("telefone", strings.Repeat("1", 40)))
}

// Sem a chave de cifra do convite a senha não chega à pessoa: a tool recusa antes
// de criar alguém que ninguém consegue acessar.
func TestCriarUsuario_BoasVindasIndisponivelRecusaNoPedido(t *testing.T) {
	c := func(t *testing.T) (*bancadaTool, map[string]any) {
		b := novaBancadaTool(t)
		cli := semearClientesDeUsuario(t, b)
		return b, argsCriarUsuario("Pessoa Nova", "nova@sem.boasvindas.teste", "cliente", cli.cA)
	}
	t.Run("sem emissor", func(t *testing.T) {
		b, args := c(t)
		b.E.Acoes.BoasVindas = nil
		texto := b.chamarErro(t, toolCriarUsuario, args)
		require.Contains(t, texto, "[boas_vindas_indisponivel]")
		require.Contains(t, texto, "WELCOME_ENC_KEY")
		require.Contains(t, texto, "/admin/users", "a saída é criar pela tela")
		require.Zero(t, b.totalAuditorias(t))
	})
	t.Run("serviço sem chave de cifra", func(t *testing.T) {
		b, args := c(t)
		b.E.Acoes.BoasVindas = acoes.BoasVindasDoServico(welcome.New(welcome.Config{Repo: welcome.NewRepo(b.pool), Log: zap.NewNop()}))
		require.Contains(t, b.chamarErro(t, toolCriarUsuario, args), "[boas_vindas_indisponivel]")
	})
	t.Run("adaptador com serviço nil (o que o main monta sem chave)", func(t *testing.T) {
		b, args := c(t)
		b.E.Acoes.BoasVindas = acoes.BoasVindasDoServico(nil)
		require.Contains(t, b.chamarErro(t, toolCriarUsuario, args), "[boas_vindas_indisponivel]")
		require.Equal(t, 1, contarUsuarios(t, b), "só o admin da bancada existe")
	})
}

// Sem SMTP na VM o email de boas-vindas não sai (email_status disabled) e a pessoa criada nunca
// receberia a senha: a tool recusa antes de criar alguém (como reenviar_pos_venda recusa com
// email_desligado), no pedido e na aprovação. A saída é a tela, onde o link aparece para copiar.
func TestCriarUsuario_SemSMTPRecusaNoPedidoENaAprovacao(t *testing.T) {
	t.Run("no pedido", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearClientesDeUsuario(t, b)
		_, correio := ligarBoasVindas(t, b, false)
		antes := contarUsuarios(t, b)

		texto := b.chamarErro(t, toolCriarUsuario, argsCriarUsuario("Pessoa Nova", "nova@sem.smtp.teste", "cliente", c.cA))

		require.Contains(t, texto, "[email_desligado]")
		require.Contains(t, texto, "SMTP")
		require.Contains(t, texto, "/admin/users", "a saída é criar pela tela")
		require.Equal(t, antes, contarUsuarios(t, b))
		require.Zero(t, correio.total())
		require.Zero(t, b.totalAuditorias(t), "recusa não deixa pedido nem auditoria")
	})
	t.Run("na aprovação", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearClientesDeUsuario(t, b)
		ligarBoasVindas(t, b, true)
		out := b.pedePendente(t, toolCriarUsuario, argsCriarUsuario("Pessoa Nova", "nova@sem.smtp.teste", "cliente", c.cA))
		antes := contarUsuarios(t, b)
		ligarBoasVindas(t, b, false) // o SMTP caiu no meio

		exigeRecusaNaAprovacao(t, b, toolCriarUsuario, out["pendente_id"].(string), "email_desligado")
		require.Equal(t, antes, contarUsuarios(t, b))
	})
}

// O estado muda entre o pedido e a aprovação: a recusa vem do Preview rerodado e
// nada é criado.
func TestCriarUsuario_RecusaNaAprovacao(t *testing.T) {
	pedir := func(t *testing.T) (*bancadaTool, cenarioUsuarios, string) {
		b := novaBancadaTool(t)
		c := semearClientesDeUsuario(t, b)
		ligarBoasVindas(t, b, true)
		out := b.pedePendente(t, toolCriarUsuario, argsCriarUsuario("Pessoa Nova", "nova@aprovacao.teste", "cliente", c.cA, c.cB))
		return b, c, out["pendente_id"].(string)
	}
	t.Run("o email foi tomado no meio", func(t *testing.T) {
		b, _, pid := pedir(t)
		antes := contarUsuarios(t, b)
		_, err := users.NewRepo(b.pool).Create(b.ctx, users.CreateInput{Email: "Nova@Aprovacao.Teste", PasswordHash: "x", Role: "admin", Name: "Outra"})
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolCriarUsuario, pid, "email_taken")
		require.Equal(t, antes+1, contarUsuarios(t, b), "só o usuário que tomou o email")
	})
	t.Run("um cliente da carteira foi apagado no meio", func(t *testing.T) {
		b, c, pid := pedir(t)
		antes := contarUsuarios(t, b)
		_, err := b.pool.Exec(b.ctx, `DELETE FROM clients WHERE id = $1`, c.cB)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolCriarUsuario, pid, "client_not_found")
		require.Equal(t, antes, contarUsuarios(t, b))
	})
	t.Run("as boas-vindas deixaram de estar disponíveis no meio", func(t *testing.T) {
		b, _, pid := pedir(t)
		antes := contarUsuarios(t, b)
		b.E.Acoes.BoasVindas = nil

		exigeRecusaNaAprovacao(t, b, toolCriarUsuario, pid, "boas_vindas_indisponivel")
		require.Equal(t, antes, contarUsuarios(t, b))
	})
}

func TestCriarUsuario_ChamadaRepetidaNaoCriaDuasVezes(t *testing.T) {
	b := novaBancadaTool(t)
	_, correio := ligarBoasVindas(t, b, true)
	args := argsCriarUsuario("Repetida", "repetida@pedido.teste", "admin")

	primeiro := b.pedePendente(t, toolCriarUsuario, args)
	segundo := b.pedePendente(t, toolCriarUsuario, args)

	require.Equal(t, primeiro["pendente_id"], segundo["pendente_id"])
	b.aprovar(t, primeiro["pendente_id"].(string))
	require.Equal(t, 1, correio.total(), "um email só")
}

func TestCriarUsuario_DescricaoDizQueASenhaNaoPassaPeloChat(t *testing.T) {
	for _, tl := range listarToolsDoServidor(t) {
		if tl.Name != toolCriarUsuario {
			continue
		}
		for _, trecho := range []string{
			"email de boas-vindas", "gera", "senha", "nunca peça nem informe senha", "boas_vindas_indisponivel", "email_taken",
			"client_id_required", "client_id_not_allowed_for_admin", "client_not_found", "obter_usuario", "cliente_ids",
		} {
			require.Contains(t, tl.Description, trecho)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════════
// editar_usuario
// ═══════════════════════════════════════════════════════════════════════

func argsEditarUsuario(id uuid.UUID, extra map[string]any) map[string]any {
	a := map[string]any{"usuario_id": id.String()}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

func TestEditarUsuario_PedidoNaoEscreveEAprovarEdita(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearClientesDeUsuario(t, b)
	alvo := novoUsuarioDe(t, b, "viewer", true, c.cA, c.cB)
	antes := linhaDoUsuario(t, b, alvo.ID)
	require.NotContains(t, propriedades(esquemaDa(t, b.cfg, toolEditarUsuario)), "confirmacao", "crítico nunca recebe token")

	out := b.pedePendente(t, toolEditarUsuario, argsEditarUsuario(alvo.ID, map[string]any{
		"nome": "  Nome Novo  ", "telefone": "(21) 98888-7777",
		"adicionar_clientes": strsDosIDs(c.cC), "remover_clientes": strsDosIDs(c.cA), "recebe_pos_venda": true,
	}))

	require.Contains(t, out["motivo"], "muda acesso a dados de cliente")
	require.Equal(t, portalDosUsuarios, out["links"].(map[string]any)["tela"])
	require.Equal(t, antes, linhaDoUsuario(t, b, alvo.ID), "o pedido não escreve")
	pv := out["preview"].(map[string]any)
	require.Contains(t, pv["resumo"], alvo.Name)
	require.Contains(t, pv["resumo"], alvo.Email)
	mud := mustJSON(t, pv["mudancas"])
	require.Contains(t, mud, "Nome Novo", "o nome é aparado")
	require.Contains(t, mud, nomeAlfa+", "+nomeBeta, "a carteira de hoje, por nome")
	require.Contains(t, mud, nomeBeta+", "+nomeGama, "a carteira depois: Alfa sai, Gama entra")
	ef := mustJSON(t, pv["efeitos"])
	require.Contains(t, ef, nomeAlfa, "quem sai da carteira é nomeado")
	require.Contains(t, ef, "deixa de ver")
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, toolEditarUsuario, pid)
	criada := b.auditoria(t, "mcp.pendente.criada")
	require.Equal(t, "user", criada[0].Recurso)
	require.Equal(t, alvo.ID, *criada[0].RecursoID)

	ap := b.aprovar(t, pid)

	res := ap.resultadoJSON(t)
	exigeIgualAoObterUsuario(t, b, alvo.ID, res)
	u := res["usuario"].(map[string]any)
	require.Equal(t, "Nome Novo", u["nome"])
	require.Equal(t, true, u["recebe_pos_venda"])
	var nomes []string
	for _, cl := range u["clientes"].([]any) {
		nomes = append(nomes, cl.(map[string]any)["nome"].(string))
	}
	require.Equal(t, []string{nomeBeta, nomeGama}, nomes)
	require.Contains(t, linhaDoUsuario(t, b, alvo.ID), "tel='(21) 98888-7777'")
	exigeAuditoriaDeUsuario(t, b, toolEditarUsuario, alvo.ID, pid)
	require.Equal(t, "executada", b.estadoDaPendente(t, pid))
}

// O resultado da carteira é a de AGORA + adicionar − remover, calculado de novo na
// aprovação: se alguém mexeu na carteira entre o pedido e a aprovação, o que ele
// fez fica. O resumo que a pessoa aprovou ("A, B → B, C") já não diz isso — então a
// primeira aprovação é recusada (preview_desatualizado) com o resumo novo, e é a
// segunda, com a carteira nova à vista, que executa o merge.
func TestEditarUsuario_CarteiraEhMergeDoEstadoDaAprovacao(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearClientesDeUsuario(t, b)
	alvo := novoUsuarioDe(t, b, "viewer", true, c.cA, c.cB)
	out := b.pedePendente(t, toolEditarUsuario, argsEditarUsuario(alvo.ID, map[string]any{
		"adicionar_clientes": strsDosIDs(c.cC), "remover_clientes": strsDosIDs(c.cA),
	}))
	// No meio, outra pessoa troca a carteira por [A, B, D].
	d := mcptest.Cliente(t, b.ctx, b.pool, "Delta Mídia")
	require.NoError(t, users.NewRepo(b.pool).SetClients(b.ctx, alvo.ID, []uuid.UUID{c.cA, c.cB, d}))

	novo := exigeResumoDesatualizado(t, b, toolEditarUsuario, out["pendente_id"].(string))
	require.Contains(t, mustJSON(t, novo.Mudancas), "Delta Mídia", "o resumo novo já mostra a carteira que o outro deixou")

	ap := b.aprovar(t, out["pendente_id"].(string))

	var nomes []string
	for _, cl := range ap.resultadoJSON(t)["usuario"].(map[string]any)["clientes"].([]any) {
		nomes = append(nomes, cl.(map[string]any)["nome"].(string))
	}
	require.ElementsMatch(t, []string{nomeBeta, "Delta Mídia", nomeGama}, nomes, "A saiu, C entrou, e o D de quem mexeu no meio ficou")
}

func TestEditarUsuario_RebaixarAdminRevogaAsConexoesDeIA(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearClientesDeUsuario(t, b)
	alvo := novoUsuarioDe(t, b, "admin", true)
	outro := novoUsuarioDe(t, b, "admin", true)
	tokenMCPDe(t, b, alvo.ID)
	tokenMCPDe(t, b, outro.ID)

	out := b.pedePendente(t, toolEditarUsuario, argsEditarUsuario(alvo.ID, map[string]any{
		"papel": "cliente", "adicionar_clientes": strsDosIDs(c.cA, c.cB),
	}))

	ef := mustJSON(t, out["preview"].(map[string]any)["efeitos"])
	require.Contains(t, ef, "revoga as conexões de IA dessa pessoa")
	require.Contains(t, ef, "deixa de ser administrador")
	require.Contains(t, mustJSON(t, out["preview"].(map[string]any)["mudancas"]), "administrador")
	require.Equal(t, 1, tokensVivosDe(t, b, alvo.ID), "o pedido não revoga nada")

	ap := b.aprovar(t, out["pendente_id"].(string))

	u := ap.resultadoJSON(t)["usuario"].(map[string]any)
	require.Equal(t, "cliente", u["papel"])
	require.Len(t, u["clientes"], 2)
	require.Zero(t, tokensVivosDe(t, b, alvo.ID), "o trigger da 0070 revogou as credenciais")
	require.Equal(t, 1, tokensVivosDe(t, b, outro.ID), "as de outro admin ficam")
}

func TestEditarUsuario_MudarSoONomeNaoRevogaNada(t *testing.T) {
	b := novaBancadaTool(t)
	alvo := novoUsuarioDe(t, b, "admin", true)
	tokenMCPDe(t, b, alvo.ID)

	out := b.pedePendente(t, toolEditarUsuario, argsEditarUsuario(alvo.ID, map[string]any{"nome": "Só O Nome"}))

	require.NotContains(t, mustJSON(t, out["preview"].(map[string]any)["efeitos"]), "revoga")
	b.aprovar(t, out["pendente_id"].(string))
	require.Equal(t, 1, tokensVivosDe(t, b, alvo.ID))
}

func TestEditarUsuario_ClienteVirandoAdminEsvaziaACarteira(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearClientesDeUsuario(t, b)
	alvo := novoUsuarioDe(t, b, "viewer", true, c.cA, c.cB)

	out := b.pedePendente(t, toolEditarUsuario, argsEditarUsuario(alvo.ID, map[string]any{"papel": "admin"}))

	ef := mustJSON(t, out["preview"].(map[string]any)["efeitos"])
	require.Contains(t, ef, "todos os clientes")
	require.Contains(t, ef, "carteira")
	ap := b.aprovar(t, out["pendente_id"].(string))
	u := ap.resultadoJSON(t)["usuario"].(map[string]any)
	require.Equal(t, "admin", u["papel"])
	require.Empty(t, u["clientes"])
}

func TestEditarUsuario_PapelIgualAoAtualNaoContaComoMudanca(t *testing.T) {
	b := novaBancadaTool(t)
	// O próprio admin pode editar o que não é o papel, mesmo repetindo o papel que já tem.
	out := b.pedePendente(t, toolEditarUsuario, argsEditarUsuario(b.admin, map[string]any{"papel": "admin", "nome": "Eu Mesmo"}))
	b.aprovar(t, out["pendente_id"].(string))
	var nome string
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT name FROM users WHERE id = $1`, b.admin).Scan(&nome))
	require.Equal(t, "Eu Mesmo", nome)
}

// O efeito é o da rota PATCH: duas fixtures idênticas, uma pela rota e a outra pela
// aprovação. Papel, cliente principal, carteira, preferências e a revogação das
// conexões de IA são os mesmos.
func TestEditarUsuario_ParidadeComARota(t *testing.T) {
	casos := []struct {
		nome   string
		papel  string
		ativo  bool
		cart   func(c cenarioUsuarios) []uuid.UUID
		tool   func(c cenarioUsuarios) map[string]any
		rota   func(c cenarioUsuarios, u *users.User) map[string]any
		tokens int // conexões de IA vivas depois
	}{
		{"admin vira cliente com carteira", "admin", true,
			func(cenarioUsuarios) []uuid.UUID { return nil },
			func(c cenarioUsuarios) map[string]any {
				return map[string]any{"papel": "cliente", "adicionar_clientes": strsDosIDs(c.cA, c.cB)}
			},
			func(c cenarioUsuarios, _ *users.User) map[string]any {
				return map[string]any{"role": "client", "client_ids": []string{c.cA.String(), c.cB.String()}}
			}, 0},
		{"cliente vira admin", "viewer", true,
			func(c cenarioUsuarios) []uuid.UUID { return []uuid.UUID{c.cA, c.cB} },
			func(cenarioUsuarios) map[string]any { return map[string]any{"papel": "admin"} },
			func(cenarioUsuarios, *users.User) map[string]any { return map[string]any{"role": "admin"} }, 1},
		{"carteira: sai A, entra C", "viewer", true,
			func(c cenarioUsuarios) []uuid.UUID { return []uuid.UUID{c.cA, c.cB} },
			func(c cenarioUsuarios) map[string]any {
				return map[string]any{"adicionar_clientes": strsDosIDs(c.cC), "remover_clientes": strsDosIDs(c.cA)}
			},
			func(c cenarioUsuarios, _ *users.User) map[string]any {
				return map[string]any{"client_ids": []string{c.cB.String(), c.cC.String()}}
			}, 1},
		{"carteira: só o principal sai", "viewer", true,
			func(c cenarioUsuarios) []uuid.UUID { return []uuid.UUID{c.cA, c.cB, c.cC} },
			func(c cenarioUsuarios) map[string]any { return map[string]any{"remover_clientes": strsDosIDs(c.cA)} },
			// Quem fica, na ordem em que a carteira é lida (as ligações criadas juntas
			// têm o mesmo created_at e saem pelo id): é a ordem que decide o novo principal.
			func(c cenarioUsuarios, u *users.User) map[string]any {
				var ficam []string
				for _, id := range u.ClientIDs {
					if id != c.cA {
						ficam = append(ficam, id.String())
					}
				}
				return map[string]any{"client_ids": ficam}
			}, 1},
		{"nome, telefone e preferências", "admin", true,
			func(cenarioUsuarios) []uuid.UUID { return nil },
			func(cenarioUsuarios) map[string]any {
				return map[string]any{"nome": "Outro Nome", "telefone": "123", "recebe_alertas": false, "recebe_pos_venda": true}
			},
			func(cenarioUsuarios, *users.User) map[string]any {
				return map[string]any{"name": "Outro Nome", "phone": "123", "receive_alert_emails": false, "receive_post_sale_emails": true}
			}, 1},
		{"telefone apagado com texto vazio", "viewer", true,
			func(c cenarioUsuarios) []uuid.UUID { return []uuid.UUID{c.cA} },
			func(cenarioUsuarios) map[string]any { return map[string]any{"telefone": ""} },
			func(cenarioUsuarios, *users.User) map[string]any { return map[string]any{"phone": ""} }, 1},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			b := novaBancadaTool(t)
			c := semearClientesDeUsuario(t, b)
			// A tool exige que o telefone mude: as fixtures partem de um telefone.
			gemeo := func() *users.User {
				u := novoUsuarioDe(t, b, caso.papel, caso.ativo, caso.cart(c)...)
				_, err := b.pool.Exec(b.ctx, `UPDATE users SET phone = '999', name = 'Pessoa Gêmea' WHERE id = $1`, u.ID)
				require.NoError(t, err)
				tokenMCPDe(t, b, u.ID)
				return u
			}
			viaRota, viaTool := gemeo(), gemeo()
			require.Equal(t, linhaDoUsuario(t, b, viaRota.ID), linhaDoUsuario(t, b, viaTool.ID), "fixture: gêmeas")
			h := handlers.NewUsersHandler(users.NewRepo(b.pool), nil)

			chamarHandlerCorpo(t, h.Patch, http.MethodPatch, "/admin/users/"+viaRota.ID.String(), caso.rota(c, viaRota), map[string]string{"id": viaRota.ID.String()}, b.admin)
			out := b.pedePendente(t, toolEditarUsuario, argsEditarUsuario(viaTool.ID, caso.tool(c)))
			b.aprovar(t, out["pendente_id"].(string))

			require.Equal(t, linhaDoUsuario(t, b, viaRota.ID), linhaDoUsuario(t, b, viaTool.ID), "mesma linha e mesma carteira")
			require.Equal(t, tokensVivosDe(t, b, viaRota.ID), tokensVivosDe(t, b, viaTool.ID), "mesma revogação das conexões de IA")
			require.Equal(t, caso.tokens, tokensVivosDe(t, b, viaTool.ID))
		})
	}
}

func TestEditarUsuario_RecusasNoPedido(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearClientesDeUsuario(t, b)
	cliente := novoUsuarioDe(t, b, "viewer", true, c.cA, c.cB)
	admin2 := novoUsuarioDe(t, b, "admin", true)
	apagado := novoUsuarioDe(t, b, "viewer", true, c.cA)
	require.NoError(t, users.NewRepo(b.pool).SoftDelete(b.ctx, apagado.ID))
	linhaCliente, linhaAdmin2 := linhaDoUsuario(t, b, cliente.ID), linhaDoUsuario(t, b, admin2.ID)

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		texto  string
	}{
		"usuario_id zerado":                     {argsEditarUsuario(uuid.Nil, map[string]any{"nome": "X"}), "[nao_encontrado]", "usuário"},
		"usuario_id em texto":                   {map[string]any{"usuario_id": "xyz", "nome": "X"}, "[entrada_invalida]", "usuario_id"},
		"usuário inexistente":                   {argsEditarUsuario(uuid.New(), map[string]any{"nome": "X"}), "[nao_encontrado]", "usuário"},
		"usuário apagado":                       {argsEditarUsuario(apagado.ID, map[string]any{"nome": "X"}), "[usuario_apagado]", "apagado"},
		"nenhuma mudança informada":             {argsEditarUsuario(cliente.ID, nil), "[entrada_invalida]", "ao menos uma mudança"},
		"listas vazias não são mudança":         {argsEditarUsuario(cliente.ID, map[string]any{"adicionar_clientes": []any{}, "remover_clientes": []any{}}), "[entrada_invalida]", "ao menos uma mudança"},
		"nome vazio":                            {argsEditarUsuario(cliente.ID, map[string]any{"nome": "  "}), "[entrada_invalida]", "nome"},
		"nome com 201 caracteres":               {argsEditarUsuario(cliente.ID, map[string]any{"nome": strings.Repeat("é", 201)}), "[entrada_invalida]", "200"},
		"telefone com 41 caracteres":            {argsEditarUsuario(cliente.ID, map[string]any{"telefone": strings.Repeat("1", 41)}), "[entrada_invalida]", "40"},
		"papel inválido":                        {argsEditarUsuario(cliente.ID, map[string]any{"papel": "client"}), "[entrada_invalida]", "admin ou cliente"},
		"o próprio papel":                       {argsEditarUsuario(b.admin, map[string]any{"papel": "cliente", "adicionar_clientes": strsDosIDs(c.cA)}), "[proprio_usuario]", "outro administrador"},
		"adicionar cliente que não existe":      {argsEditarUsuario(cliente.ID, map[string]any{"adicionar_clientes": strsDosIDs(uuid.New())}), "[client_not_found]", "não existe"},
		"adicionar id inválido":                 {argsEditarUsuario(cliente.ID, map[string]any{"adicionar_clientes": []any{"xyz"}}), "[entrada_invalida]", "adicionar_clientes"},
		"remover id inválido":                   {argsEditarUsuario(cliente.ID, map[string]any{"remover_clientes": []any{"xyz"}}), "[entrada_invalida]", "remover_clientes"},
		"o mesmo cliente nas duas listas":       {argsEditarUsuario(cliente.ID, map[string]any{"adicionar_clientes": strsDosIDs(c.cC), "remover_clientes": strsDosIDs(c.cC)}), "[entrada_invalida]", "adicionar_clientes e em remover_clientes"},
		"esvaziar a carteira de um cliente":     {argsEditarUsuario(cliente.ID, map[string]any{"remover_clientes": strsDosIDs(c.cA, c.cB)}), "[client_id_required]", "desativar_usuario"},
		"admin virando cliente sem carteira":    {argsEditarUsuario(admin2.ID, map[string]any{"papel": "cliente"}), "[client_id_required]", "adicionar_clientes"},
		"carteira para um admin":                {argsEditarUsuario(admin2.ID, map[string]any{"adicionar_clientes": strsDosIDs(c.cA)}), "[client_id_not_allowed_for_admin]", "carteira"},
		"carteira para quem está virando admin": {argsEditarUsuario(cliente.ID, map[string]any{"papel": "admin", "adicionar_clientes": strsDosIDs(c.cC)}), "[client_id_not_allowed_for_admin]", "carteira"},
		"nome igual ao atual":                   {argsEditarUsuario(cliente.ID, map[string]any{"nome": " " + cliente.Name + " "}), "[sem_mudanca]", "nada muda"},
		"papel igual ao atual":                  {argsEditarUsuario(cliente.ID, map[string]any{"papel": "cliente"}), "[sem_mudanca]", "nada muda"},
		"adicionar quem já está na carteira":    {argsEditarUsuario(cliente.ID, map[string]any{"adicionar_clientes": strsDosIDs(c.cA)}), "[sem_mudanca]", "nada muda"},
		"remover quem não está na carteira":     {argsEditarUsuario(cliente.ID, map[string]any{"remover_clientes": strsDosIDs(c.cC)}), "[sem_mudanca]", "nada muda"},
		"preferência igual à atual":             {argsEditarUsuario(admin2.ID, map[string]any{"recebe_alertas": true}), "[sem_mudanca]", "nada muda"},
	} {
		texto := b.chamarErro(t, toolEditarUsuario, caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		require.Contains(t, texto, caso.texto, nome)
	}
	require.Equal(t, linhaCliente, linhaDoUsuario(t, b, cliente.ID))
	require.Equal(t, linhaAdmin2, linhaDoUsuario(t, b, admin2.ID))
	require.Zero(t, b.totalAuditorias(t), "recusa não deixa pedido nem auditoria")
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_acoes_pendentes`).Scan(&n))
	require.Zero(t, n)

	// O que passa: mudar o próprio nome; remover um cliente de uma carteira que fica com outro.
	b.pedePendente(t, toolEditarUsuario, argsEditarUsuario(b.admin, map[string]any{"nome": "Meu Nome Novo"}))
	b.pedePendente(t, toolEditarUsuario, argsEditarUsuario(cliente.ID, map[string]any{"remover_clientes": strsDosIDs(c.cA)}))
}

func TestEditarUsuario_UltimoAdminAtivoNaoSaiDoPapel(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearClientesDeUsuario(t, b)
	alvo := novoUsuarioDe(t, b, "admin", true)
	// O dono do pedido é outro admin; ele fica inativo e o alvo vira o único ativo.
	_, err := b.pool.Exec(b.ctx, `UPDATE users SET is_active = false WHERE id = $1`, b.admin)
	require.NoError(t, err)

	texto := b.chamarErro(t, toolEditarUsuario, argsEditarUsuario(alvo.ID, map[string]any{"papel": "cliente", "adicionar_clientes": strsDosIDs(c.cA)}))

	require.Contains(t, texto, "[ultimo_admin]")
	require.Contains(t, texto, "único administrador ativo")
	require.Contains(t, linhaDoUsuario(t, b, alvo.ID), "admin|")

	// Admin INATIVO não conta como ativo: rebaixá-lo não deixa o sistema sem admin.
	inativo := novoUsuarioDe(t, b, "admin", false)
	b.pedePendente(t, toolEditarUsuario, argsEditarUsuario(inativo.ID, map[string]any{"papel": "cliente", "adicionar_clientes": strsDosIDs(c.cA)}))
	// Mudar o que não é o papel do último admin é livre.
	b.pedePendente(t, toolEditarUsuario, argsEditarUsuario(alvo.ID, map[string]any{"nome": "Continua Admin"}))
}

func TestEditarUsuario_RecusaNaAprovacao(t *testing.T) {
	pedir := func(t *testing.T, papel string, extra func(c cenarioUsuarios) map[string]any) (*bancadaTool, cenarioUsuarios, *users.User, string) {
		b := novaBancadaTool(t)
		c := semearClientesDeUsuario(t, b)
		var alvo *users.User
		if papel == "admin" {
			alvo = novoUsuarioDe(t, b, "admin", true)
		} else {
			alvo = novoUsuarioDe(t, b, "viewer", true, c.cA, c.cB)
		}
		out := b.pedePendente(t, toolEditarUsuario, argsEditarUsuario(alvo.ID, extra(c)))
		return b, c, alvo, out["pendente_id"].(string)
	}
	rebaixar := func(c cenarioUsuarios) map[string]any {
		return map[string]any{"papel": "cliente", "adicionar_clientes": strsDosIDs(c.cA)}
	}
	t.Run("os outros admins foram desativados no meio: o alvo é o último", func(t *testing.T) {
		b, _, alvo, pid := pedir(t, "admin", rebaixar)
		_, err := b.pool.Exec(b.ctx, `UPDATE users SET is_active = false WHERE role = 'admin' AND id <> $1`, alvo.ID)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolEditarUsuario, pid, "ultimo_admin")
		require.Contains(t, linhaDoUsuario(t, b, alvo.ID), "admin|", "continua admin")
	})
	t.Run("o usuário foi apagado no meio", func(t *testing.T) {
		b, _, alvo, pid := pedir(t, "viewer", func(c cenarioUsuarios) map[string]any { return map[string]any{"nome": "Novo"} })
		require.NoError(t, users.NewRepo(b.pool).SoftDelete(b.ctx, alvo.ID))

		exigeRecusaNaAprovacao(t, b, toolEditarUsuario, pid, "usuario_apagado")
	})
	t.Run("o usuário não existe mais", func(t *testing.T) {
		b, _, alvo, pid := pedir(t, "viewer", func(c cenarioUsuarios) map[string]any { return map[string]any{"nome": "Novo"} })
		_, err := b.pool.Exec(b.ctx, `DELETE FROM users WHERE id = $1`, alvo.ID)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolEditarUsuario, pid, "nao_encontrado")
	})
	t.Run("um cliente a adicionar foi apagado no meio", func(t *testing.T) {
		b, c, _, pid := pedir(t, "viewer", func(c cenarioUsuarios) map[string]any {
			return map[string]any{"adicionar_clientes": strsDosIDs(c.cC)}
		})
		_, err := b.pool.Exec(b.ctx, `DELETE FROM clients WHERE id = $1`, c.cC)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolEditarUsuario, pid, "client_not_found")
	})
	t.Run("a carteira ficaria vazia depois que outra pessoa tirou o outro cliente", func(t *testing.T) {
		b, c, alvo, pid := pedir(t, "viewer", func(c cenarioUsuarios) map[string]any {
			return map[string]any{"remover_clientes": strsDosIDs(c.cB)}
		})
		require.NoError(t, users.NewRepo(b.pool).SetClients(b.ctx, alvo.ID, []uuid.UUID{c.cB}))

		exigeRecusaNaAprovacao(t, b, toolEditarUsuario, pid, "client_id_required")
		require.Contains(t, linhaDoUsuario(t, b, alvo.ID), fmt.Sprintf("carteira=[%s]", c.cB))
	})
	t.Run("alguém já fez a mesma mudança no meio", func(t *testing.T) {
		b, _, alvo, pid := pedir(t, "viewer", func(c cenarioUsuarios) map[string]any { return map[string]any{"nome": "Nome Pedido"} })
		_, err := b.pool.Exec(b.ctx, `UPDATE users SET name = 'Nome Pedido' WHERE id = $1`, alvo.ID)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolEditarUsuario, pid, "sem_mudanca")
	})
	t.Run("o alvo virou o próprio dono: a guarda de si mesmo vale na aprovação", func(t *testing.T) {
		b := novaBancadaTool(t)
		c := semearClientesDeUsuario(t, b)
		// O pedido é gravado sem passar pelo Preview do pedido (que já recusaria):
		// só a aprovação, rodando o Preview com o dono no contexto, pode barrar.
		pid := pendenteDireta(t, b, toolEditarUsuario, argsEditarUsuario(b.admin, map[string]any{"papel": "cliente", "adicionar_clientes": strsDosIDs(c.cA)}))

		exigeRecusaNaAprovacao(t, b, toolEditarUsuario, pid, "proprio_usuario")
		require.Contains(t, linhaDoUsuario(t, b, b.admin), "admin|")
		require.Equal(t, 1, tokensVivosDe(t, b, b.p.UserID), "a credencial de quem pediu continua viva")
	})
}

// Nada do que a edição devolve ou grava leva segredo.
func TestEditarUsuario_NaoCarregaSegredo(t *testing.T) {
	b := novaBancadaTool(t)
	svc, _ := ligarBoasVindas(t, b, true)
	c := semearClientesDeUsuario(t, b)
	// A pessoa tem um convite com senha inicial: o resultado da edição mostra só o estado dele.
	novo, _, err := b.E.Acoes.CriarUsuario(b.ctx, acoes.EntradaUsuario{Email: "com.convite@edicao.teste", Senha: "senha-inicial-123", Nome: "Com Convite",
		Papel: "client", ClienteIDs: []uuid.UUID{c.cA}, EnviarBoasVindas: true, Ator: &b.admin})
	require.NoError(t, err)
	token, _ := conviteDe(t, b, svc, novo.ID)

	out := b.pedePendente(t, toolEditarUsuario, argsEditarUsuario(novo.ID, map[string]any{"nome": "Com Convite Editado"}))
	ap := b.aprovar(t, out["pendente_id"].(string))

	for nome, texto := range map[string]string{"resultado": string(ap.ResultadoJSON), "pedido": mustJSON(t, out), "audit": todoOAudit(t, b), "pendentes": todasAsPendentes(t, b)} {
		require.NotContains(t, texto, "senha-inicial-123", nome)
		require.NotContains(t, texto, token, nome)
		require.NotContains(t, texto, "/boasvindas/", nome)
	}
}

func TestEditarUsuario_DescricaoAvisaDoRebaixamentoEDaSessao(t *testing.T) {
	for _, tl := range listarToolsDoServidor(t) {
		if tl.Name != toolEditarUsuario {
			continue
		}
		for _, trecho := range []string{
			"adicionar_clientes", "remover_clientes", "REVOGA", "conexões de IA", "proprio_usuario", "ultimo_admin", "sem_mudanca",
			"8 h", "próximo login", "obter_usuario", "ao menos uma mudança",
		} {
			require.Contains(t, tl.Description, trecho)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════════
// desativar_usuario
// ═══════════════════════════════════════════════════════════════════════

func argsDesativarUsuario(id uuid.UUID) map[string]any {
	return map[string]any{"usuario_id": id.String()}
}

func TestDesativarUsuario_PedidoNaoEscreveEAprovarDesativa(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearClientesDeUsuario(t, b)
	alvo := novoUsuarioDe(t, b, "viewer", true, c.cA)
	tokenMCPDe(t, b, alvo.ID)
	antes := linhaDoUsuario(t, b, alvo.ID)
	require.NotContains(t, propriedades(esquemaDa(t, b.cfg, toolDesativarUsuario)), "confirmacao", "crítico nunca recebe token")

	out := b.pedePendente(t, toolDesativarUsuario, argsDesativarUsuario(alvo.ID))

	require.Contains(t, out["motivo"], "corta o acesso")
	require.Equal(t, portalDosUsuarios, out["links"].(map[string]any)["tela"])
	require.Equal(t, antes, linhaDoUsuario(t, b, alvo.ID), "o pedido não escreve")
	require.Equal(t, 1, tokensVivosDe(t, b, alvo.ID))
	pv := out["preview"].(map[string]any)
	require.Contains(t, pv["resumo"], alvo.Name)
	ef := mustJSON(t, pv["efeitos"])
	require.Contains(t, ef, "login cortado em até 60 s")
	require.Contains(t, ef, "conexões de IA")
	require.Contains(t, ef, "revogadas")
	pid := out["pendente_id"].(string)
	exigeSemEscritaNoPedido(t, b, toolDesativarUsuario, pid)

	ap := b.aprovar(t, pid)

	res := ap.resultadoJSON(t)
	exigeIgualAoObterUsuario(t, b, alvo.ID, res)
	require.Equal(t, "inativo", res["usuario"].(map[string]any)["situacao"])
	require.Contains(t, linhaDoUsuario(t, b, alvo.ID), "ativo=false")
	require.Zero(t, tokensVivosDe(t, b, alvo.ID), "o trigger da 0070 revogou as credenciais")
	exigeAuditoriaDeUsuario(t, b, toolDesativarUsuario, alvo.ID, pid)
	require.Equal(t, "executada", b.estadoDaPendente(t, pid))
}

func TestDesativarUsuario_ParidadeComARota(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearClientesDeUsuario(t, b)
	for _, papel := range []string{"admin", "viewer"} {
		var cart []uuid.UUID
		if papel == "viewer" {
			cart = []uuid.UUID{c.cA, c.cB}
		}
		viaRota, viaTool := novoUsuarioDe(t, b, papel, true, cart...), novoUsuarioDe(t, b, papel, true, cart...)
		outro := novoUsuarioDe(t, b, papel, true, cart...)
		for _, u := range []*users.User{viaRota, viaTool, outro} {
			_, err := b.pool.Exec(b.ctx, `UPDATE users SET name = 'Pessoa Gêmea' WHERE id = $1`, u.ID)
			require.NoError(t, err)
			tokenMCPDe(t, b, u.ID)
		}
		h := handlers.NewUsersHandler(users.NewRepo(b.pool), nil)

		chamarHandlerCorpo(t, h.Patch, http.MethodPatch, "/admin/users/"+viaRota.ID.String(), map[string]any{"is_active": false}, map[string]string{"id": viaRota.ID.String()}, b.admin)
		out := b.pedePendente(t, toolDesativarUsuario, argsDesativarUsuario(viaTool.ID))
		b.aprovar(t, out["pendente_id"].(string))

		require.Equal(t, linhaDoUsuario(t, b, viaRota.ID), linhaDoUsuario(t, b, viaTool.ID), papel)
		require.Zero(t, tokensVivosDe(t, b, viaRota.ID), papel)
		require.Zero(t, tokensVivosDe(t, b, viaTool.ID), papel)
		require.Equal(t, 1, tokensVivosDe(t, b, outro.ID), "quem não foi desativado mantém as conexões de IA")
	}
}

func TestDesativarUsuario_RecusasNoPedido(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearClientesDeUsuario(t, b)
	inativo := novoUsuarioDe(t, b, "viewer", false, c.cA)
	apagado := novoUsuarioDe(t, b, "viewer", true, c.cA)
	require.NoError(t, users.NewRepo(b.pool).SoftDelete(b.ctx, apagado.ID))

	for nome, caso := range map[string]struct {
		args   map[string]any
		codigo string
		texto  string
	}{
		"usuario_id em texto": {map[string]any{"usuario_id": "xyz"}, "[entrada_invalida]", "usuario_id"},
		"usuário inexistente": {argsDesativarUsuario(uuid.New()), "[nao_encontrado]", "usuário"},
		"usuário apagado":     {argsDesativarUsuario(apagado.ID), "[usuario_apagado]", "apagado"},
		"já inativo":          {argsDesativarUsuario(inativo.ID), "[sem_mudanca]", "já está inativo"},
		"a si mesmo":          {argsDesativarUsuario(b.admin), "[proprio_usuario]", "outro administrador"},
	} {
		texto := b.chamarErro(t, toolDesativarUsuario, caso.args)
		require.Contains(t, texto, caso.codigo, nome)
		require.Contains(t, texto, caso.texto, nome)
	}
	require.Zero(t, b.totalAuditorias(t), "recusa não deixa pedido nem auditoria")
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM mcp_acoes_pendentes`).Scan(&n))
	require.Zero(t, n)
}

func TestDesativarUsuario_UltimoAdminAtivo(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearClientesDeUsuario(t, b)
	alvo := novoUsuarioDe(t, b, "admin", true)
	cliente := novoUsuarioDe(t, b, "viewer", true, c.cA)
	_, err := b.pool.Exec(b.ctx, `UPDATE users SET is_active = false WHERE id = $1`, b.admin)
	require.NoError(t, err)

	texto := b.chamarErro(t, toolDesativarUsuario, argsDesativarUsuario(alvo.ID))

	require.Contains(t, texto, "[ultimo_admin]")
	require.Contains(t, texto, "único administrador ativo")
	// A regra é sobre administradores: desativar um cliente é livre.
	b.pedePendente(t, toolDesativarUsuario, argsDesativarUsuario(cliente.ID))
}

func TestDesativarUsuario_RecusaNaAprovacao(t *testing.T) {
	pedir := func(t *testing.T, papel string) (*bancadaTool, *users.User, string) {
		b := novaBancadaTool(t)
		c := semearClientesDeUsuario(t, b)
		var alvo *users.User
		if papel == "admin" {
			alvo = novoUsuarioDe(t, b, "admin", true)
		} else {
			alvo = novoUsuarioDe(t, b, "viewer", true, c.cA)
		}
		out := b.pedePendente(t, toolDesativarUsuario, argsDesativarUsuario(alvo.ID))
		return b, alvo, out["pendente_id"].(string)
	}
	t.Run("os outros admins foram desativados no meio: o alvo é o último", func(t *testing.T) {
		b, alvo, pid := pedir(t, "admin")
		_, err := b.pool.Exec(b.ctx, `UPDATE users SET is_active = false WHERE role = 'admin' AND id <> $1`, alvo.ID)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolDesativarUsuario, pid, "ultimo_admin")
		require.Contains(t, linhaDoUsuario(t, b, alvo.ID), "ativo=true")
	})
	t.Run("alguém já desativou no meio", func(t *testing.T) {
		b, alvo, pid := pedir(t, "viewer")
		_, err := b.pool.Exec(b.ctx, `UPDATE users SET is_active = false WHERE id = $1`, alvo.ID)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolDesativarUsuario, pid, "sem_mudanca")
	})
	t.Run("o usuário foi apagado no meio", func(t *testing.T) {
		b, alvo, pid := pedir(t, "viewer")
		require.NoError(t, users.NewRepo(b.pool).SoftDelete(b.ctx, alvo.ID))

		exigeRecusaNaAprovacao(t, b, toolDesativarUsuario, pid, "usuario_apagado")
	})
	t.Run("o usuário não existe mais", func(t *testing.T) {
		b, alvo, pid := pedir(t, "viewer")
		_, err := b.pool.Exec(b.ctx, `DELETE FROM users WHERE id = $1`, alvo.ID)
		require.NoError(t, err)

		exigeRecusaNaAprovacao(t, b, toolDesativarUsuario, pid, "nao_encontrado")
	})
	t.Run("o alvo é o próprio dono: a guarda de si mesmo vale na aprovação", func(t *testing.T) {
		b := novaBancadaTool(t)
		novoUsuarioDe(t, b, "admin", true) // há outro admin: só a regra de si mesmo pode barrar
		pid := pendenteDireta(t, b, toolDesativarUsuario, argsDesativarUsuario(b.admin))

		exigeRecusaNaAprovacao(t, b, toolDesativarUsuario, pid, "proprio_usuario")
		require.Contains(t, linhaDoUsuario(t, b, b.admin), "ativo=true")
		require.Equal(t, 1, tokensVivosDe(t, b, b.p.UserID), "a credencial de quem pediu continua viva")
	})
}

func TestDesativarUsuario_DescricaoDizOQueCortaEOQueNaoVolta(t *testing.T) {
	for _, tl := range listarToolsDoServidor(t) {
		if tl.Name != toolDesativarUsuario {
			continue
		}
		for _, trecho := range []string{"60 s", "conexões de IA", "NÃO voltam", "/admin/users", "proprio_usuario", "ultimo_admin", "sem_mudanca", "obter_usuario"} {
			require.Contains(t, tl.Description, trecho)
		}
	}
}

// ── regras que valem para as três ───────────────────────────────────────

func TestCriticasDeUsuario_DescricaoPedeAprovacaoENuncaInventarIds(t *testing.T) {
	vistas := 0
	for _, tl := range listarToolsDoServidor(t) {
		switch tl.Name {
		case toolCriarUsuario, toolEditarUsuario, toolDesativarUsuario:
		default:
			continue
		}
		vistas++
		require.Contains(t, tl.Description, "/pendentes", tl.Name)
		require.Contains(t, tl.Description, "aprovar_em", tl.Name)
		require.Contains(t, tl.Description, "nada aconteceu ainda", tl.Name)
		require.Contains(t, tl.Description, "Nunca invente ids", tl.Name)
		require.True(t, strings.HasSuffix(tl.Description, " Nível: critico."), tl.Name)
	}
	require.Equal(t, 3, vistas)
}

// Nenhuma das três executa na chamada, repetir o pedido não o duplica, e a
// aprovação roda uma vez só.
func TestCriticasDeUsuario_AprovarDuasVezesNaoExecutaDuasVezes(t *testing.T) {
	b := novaBancadaTool(t)
	c := semearClientesDeUsuario(t, b)
	alvo := novoUsuarioDe(t, b, "viewer", true, c.cA)
	out := b.pedePendente(t, toolDesativarUsuario, argsDesativarUsuario(alvo.ID))
	repetido := b.pedePendente(t, toolDesativarUsuario, argsDesativarUsuario(alvo.ID))
	require.Equal(t, out["pendente_id"], repetido["pendente_id"])
	require.Equal(t, true, repetido["ja_existia"])

	b.aprovar(t, out["pendente_id"].(string))
	_, err := b.tentarAprovar(t, out["pendente_id"].(string))

	require.Error(t, err, "o pedido já foi executado")
	require.Len(t, b.auditoria(t, "mcp.escrita."+toolDesativarUsuario), 1)
}
