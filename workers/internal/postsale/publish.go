package postsale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// PublishResult é o retorno pro frontend depois do disparo.
type PublishResult struct {
	Recipients int `json:"recipients"`
	Sent       int `json:"sent"`
	Failed     int `json:"failed"`
	Disabled   int `json:"disabled"`
}

// Publish congela o relatório e dispara os emails.
//
// A ordem é deliberada: os artefatos são montados e subidos ANTES de qualquer
// email. Se o S3 falhar, o relatório continua draft e ninguém recebe link
// quebrado. Depois que o payload congela, falha de SMTP é por destinatário e
// nunca desfaz o publish — o link já vale, e o admin reenvia pela tela.
func (s *Service) Publish(ctx context.Context, reportID uuid.UUID) (*PublishResult, error) {
	rep, err := s.repo.Get(ctx, reportID)
	if err != nil {
		return nil, err
	}
	if rep.Status == "sent" {
		return nil, ErrAlreadySent
	}
	if len(rep.Blocks) == 0 {
		return nil, fmt.Errorf("postsale: relatório sem campanhas")
	}

	// 1. Artefatos por campanha. Qualquer falha aqui aborta ANTES dos emails.
	for i, b := range rep.Blocks {
		up, ok := s.takePending(reportID, b.CampaignID)
		if !ok {
			return nil, fmt.Errorf("postsale: faltam as capturas da campanha %q", b.CampaignName)
		}
		consolidated, detailed, err := s.buildCSVs(ctx, b)
		if err != nil {
			return nil, err
		}
		blob, err := buildZip(zipInput{
			MapPNG:          up.MapPNG,
			InsightsPNG:     up.InsightsPNG,
			ConsolidatedCSV: consolidated,
			DetailedCSV:     detailed,
			CampaignSlug:    slug(b.CampaignName),
		})
		if err != nil {
			return nil, err
		}
		// O mapa e os indicadores também vão como objetos próprios, e não só
		// dentro do zip: o documento mostra o mapa na tela, e ninguém abre um
		// zip pra ver a imagem que devia estar na página.
		assets := Assets{
			BundleZIP:   assetKey(reportID, b.CampaignID, "relatorios.zip"),
			MapPNG:      assetKey(reportID, b.CampaignID, "mapa.png"),
			InsightsPNG: assetKey(reportID, b.CampaignID, "indicadores.png"),
		}
		uploads := []struct {
			key, contentType string
			body             []byte
		}{
			{assets.MapPNG, "image/png", up.MapPNG},
			{assets.InsightsPNG, "image/png", up.InsightsPNG},
			{assets.BundleZIP, "application/zip", blob},
		}
		for _, u := range uploads {
			if err := s.storage.Put(ctx, u.key, bytes.NewReader(u.body), u.contentType); err != nil {
				return nil, fmt.Errorf("postsale: subir %s: %w", u.key, err)
			}
		}
		rep.Blocks[i].Assets = assets
		if err := s.repo.SetAssets(ctx, b.ID, assets); err != nil {
			return nil, err
		}
	}

	// 2. Congela o payload — com o MESMO builder que o preview usa.
	in := SnapshotInput{
		ClientID:       rep.ClientID,
		Title:          rep.Title,
		IntroMessage:   rep.IntroMessage,
		AttachmentsURL: rep.AttachmentsURL,
	}
	for _, b := range rep.Blocks {
		in.Blocks = append(in.Blocks, BlockInput{
			CampaignID:     b.CampaignID,
			From:           b.From,
			To:             b.To,
			CheckingText:   b.CheckingText,
			CheckingRows:   b.CheckingRows,
			CheckingEdited: b.CheckingEdited,
			KPIOverrides:   b.KPIOverrides,
			HasBundle:      b.Assets.BundleZIP != "",
		})
	}
	payload, err := s.Build(ctx, in)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if err := s.repo.MarkSent(ctx, reportID, raw); err != nil {
		return nil, err
	}

	// 3. Destinatários: usuários ativos do cliente + os admins que optaram por
	// receber cópia de todo pós-venda. Os dois grupos são destinatários iguais,
	// cada um com token próprio — é o que permite revogar um link sem derrubar
	// os outros. Consequência aceita: o admin que abrir o documento entra na
	// contagem de aberturas do relatório.
	people, err := s.repo.ActiveClientUsers(ctx, rep.ClientID)
	if err != nil {
		return nil, err
	}
	internal, err := s.repo.InternalRecipients(ctx)
	if err != nil {
		return nil, err
	}
	people = append(people, internal...)
	for i := range people {
		tok, err := NewToken()
		if err != nil {
			return nil, err
		}
		people[i].Token = tok
	}
	recs, err := s.repo.CreateRecipients(ctx, reportID, people)
	if err != nil {
		return nil, err
	}

	// 4. Um email por destinatário (token individual).
	res := &PublishResult{Recipients: len(recs)}
	for _, rc := range recs {
		status, errMsg := s.sendOne(ctx, payload, rc)
		switch status {
		case "sent":
			res.Sent++
		case "failed":
			res.Failed++
		case "disabled":
			res.Disabled++
		}
		if err := s.repo.MarkEmail(ctx, rc.ID, status, errMsg); err != nil {
			s.log.Warn("postsale: status de email não gravado", zap.Error(err))
		}
	}
	s.clearPending(reportID)
	s.log.Info("postsale: publicado",
		zap.String("report_id", reportID.String()),
		zap.Int("recipients", res.Recipients),
		zap.Int("sent", res.Sent), zap.Int("failed", res.Failed))
	return res, nil
}

// Resend reenvia para um destinatário já existente. O token é o MESMO: reenvio
// é "o email não chegou", não "quero um link novo". Para trocar o link, revogue
// e publique de novo.
func (s *Service) Resend(ctx context.Context, reportID, recipientID uuid.UUID) error {
	rep, err := s.repo.Get(ctx, reportID)
	if err != nil {
		return err
	}
	if rep.Status != "sent" {
		return ErrNotFound
	}
	var target *Recipient
	for i := range rep.Recipients {
		if rep.Recipients[i].ID == recipientID {
			target = &rep.Recipients[i]
			break
		}
	}
	if target == nil || target.RevokedAt != nil {
		// Revogado não reenvia: seria ressuscitar um link que o admin matou.
		return ErrNotFound
	}

	raw, err := s.repo.Payload(ctx, reportID)
	if err != nil {
		return err
	}
	var payload Payload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("postsale: payload congelado ilegível: %w", err)
	}

	status, errMsg := s.sendOne(ctx, &payload, *target)
	return s.repo.MarkEmail(ctx, recipientID, status, errMsg)
}

// sendOne renderiza e envia. Devolve o status a gravar e a mensagem de erro.
//
// Sem SMTP configurado (dev local, ou VM sem credencial) o status é 'disabled'
// e não 'sent': marcar como enviado um email que nunca saiu é exatamente a
// falha silenciosa que a regra 4.5 do CLAUDE.md manda evitar. O link continua
// válido e o admin manda por fora.
func (s *Service) sendOne(ctx context.Context, p *Payload, rc Recipient) (string, string) {
	if !s.mailEnabled || s.mail == nil {
		return "disabled", ""
	}
	camps := make([]emailCampaign, 0, len(p.Campaigns))
	for _, c := range p.Campaigns {
		camps = append(camps, emailCampaign{Name: c.Name, Period: c.PeriodLabel})
	}
	subject, html, text := renderEmail(emailData{
		Name:       firstName(rc.Name),
		ClientName: p.Client.Name,
		Link:       s.Link(rc.Token),
		BaseURL:    s.baseURL,
		Campaigns:  camps,
	})
	if err := s.mail.Send(ctx, []string{rc.Email}, subject, html, text); err != nil {
		s.log.Error("postsale: falha ao enviar email",
			zap.String("to", rc.Email), zap.Error(err))
		return "failed", err.Error()
	}
	return "sent", ""
}
