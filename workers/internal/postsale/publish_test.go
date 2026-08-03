package postsale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
)

// fakeStore captura os objetos sem falar com o S3.
type fakeStore struct {
	objects map[string][]byte
	failPut bool
}

func (f *fakeStore) Put(_ context.Context, key string, body io.Reader, _ string) error {
	if f.failPut {
		return errors.New("s3 fora do ar")
	}
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if f.objects == nil {
		f.objects = map[string][]byte{}
	}
	f.objects[key] = b
	return nil
}

func (f *fakeStore) Get(_ context.Context, key string) (io.ReadCloser, string, int64, error) {
	b, ok := f.objects[key]
	if !ok {
		return nil, "", 0, errors.New("objeto inexistente")
	}
	ct := "application/octet-stream"
	if strings.HasSuffix(key, ".png") {
		ct = "image/png"
	} else if strings.HasSuffix(key, ".zip") {
		ct = "application/zip"
	}
	return io.NopCloser(bytes.NewReader(b)), ct, int64(len(b)), nil
}

// recordingMailer registra os envios e pode falhar em endereços escolhidos.
type recordingMailer struct {
	sent []string
	fail map[string]bool
}

func (m *recordingMailer) Send(_ context.Context, to []string, _, _, _ string) error {
	if m.fail[to[0]] {
		return errors.New("smtp: recusado")
	}
	m.sent = append(m.sent, to[0])
	return nil
}

func newTestService(t *testing.T, pool *pgxpool.Pool, mail *recordingMailer, store *fakeStore) *Service {
	t.Helper()
	return New(Config{
		Repo:        NewRepo(pool),
		Insights:    catalog.NewInsights(pool),
		Detections:  catalog.NewDetections(pool),
		Storage:     store,
		Mailer:      mail,
		MailEnabled: mail != nil,
		BaseURL:     "https://e-monitor.online",
		Footer:      Footer{Email: "spot@hubradios.com.br", City: "São Paulo, Brasil"},
		Log:         zap.NewNop(),
	})
}

// createDraftWithBlock monta um draft de uma campanha no período do seed.
func createDraftWithBlock(t *testing.T, ctx context.Context, svc *Service, seed scenario) *Report {
	t.Helper()
	rep, err := svc.Repo().CreateDraft(ctx, CreateDraftInput{
		ClientID: seed.ClientID, Title: "Pós-venda · Teste",
	})
	require.NoError(t, err)
	require.NoError(t, svc.Repo().ReplaceBlocks(ctx, rep.ID, []BlockRow{{
		CampaignID: seed.CampaignID, From: date(2026, 6, 1), To: date(2026, 6, 30),
	}}))
	return rep
}

func tokenOf(t *testing.T, ctx context.Context, svc *Service, reportID uuid.UUID) string {
	t.Helper()
	recs, err := svc.Repo().Recipients(ctx, reportID)
	require.NoError(t, err)
	require.NotEmpty(t, recs)
	return recs[0].Token
}

func capture() uploadedAssets {
	return uploadedAssets{MapPNG: []byte("fake-map-png"), InsightsPNG: []byte("fake-insights-png")}
}

// Falha de SMTP num destinatário não derruba o publish nem os outros envios: o
// relatório já está congelado e os links valem.
func TestPublish_FalhaDeEmailNaoDerrubaOsOutros(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	seedClientUser(t, ctx, pool, seed.ClientID, "ok@empresa.com", true)
	seedClientUser(t, ctx, pool, seed.ClientID, "quebra@empresa.com", true)
	// Desativado não recebe.
	seedClientUser(t, ctx, pool, seed.ClientID, "zzdesativado@empresa.com", false)

	mail := &recordingMailer{fail: map[string]bool{"quebra@empresa.com": true}}
	svc := newTestService(t, pool, mail, &fakeStore{})

	rep := createDraftWithBlock(t, ctx, svc, seed)
	require.NoError(t, svc.SetAssets(ctx, rep.ID, seed.CampaignID, capture()))

	res, err := svc.Publish(ctx, rep.ID)
	require.NoError(t, err)
	require.Equal(t, 2, res.Recipients, "só os ativos entram")
	require.Equal(t, 1, res.Sent)
	require.Equal(t, 1, res.Failed)

	loaded, err := svc.Repo().Get(ctx, rep.ID)
	require.NoError(t, err)
	require.Equal(t, "sent", loaded.Status)
	require.NotNil(t, loaded.SentAt)

	byEmail := map[string]string{}
	for _, r := range loaded.Recipients {
		byEmail[r.Email] = r.EmailStatus
	}
	require.Equal(t, "sent", byEmail["ok@empresa.com"])
	require.Equal(t, "failed", byEmail["quebra@empresa.com"])
	require.NotContains(t, byEmail, "zzdesativado@empresa.com")

	// Publicar de novo é 409, não um segundo disparo silencioso.
	_, err = svc.Publish(ctx, rep.ID)
	require.ErrorIs(t, err, ErrAlreadySent)
}

// Admin com o opt-in ligado recebe o pós-venda de QUALQUER cliente, com token
// próprio — não é encaminhamento do link do cliente.
func TestPublish_IncluiAdminsQueOptaram(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	seedClientUser(t, ctx, pool, seed.ClientID, "pv-cliente@empresa.com", true)
	seedInternalAdmin(t, ctx, pool, "pv-acompanha@hubradios.com", true, true)
	seedInternalAdmin(t, ctx, pool, "pv-ignora@hubradios.com", true, false)
	seedInternalAdmin(t, ctx, pool, "pv-desativado@hubradios.com", false, true)

	mail := &recordingMailer{}
	svc := newTestService(t, pool, mail, &fakeStore{})

	rep := createDraftWithBlock(t, ctx, svc, seed)
	require.NoError(t, svc.SetAssets(ctx, rep.ID, seed.CampaignID, capture()))

	_, err := svc.Publish(ctx, rep.ID)
	require.NoError(t, err)

	require.Contains(t, mail.sent, "pv-cliente@empresa.com")
	require.Contains(t, mail.sent, "pv-acompanha@hubradios.com")
	require.NotContains(t, mail.sent, "pv-ignora@hubradios.com")
	require.NotContains(t, mail.sent, "pv-desativado@hubradios.com")

	loaded, err := svc.Repo().Get(ctx, rep.ID)
	require.NoError(t, err)
	tokens := map[string]string{}
	for _, r := range loaded.Recipients {
		require.NotContains(t, tokens, r.Email, "um destinatário, um email")
		tokens[r.Email] = r.Token
	}
	require.NotEqual(t, tokens["pv-cliente@empresa.com"], tokens["pv-acompanha@hubradios.com"],
		"token é por destinatário: é o que permite revogar um link sem derrubar o outro")

	// O link do admin abre o mesmo documento congelado.
	_, err = svc.Resolve(ctx, tokens["pv-acompanha@hubradios.com"])
	require.NoError(t, err)
}

// O Publish NÃO deduplica entre os dois grupos porque o CHECK
// users_client_role_consistency (migration 0027) torna a interseção impossível:
// admin/operator tem client_id NULL. Se alguém relaxar esse CHECK, este teste
// quebra antes de alguém receber dois emails do mesmo pós-venda.
func TestPublish_AdminNaoDuplicaDestinatario(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)

	_, err := pool.Exec(ctx,
		`INSERT INTO users (email, password_hash, role, client_id, name, receive_post_sale_emails)
		 VALUES ($1, 'h', 'admin', $2, 'Impossível', TRUE)`,
		"pv-hibrido@hubradios.com", seed.ClientID)
	require.Error(t, err, "admin com client_id tem que ser recusado pelo banco")

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "23514", pgErr.Code, "check_violation")
	require.Equal(t, "users_client_role_consistency", pgErr.ConstraintName)
}

// O congelado não muda depois. Este é o teste que protege a promessa central
// do pós-venda.
func TestPublish_PayloadCongela(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	seedClientUser(t, ctx, pool, seed.ClientID, "cliente@empresa.com", true)
	svc := newTestService(t, pool, &recordingMailer{}, &fakeStore{})

	rep := createDraftWithBlock(t, ctx, svc, seed)
	// O link dos anexos congela junto com o resto: o botão que o cliente clicar
	// daqui a um ano aponta pra onde apontava no dia do envio.
	require.NoError(t, svc.Repo().UpdateContent(ctx, rep.ID, "T", "oi",
		"https://drive.google.com/drive/folders/xyz"))
	require.NoError(t, svc.SetAssets(ctx, rep.ID, seed.CampaignID, capture()))
	_, err := svc.Publish(ctx, rep.ID)
	require.NoError(t, err)

	tok := tokenOf(t, ctx, svc, rep.ID)
	before, err := svc.Repo().ResolveToken(ctx, tok)
	require.NoError(t, err)
	require.Contains(t, string(before.Payload), `"checking_rows"`)
	// Compara pelo VALOR e não por substring: o jsonb do Postgres reescreve o
	// JSON (`": "` com espaço), então casar texto cru quebra sem o dado estar
	// errado.
	var congelado Payload
	require.NoError(t, json.Unmarshal(before.Payload, &congelado))
	require.Equal(t, "https://drive.google.com/drive/folders/xyz", congelado.AttachmentsURL)

	// Apaga TODAS as veiculações da campanha: se o payload recalculasse, os
	// números mudariam.
	_, err = pool.Exec(ctx, `DELETE FROM detection_campaigns WHERE campaign_id = $1`, seed.CampaignID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM detections WHERE campaign_id = $1`, seed.CampaignID)
	require.NoError(t, err)

	after, err := svc.Repo().ResolveToken(ctx, tok)
	require.NoError(t, err)
	require.JSONEq(t, string(before.Payload), string(after.Payload))
}

// Sem as capturas do browser, o publish para ANTES de qualquer email sair.
func TestPublish_AbortaSemAssets(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	seedClientUser(t, ctx, pool, seed.ClientID, "cliente@empresa.com", true)
	mail := &recordingMailer{}
	svc := newTestService(t, pool, mail, &fakeStore{})

	rep := createDraftWithBlock(t, ctx, svc, seed)
	_, err := svc.Publish(ctx, rep.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "capturas")
	require.Empty(t, mail.sent, "nenhum email pode sair com bundle incompleto")

	loaded, err := svc.Repo().Get(ctx, rep.ID)
	require.NoError(t, err)
	require.Equal(t, "draft", loaded.Status, "o relatório continua editável")
}

// S3 fora do ar aborta antes dos emails — link quebrado é pior que atraso.
func TestPublish_AbortaSeS3Falha(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	seedClientUser(t, ctx, pool, seed.ClientID, "cliente@empresa.com", true)
	mail := &recordingMailer{}
	svc := newTestService(t, pool, mail, &fakeStore{failPut: true})

	rep := createDraftWithBlock(t, ctx, svc, seed)
	require.NoError(t, svc.SetAssets(ctx, rep.ID, seed.CampaignID, capture()))

	_, err := svc.Publish(ctx, rep.ID)
	require.Error(t, err)
	require.Empty(t, mail.sent)

	loaded, err := svc.Repo().Get(ctx, rep.ID)
	require.NoError(t, err)
	require.Equal(t, "draft", loaded.Status)
}

// Sem SMTP o link continua valendo: status 'disabled', nunca 'sent'.
func TestPublish_SemSMTPMarcaDisabled(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	seedClientUser(t, ctx, pool, seed.ClientID, "cliente@empresa.com", true)
	svc := newTestService(t, pool, nil, &fakeStore{})

	rep := createDraftWithBlock(t, ctx, svc, seed)
	require.NoError(t, svc.SetAssets(ctx, rep.ID, seed.CampaignID, capture()))

	res, err := svc.Publish(ctx, rep.ID)
	require.NoError(t, err)
	require.Equal(t, 1, res.Disabled)
	require.Equal(t, 0, res.Sent)

	// O link vale mesmo assim.
	_, err = svc.Resolve(ctx, tokenOf(t, ctx, svc, rep.ID))
	require.NoError(t, err)
}

// Os artefatos saem em BYTES pela API (nunca redirect pra presigned, que em
// prod aponta pra localhost:9000), e só depois de validar o token.
func TestOpenAsset_ExigeTokenValido(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	seedClientUser(t, ctx, pool, seed.ClientID, "cliente@empresa.com", true)
	store := &fakeStore{}
	svc := newTestService(t, pool, &recordingMailer{}, store)

	rep := createDraftWithBlock(t, ctx, svc, seed)
	require.NoError(t, svc.SetAssets(ctx, rep.ID, seed.CampaignID, capture()))
	_, err := svc.Publish(ctx, rep.ID)
	require.NoError(t, err)

	tok := tokenOf(t, ctx, svc, rep.ID)
	zip, err := svc.OpenBundle(ctx, tok, seed.CampaignID)
	require.NoError(t, err)
	defer zip.Body.Close()
	require.Equal(t, "application/zip", zip.ContentType)
	blob, err := io.ReadAll(zip.Body)
	require.NoError(t, err)
	require.NotEmpty(t, blob)
	require.Equal(t, int64(len(blob)), zip.Size)

	_, err = svc.OpenBundle(ctx, "token-invalido", seed.CampaignID)
	require.ErrorIs(t, err, ErrNotFound)

	// O mapa vive como objeto PRÓPRIO, não só dentro do zip: o documento mostra
	// a imagem na tela, e ninguém abre um zip pra ver o mapa.
	mapAsset, err := svc.OpenImage(ctx, tok, seed.CampaignID, AssetMap)
	require.NoError(t, err)
	defer mapAsset.Body.Close()
	require.Equal(t, "image/png", mapAsset.ContentType)
	mapBytes, err := io.ReadAll(mapAsset.Body)
	require.NoError(t, err)
	require.Equal(t, capture().MapPNG, mapBytes)

	insAsset, err := svc.OpenImage(ctx, tok, seed.CampaignID, AssetInsights)
	require.NoError(t, err)
	defer insAsset.Body.Close()
	insBytes, err := io.ReadAll(insAsset.Body)
	require.NoError(t, err)
	require.Equal(t, capture().InsightsPNG, insBytes)

	// Token inválido não abre imagem nenhuma.
	_, err = svc.OpenImage(ctx, "token-invalido", seed.CampaignID, AssetMap)
	require.ErrorIs(t, err, ErrNotFound)

	// Os três objetos subiram (mapa, indicadores, zip).
	require.Len(t, store.objects, 3)
}
