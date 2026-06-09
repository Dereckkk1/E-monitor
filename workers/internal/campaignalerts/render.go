package campaignalerts

import (
	"bytes"
	"embed"
	"fmt"
	htmltpl "html/template"
	texttpl "text/template"
	"time"

	"radiocheck/internal/calendar"
)

//go:embed templates/*.html templates/*.txt
var templatesFS embed.FS

// EmailContent é o resultado renderizado de um disparo.
type EmailContent struct {
	Subject string
	HTML    string
	Text    string
}

// viewData é o contrato passado aos templates. NÃO renomear campos sem atualizar
// os templates (o craft visual deve preservar este contrato).
type viewData struct {
	Recipient string
	Heading   string
	BaseURL   string
	Campaigns []CampaignAlert
}

// fmtDate formata uma data civil (DATE do banco, meia-noite UTC) como
// dd/mm/aaaa. Usa CivilDate para não deslocar o dia por timezone.
func fmtDate(t time.Time) string {
	return calendar.CivilDate(t).Format("02/01/2006")
}

var (
	htmlTemplates = htmltpl.Must(htmltpl.New("").Funcs(htmltpl.FuncMap{
		"fmtDate": fmtDate,
	}).ParseFS(templatesFS, "templates/*.html"))
	textTemplate = texttpl.Must(texttpl.New("_email.txt").Funcs(texttpl.FuncMap{
		"fmtDate": fmtDate,
	}).ParseFS(templatesFS, "templates/_email.txt"))
)

func render(htmlFile, subject, heading, recipient, baseURL string, campaigns []CampaignAlert) (EmailContent, error) {
	data := viewData{Recipient: recipient, Heading: heading, BaseURL: baseURL, Campaigns: campaigns}
	var html bytes.Buffer
	if err := htmlTemplates.ExecuteTemplate(&html, htmlFile, data); err != nil {
		return EmailContent{}, fmt.Errorf("render html %s: %w", htmlFile, err)
	}
	var text bytes.Buffer
	if err := textTemplate.Execute(&text, data); err != nil {
		return EmailContent{}, fmt.Errorf("render text: %w", err)
	}
	return EmailContent{Subject: subject, HTML: html.String(), Text: text.String()}, nil
}

func subjectCount(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// RenderStartingNoMaterial — disparo 1.
func RenderStartingNoMaterial(recipient string, campaigns []CampaignAlert, baseURL string) (EmailContent, error) {
	n := len(campaigns)
	heading := subjectCount(n, "campanha sem material", "campanhas sem material")
	return render("starting_no_material.html", "⚠️ "+heading, heading, recipient, baseURL, campaigns)
}

// RenderStarting — disparo 2.
func RenderStarting(recipient string, campaigns []CampaignAlert, baseURL string) (EmailContent, error) {
	n := len(campaigns)
	subj := subjectCount(n, "campanha iniciando", "campanhas iniciando")
	return render("starting.html", subj, subj, recipient, baseURL, campaigns)
}

// RenderEnding — disparo 3.
func RenderEnding(recipient string, campaigns []CampaignAlert, baseURL string) (EmailContent, error) {
	n := len(campaigns)
	subj := subjectCount(n, "campanha terminando", "campanhas terminando")
	return render("ending.html", subj, subj, recipient, baseURL, campaigns)
}
