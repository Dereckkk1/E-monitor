package postsale

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
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

func (f *fakeStore) PresignGet(_ context.Context, key string, ttl time.Duration) (string, time.Time, error) {
	if _, ok := f.objects[key]; !ok {
		return "", time.Time{}, errors.New("objeto inexistente")
	}
	return "https://s3.test/" + key + "?sig=x", time.Now().Add(ttl), nil
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

// O congelado não muda depois. Este é o teste que protege a promessa central
// do pós-venda.
func TestPublish_PayloadCongela(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	seedClientUser(t, ctx, pool, seed.ClientID, "cliente@empresa.com", true)
	svc := newTestService(t, pool, &recordingMailer{}, &fakeStore{})

	rep := createDraftWithBlock(t, ctx, svc, seed)
	require.NoError(t, svc.SetAssets(ctx, rep.ID, seed.CampaignID, capture()))
	_, err := svc.Publish(ctx, rep.ID)
	require.NoError(t, err)

	tok := tokenOf(t, ctx, svc, rep.ID)
	before, err := svc.Repo().ResolveToken(ctx, tok)
	require.NoError(t, err)
	require.Contains(t, string(before.Payload), `"checking_rows"`)

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

// O bundle sai por presigned URL, e só depois de validar o token.
func TestBundleURL_ExigeTokenValido(t *testing.T) {
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
	u, err := svc.BundleURL(ctx, tok, seed.CampaignID, 15*time.Minute)
	require.NoError(t, err)
	require.Contains(t, u, "post-sale/")
	require.Contains(t, u, "sig=")

	_, err = svc.BundleURL(ctx, "token-invalido", seed.CampaignID, time.Minute)
	require.ErrorIs(t, err, ErrNotFound)

	// O mapa vive como objeto PRÓPRIO, não só dentro do zip: o documento mostra
	// a imagem na tela, e ninguém abre um zip pra ver o mapa.
	mapURL, err := svc.ImageURL(ctx, tok, seed.CampaignID, AssetMap, time.Minute)
	require.NoError(t, err)
	require.Contains(t, mapURL, "mapa.png")

	insURL, err := svc.ImageURL(ctx, tok, seed.CampaignID, AssetInsights, time.Minute)
	require.NoError(t, err)
	require.Contains(t, insURL, "indicadores.png")

	// Token inválido não presigna imagem nenhuma.
	_, err = svc.ImageURL(ctx, "token-invalido", seed.CampaignID, AssetMap, time.Minute)
	require.ErrorIs(t, err, ErrNotFound)

	// Os três objetos subiram (mapa, indicadores, zip).
	require.Len(t, store.objects, 3)
}
