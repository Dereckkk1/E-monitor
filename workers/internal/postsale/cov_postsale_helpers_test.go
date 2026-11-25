package postsale

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
)

// covZEmptyCTStore devolve objetos SEM content-type, para exercitar o fallback.
type covZEmptyCTStore struct{ objects map[string][]byte }

func (s *covZEmptyCTStore) Put(_ context.Context, key string, body io.Reader, _ string) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if s.objects == nil {
		s.objects = map[string][]byte{}
	}
	s.objects[key] = b
	return nil
}

func (s *covZEmptyCTStore) Get(_ context.Context, key string) (io.ReadCloser, string, int64, error) {
	b, ok := s.objects[key]
	if !ok {
		return nil, "", 0, io.ErrUnexpectedEOF
	}
	return io.NopCloser(bytes.NewReader(b)), "", int64(len(b)), nil
}

// covZSecondCampaign cria uma segunda campanha do mesmo cliente. A limpeza apaga
// antes os relatorios do cliente: o bloco referencia a campanha com RESTRICT.
func covZSecondCampaign(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed scenario, name string) uuid.UUID {
	t.Helper()
	camp, err := catalog.NewCampaigns(pool).Create(ctx, catalog.CreateCampaignInput{
		Name:           name,
		ClientID:       seed.ClientID,
		StartDate:      date(2026, 6, 1),
		EndDate:        date(2026, 6, 30),
		TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM post_sale_reports WHERE client_id=$1`, seed.ClientID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM campaigns WHERE id=$1`, camp.ID)
	})
	return camp.ID
}

// covZSecondClient cria outro cliente (a limpeza apaga por cascata os relatorios dele).
func covZSecondClient(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	cli, err := catalog.NewClients(pool).Create(ctx, catalog.CreateClientInput{Name: name})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM clients WHERE id=$1`, cli.ID) })
	return cli.ID
}

// covZPublished publica um relatorio de uma campanha com 1 destinatario e devolve
// (service, relatorio, destinatario).
func covZPublished(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed scenario, mail *recordingMailer, store ObjectStore) (*Service, *Report, Recipient) {
	t.Helper()
	seedClientUser(t, ctx, pool, seed.ClientID, "cliente-covz@example.com", true)
	var svc *Service
	if fs, ok := store.(*fakeStore); ok {
		svc = newTestService(t, pool, mail, fs)
	} else {
		svc = newTestService(t, pool, mail, &fakeStore{})
		svc.storage = store
	}
	rep := createDraftWithBlock(t, ctx, svc, seed)
	require.NoError(t, svc.SetAssets(ctx, rep.ID, seed.CampaignID, capture()))
	_, err := svc.Publish(ctx, rep.ID)
	require.NoError(t, err)
	recs, err := svc.Repo().Recipients(ctx, rep.ID)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	return svc, rep, recs[0]
}
