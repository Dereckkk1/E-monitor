package postsale

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// O wizard salva os blocos no passo 2 ANTES de o admin editar o Checking, e
// nessa hora nenhuma linha foi tocada.
//
// Este teste trava o contrato: "não editei" tem que derivar do banco, e NÃO pode
// virar "lista vazia" — senão o passo 3 e o documento aparecem sem nenhuma
// emissora, com todas caindo em "as outras N entregaram conforme o planejado".
func TestPreview_BlocoSalvoSemEdicaoAindaDerivaOChecking(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	svc := newTestService(t, pool, &recordingMailer{}, &fakeStore{})

	rep, err := svc.Repo().CreateDraft(ctx, CreateDraftInput{
		ClientID: seed.ClientID, Title: "Pós-venda · Teste",
	})
	require.NoError(t, err)

	// Exatamente o que o passo 2 do wizard grava: período escolhido, texto
	// vazio, Checking intocado.
	require.NoError(t, svc.Repo().ReplaceBlocks(ctx, rep.ID, []BlockRow{{
		CampaignID:     seed.CampaignID,
		From:           date(2026, 6, 1),
		To:             date(2026, 6, 30),
		CheckingEdited: false,
	}}))

	p, err := svc.Preview(ctx, rep.ID)
	require.NoError(t, err)
	require.Len(t, p.Campaigns, 1)

	blk := p.Campaigns[0]
	// O cenário tem 3 emissoras: 1 acima, 1 devendo, 1 conforme.
	require.Len(t, blk.CheckingRows, 2,
		"o Checking tem que listar a emissora com excedente e a com déficit")
	require.Equal(t, 1, blk.ConformingCount,
		"só a emissora que entregou exatamente o combinado vira contagem")

	// Sem texto digitado, o documento não sai com o Checking mudo.
	require.NotEmpty(t, blk.CheckingText)
	require.Contains(t, blk.CheckingText, "%")
}

// Lista vazia gravada DE PROPÓSITO (o admin removeu todas as linhas) continua
// valendo: aí sim todas as emissoras viram contagem.
func TestPreview_AdminRemovendoTodasAsLinhasEhRespeitado(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	svc := newTestService(t, pool, &recordingMailer{}, &fakeStore{})

	rep, err := svc.Repo().CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "T"})
	require.NoError(t, err)
	require.NoError(t, svc.Repo().ReplaceBlocks(ctx, rep.ID, []BlockRow{{
		CampaignID:     seed.CampaignID,
		From:           date(2026, 6, 1),
		To:             date(2026, 6, 30),
		CheckingRows:   []StationRow{},
		CheckingEdited: true, // decisão explícita do admin
	}}))

	p, err := svc.Preview(ctx, rep.ID)
	require.NoError(t, err)
	blk := p.Campaigns[0]
	require.Empty(t, blk.CheckingRows)
	require.Equal(t, 3, blk.ConformingCount, "as 3 emissoras do período viram contagem")
}

// Os KPIs vinham ZERADOS porque StationIDs ia nil pro Insights: o SQL usa
// `$N::uuid[] = '{}'` como flag de "sem filtro de emissora", e nil chega como
// NULL no pgx — NULL = '{}' não é verdadeiro, então a query filtrava por
// conjunto vazio. O cenário tem 20 tocadas com PMM 1000, então impactos > 0 é o
// que separa "consultou certo" de "consultou com filtro vazio".
func TestPreview_KPIsNaoVemZeradosPorFiltroDeEmissora(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	svc := newTestService(t, pool, &recordingMailer{}, &fakeStore{})

	rep, err := svc.Repo().CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "T"})
	require.NoError(t, err)
	require.NoError(t, svc.Repo().ReplaceBlocks(ctx, rep.ID, []BlockRow{{
		CampaignID: seed.CampaignID,
		From:       date(2026, 6, 1),
		To:         date(2026, 6, 30),
	}}))

	p, err := svc.Preview(ctx, rep.ID)
	require.NoError(t, err)
	k := p.Campaigns[0].KPIs
	require.Greater(t, k.Impactos, int64(0), "impactos zerados = filtro de emissora quebrado")
	require.Greater(t, k.StationsCount, 0, "nenhuma emissora contada")
}

// Override do admin: o número digitado vence o do sistema, e o CPM é RECALCULADO
// a partir dele — nunca digitado. Guardar um CPM à mão criaria um número que
// contradiz o valor e os impactos exibidos ao lado.
func TestPreview_OverrideDoAdminRecalculaOCPM(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	svc := newTestService(t, pool, &recordingMailer{}, &fakeStore{})

	rep, err := svc.Repo().CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "T"})
	require.NoError(t, err)

	valor := 5000.0
	impactos := int64(250000)
	require.NoError(t, svc.Repo().ReplaceBlocks(ctx, rep.ID, []BlockRow{{
		CampaignID: seed.CampaignID,
		From:       date(2026, 6, 1),
		To:         date(2026, 6, 30),
		KPIOverrides: KPIOverrides{
			ValorEntregue: &valor,
			Impactos:      &impactos,
		},
	}}))

	p, err := svc.Preview(ctx, rep.ID)
	require.NoError(t, err)
	k := p.Campaigns[0].KPIs

	require.InDelta(t, 5000.0, k.ValorEntregue, 0.001)
	require.Equal(t, int64(250000), k.Impactos)
	// 5000 / 250000 * 1000 = 20,00
	require.InDelta(t, 20.0, k.CPM, 0.001, "CPM tem que seguir o valor e os impactos digitados")
	require.True(t, k.Overridden, "o payload marca que houve ajuste manual")
}

// O numerador do CPM soma a BONIFICAÇÃO ao valor entregue. O CPM mede a
// eficiência da mídia entregue a preço de tabela, não a da negociação: a tocada
// de bônus já está nos impactos do denominador, então tem que estar no numerador
// ao preço de tabela dela. Sem isso, um pós-venda com muito bônus sairia com um
// CPM artificialmente baixo, incomparável com o de qualquer outra campanha.
func TestPreview_CPMSomaABonificacaoNoNumerador(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	svc := newTestService(t, pool, &recordingMailer{}, &fakeStore{})

	rep, err := svc.Repo().CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "T"})
	require.NoError(t, err)

	valor := 5000.0
	bonif := 1000.0
	impactos := int64(250000)
	require.NoError(t, svc.Repo().ReplaceBlocks(ctx, rep.ID, []BlockRow{{
		CampaignID: seed.CampaignID,
		From:       date(2026, 6, 1),
		To:         date(2026, 6, 30),
		KPIOverrides: KPIOverrides{
			ValorEntregue: &valor,
			Bonificacao:   &bonif,
			Impactos:      &impactos,
		},
	}}))

	p, err := svc.Preview(ctx, rep.ID)
	require.NoError(t, err)
	k := p.Campaigns[0].KPIs

	// (5000 + 1000) / 250000 × 1000 = 24,00 — e NÃO 20,00 (só o pago).
	require.InDelta(t, 24.0, k.CPM, 0.001,
		"CPM = (valor entregue + bonificação) ÷ impactos × 1000; 20,00 significa que a bonificação saiu do numerador")
	// O valor entregue exibido ao lado continua sendo SÓ o que o cliente pagou.
	require.InDelta(t, 5000.0, k.ValorEntregue, 0.001)
	require.InDelta(t, 1000.0, k.Bonificacao, 0.001)
}

// Impactos zero com valor sobrescrito não pode gerar CPM infinito.
func TestPreview_OverrideComImpactosZeroNaoExplodeOCPM(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	svc := newTestService(t, pool, &recordingMailer{}, &fakeStore{})

	rep, err := svc.Repo().CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "T"})
	require.NoError(t, err)

	valor := 800.0
	zero := int64(0)
	require.NoError(t, svc.Repo().ReplaceBlocks(ctx, rep.ID, []BlockRow{{
		CampaignID:   seed.CampaignID,
		From:         date(2026, 6, 1),
		To:           date(2026, 6, 30),
		KPIOverrides: KPIOverrides{ValorEntregue: &valor, Impactos: &zero},
	}}))

	p, err := svc.Preview(ctx, rep.ID)
	require.NoError(t, err)
	require.Equal(t, 0.0, p.Campaigns[0].KPIs.CPM)
}
