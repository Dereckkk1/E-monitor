package welcome

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/db"
	"radiocheck/internal/dbtest"
)

// ── Harness ──────────────────────────────────────────────────────────────

func newTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	dbtest.GuardOrSkip(t, ctx, pool)
	_, err = pool.Exec(ctx, `TRUNCATE user_welcome_invites, users, clients RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
	return ctx, pool
}

// fakeMailer captura o envio em vez de falar SMTP.
type fakeMailer struct {
	sent    int
	to      []string
	subject string
	html    string
	text    string
	err     error
}

func (m *fakeMailer) Send(_ context.Context, to []string, subject, html, text string) error {
	if m.err != nil {
		return m.err
	}
	m.sent++
	m.to, m.subject, m.html, m.text = to, subject, html, text
	return nil
}

func newTestService(t *testing.T, pool *pgxpool.Pool, mail *fakeMailer, mailEnabled bool) *Service {
	t.Helper()
	c, err := NewCipher(testKeyHex(t))
	require.NoError(t, err)
	return New(Config{
		Repo:        NewRepo(pool),
		Cipher:      c,
		Mailer:      mail,
		MailEnabled: mailEnabled,
		BaseURL:     "https://e-monitor.online/",
		Log:         zap.NewNop(),
	})
}

// seedUser cria um usuário válido. Para role 'viewer' a constraint
// users_client_role_consistency exige client_id, então criamos a empresa junto.
func seedUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, role string, clientID *uuid.UUID) uuid.UUID {
	t.Helper()
	if role == "viewer" && clientID == nil {
		var cid uuid.UUID
		err := pool.QueryRow(ctx,
			`INSERT INTO clients (name) VALUES ($1) RETURNING id`,
			"Sofá & Cia "+uuid.NewString()[:8]).Scan(&cid)
		require.NoError(t, err)
		clientID = &cid
	}
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, client_id, name)
		 VALUES ($1, 'x', $2, $3, 'Ana Souza') RETURNING id`,
		"ana"+uuid.NewString()[:8]+"@cliente.com", role, clientID).Scan(&id)
	require.NoError(t, err)
	return id
}

// ── Fluxo feliz ──────────────────────────────────────────────────────────

func TestService_IssueAndResolve(t *testing.T) {
	ctx, pool := newTestPool(t)
	mail := &fakeMailer{}
	svc := newTestService(t, pool, mail, true)

	userID := seedUser(t, ctx, pool, "viewer", nil)
	const senha = "Xk9#mQ2vLp7@Rt4z"

	res, err := svc.Issue(ctx, SendInput{
		UserID: userID, Name: "Ana Souza", Email: "ana@cliente.com",
		Password: senha, Role: "viewer", ClientName: "Sofá & Cia",
	})
	require.NoError(t, err)
	require.Equal(t, "sent", res.EmailStatus)
	require.Equal(t, 1, mail.sent)
	require.Contains(t, res.Link, "https://e-monitor.online/boasvindas/")
	require.Contains(t, mail.html, res.Link, "o email tem que trazer o link exato")

	// A base URL vinha com barra final: o link não pode ter barra dupla.
	require.NotContains(t, res.Link, "//boasvindas")

	token := res.Link[len("https://e-monitor.online/boasvindas/"):]
	got, err := svc.Resolve(ctx, token)
	require.NoError(t, err)
	require.Equal(t, senha, got.Password, "a página mostra a senha que o admin cadastrou")
	require.Equal(t, "Ana Souza", got.Name)
	require.Equal(t, "client", got.Role, "vocabulário da API, não 'viewer' do banco")
}

func TestService_SenhaNaoVazaEmClaroNoBanco(t *testing.T) {
	ctx, pool := newTestPool(t)
	svc := newTestService(t, pool, &fakeMailer{}, true)
	userID := seedUser(t, ctx, pool, "viewer", nil)
	const senha = "SenhaSuperSecreta123"

	_, err := svc.Issue(ctx, SendInput{
		UserID: userID, Name: "Ana", Email: "ana@cliente.com",
		Password: senha, Role: "viewer",
	})
	require.NoError(t, err)

	// Este é o teste que justifica a coluna ser BYTEA cifrada: quem lê o banco
	// (ou um dump de backup) não pode encontrar a senha.
	var blob []byte
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT initial_password_enc FROM user_welcome_invites WHERE user_id = $1`,
		userID).Scan(&blob))
	require.NotContains(t, string(blob), senha)
	require.NotEmpty(t, blob)
}

// ── Snapshot: trocar a senha depois NÃO muda o convite ───────────────────

func TestService_ResolveEhSnapshotDaSenhaInicial(t *testing.T) {
	ctx, pool := newTestPool(t)
	svc := newTestService(t, pool, &fakeMailer{}, true)
	userID := seedUser(t, ctx, pool, "viewer", nil)

	res, err := svc.Issue(ctx, SendInput{
		UserID: userID, Name: "Ana", Email: "ana@cliente.com",
		Password: "senha-inicial-do-admin", Role: "viewer",
	})
	require.NoError(t, err)
	token := tokenFromLink(res.Link)

	// Cliente troca a senha em /account (o handler faz UPDATE no hash).
	_, err = pool.Exec(ctx, `UPDATE users SET password_hash = 'novo-hash' WHERE id = $1`, userID)
	require.NoError(t, err)

	got, err := svc.Resolve(ctx, token)
	require.NoError(t, err)
	require.Equal(t, "senha-inicial-do-admin", got.Password,
		"a página é um snapshot do que o admin enviou, não um espelho do registro atual")
}

// ── Revogação e recusas ──────────────────────────────────────────────────

func TestService_RevokeInvalidaOLink(t *testing.T) {
	ctx, pool := newTestPool(t)
	svc := newTestService(t, pool, &fakeMailer{}, true)
	userID := seedUser(t, ctx, pool, "viewer", nil)

	res, err := svc.Issue(ctx, SendInput{
		UserID: userID, Name: "Ana", Email: "ana@cliente.com",
		Password: "senha123456", Role: "viewer",
	})
	require.NoError(t, err)
	token := tokenFromLink(res.Link)

	_, err = svc.Resolve(ctx, token)
	require.NoError(t, err, "antes de revogar, resolve normal")

	admin := seedUser(t, ctx, pool, "admin", nil)
	require.NoError(t, svc.Repo().Revoke(ctx, res.InviteID, admin))

	_, err = svc.Resolve(ctx, token)
	require.True(t, errors.Is(err, ErrNotFound), "revogado tem que virar 404, veio %v", err)

	// Revogar é idempotente e apaga a senha cifrada de vez.
	require.NoError(t, svc.Repo().Revoke(ctx, res.InviteID, admin))
	var blob []byte
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT initial_password_enc FROM user_welcome_invites WHERE id = $1`,
		res.InviteID).Scan(&blob))
	require.Nil(t, blob, "revogar apaga a senha, não só marca a flag")
}

func TestService_RevokeSemAtorIdentificadoNaoQuebra(t *testing.T) {
	ctx, pool := newTestPool(t)
	svc := newTestService(t, pool, &fakeMailer{}, true)
	userID := seedUser(t, ctx, pool, "viewer", nil)

	res, err := svc.Issue(ctx, SendInput{
		UserID: userID, Name: "Ana", Email: "ana@cliente.com",
		Password: "senha123456", Role: "viewer",
	})
	require.NoError(t, err)

	// revoked_by tem FK pra users. Se o caller não trouxer claims, o UUID zero
	// levantaria foreign_key_violation e a revogação falharia — inaceitável
	// numa ação de segurança. Tem que virar NULL e revogar assim mesmo.
	require.NoError(t, svc.Repo().Revoke(ctx, res.InviteID, uuid.Nil))

	_, err = svc.Resolve(ctx, tokenFromLink(res.Link))
	require.True(t, errors.Is(err, ErrNotFound))
}

func TestService_UsuarioExcluidoNaoResolve(t *testing.T) {
	ctx, pool := newTestPool(t)
	svc := newTestService(t, pool, &fakeMailer{}, true)
	userID := seedUser(t, ctx, pool, "viewer", nil)

	res, err := svc.Issue(ctx, SendInput{
		UserID: userID, Name: "Ana", Email: "ana@cliente.com",
		Password: "senha123456", Role: "viewer",
	})
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `UPDATE users SET deleted_at = NOW() WHERE id = $1`, userID)
	require.NoError(t, err)

	_, err = svc.Resolve(ctx, tokenFromLink(res.Link))
	require.True(t, errors.Is(err, ErrNotFound))
}

func TestService_TokenDesconhecido(t *testing.T) {
	ctx, pool := newTestPool(t)
	svc := newTestService(t, pool, &fakeMailer{}, true)
	_, err := svc.Resolve(ctx, "token-que-nunca-existiu")
	require.True(t, errors.Is(err, ErrNotFound))
}

func TestService_ChaveTrocadaNaoVazaErroInterno(t *testing.T) {
	ctx, pool := newTestPool(t)
	svc := newTestService(t, pool, &fakeMailer{}, true)
	userID := seedUser(t, ctx, pool, "viewer", nil)

	res, err := svc.Issue(ctx, SendInput{
		UserID: userID, Name: "Ana", Email: "ana@cliente.com",
		Password: "senha123456", Role: "viewer",
	})
	require.NoError(t, err)

	// Rotação de chave sem re-emitir os convites: o blob antigo não decifra.
	// Pro visitante isso é indistinguível de link inválido — e é assim que tem
	// que ser, em vez de 500.
	other := newTestService(t, pool, &fakeMailer{}, true)
	_, err = other.Resolve(ctx, tokenFromLink(res.Link))
	require.True(t, errors.Is(err, ErrNotFound), "veio %v", err)
}

// ── Desfechos de email ───────────────────────────────────────────────────

func TestService_SMTPDesligadoAindaEmiteConvite(t *testing.T) {
	ctx, pool := newTestPool(t)
	mail := &fakeMailer{}
	svc := newTestService(t, pool, mail, false) // sem credenciais SMTP
	userID := seedUser(t, ctx, pool, "viewer", nil)

	res, err := svc.Issue(ctx, SendInput{
		UserID: userID, Name: "Ana", Email: "ana@cliente.com",
		Password: "senha123456", Role: "viewer",
	})
	require.NoError(t, err)
	require.Equal(t, "disabled", res.EmailStatus)
	require.Zero(t, mail.sent)
	// O link continua válido: o admin copia e manda por WhatsApp.
	require.NotEmpty(t, res.Link)
	got, err := svc.Resolve(ctx, tokenFromLink(res.Link))
	require.NoError(t, err)
	require.Equal(t, "senha123456", got.Password)
}

func TestService_FalhaDeSMTPNaoPerdeOConvite(t *testing.T) {
	ctx, pool := newTestPool(t)
	mail := &fakeMailer{err: errors.New("dial tcp: connection refused")}
	svc := newTestService(t, pool, mail, true)
	userID := seedUser(t, ctx, pool, "viewer", nil)

	res, err := svc.Issue(ctx, SendInput{
		UserID: userID, Name: "Ana", Email: "ana@cliente.com",
		Password: "senha123456", Role: "viewer",
	})
	// Issue NÃO devolve erro: o usuário já está criado, e falhar aqui faria o
	// admin achar que a criação inteira falhou.
	require.NoError(t, err)
	require.Equal(t, "failed", res.EmailStatus)
	require.Contains(t, res.EmailError, "connection refused")

	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT email_status FROM user_welcome_invites WHERE id = $1`, res.InviteID).Scan(&status))
	require.Equal(t, "failed", status, "o desfecho fica gravado pra UI mostrar")

	// E o link funciona mesmo assim.
	_, err = svc.Resolve(ctx, tokenFromLink(res.Link))
	require.NoError(t, err)
}

// ── Telemetria de abertura ───────────────────────────────────────────────

func TestService_ContaAberturas(t *testing.T) {
	ctx, pool := newTestPool(t)
	svc := newTestService(t, pool, &fakeMailer{}, true)
	userID := seedUser(t, ctx, pool, "viewer", nil)

	res, err := svc.Issue(ctx, SendInput{
		UserID: userID, Name: "Ana", Email: "ana@cliente.com",
		Password: "senha123456", Role: "viewer",
	})
	require.NoError(t, err)
	token := tokenFromLink(res.Link)

	for i := 0; i < 3; i++ {
		_, err := svc.Resolve(ctx, token)
		require.NoError(t, err)
	}

	var count int
	var openedAt *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT open_count, opened_at::text FROM user_welcome_invites WHERE id = $1`,
		res.InviteID).Scan(&count, &openedAt))
	require.Equal(t, 3, count)
	require.NotNil(t, openedAt, "opened_at guarda a PRIMEIRA abertura")
}

// ── Desabilitado por falta de chave ──────────────────────────────────────

func TestService_SemChaveRecusaEmitir(t *testing.T) {
	ctx, pool := newTestPool(t)
	svc := New(Config{
		Repo: NewRepo(pool), Cipher: nil, Mailer: &fakeMailer{},
		BaseURL: "https://e-monitor.online", Log: zap.NewNop(),
	})
	require.False(t, svc.Enabled())

	_, err := svc.Issue(ctx, SendInput{UserID: uuid.New(), Password: "x"})
	require.True(t, errors.Is(err, ErrDisabled))

	_, err = svc.Resolve(ctx, "qualquer")
	require.True(t, errors.Is(err, ErrDisabled))
}

// ── Latest (coluna do /admin/users) ──────────────────────────────────────

func TestRepo_LatestPegaOConviteMaisRecente(t *testing.T) {
	ctx, pool := newTestPool(t)
	svc := newTestService(t, pool, &fakeMailer{}, true)
	repo := svc.Repo()
	userID := seedUser(t, ctx, pool, "viewer", nil)

	first, err := svc.Issue(ctx, SendInput{
		UserID: userID, Name: "Ana", Email: "a@b.com", Password: "senha123456", Role: "viewer"})
	require.NoError(t, err)
	second, err := svc.Issue(ctx, SendInput{
		UserID: userID, Name: "Ana", Email: "a@b.com", Password: "senha-nova-99", Role: "viewer"})
	require.NoError(t, err)

	got, err := repo.Latest(ctx, []uuid.UUID{userID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, second.InviteID, got[userID].ID, "reenvio: vence o mais recente")
	require.NotEqual(t, first.InviteID, got[userID].ID)

	// Lista vazia não pode explodir nem gerar query inválida.
	empty, err := repo.Latest(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, empty)
}

func tokenFromLink(link string) string {
	const marker = "/boasvindas/"
	i := len(link) - 1
	for ; i >= 0; i-- {
		if i+len(marker) <= len(link) && link[i:i+len(marker)] == marker {
			return link[i+len(marker):]
		}
	}
	return ""
}
