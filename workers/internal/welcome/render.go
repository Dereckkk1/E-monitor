package welcome

import (
	"bytes"
	"embed"
	htmltpl "html/template"
	texttpl "text/template"
)

//go:embed templates/*.html templates/*.txt
var templatesFS embed.FS

var (
	htmlTemplate = htmltpl.Must(htmltpl.ParseFS(templatesFS, "templates/welcome.html"))
	textTemplate = texttpl.Must(texttpl.ParseFS(templatesFS, "templates/welcome.txt"))
)

// welcomeData é o contrato dos templates. Não renomeie campos sem atualizar os
// dois arquivos em templates/.
type welcomeData struct {
	Name       string // primeiro nome, pra saudação
	FullName   string
	Email      string
	ClientName string
	Link       string
	BaseURL    string // base pública que serve as imagens (logos) do email
	IsClient   bool
}

// Greeting resolve a saudação sem deixar "Olá ," quando o nome vem vazio.
func (d welcomeData) Greeting() string {
	if d.Name == "" {
		return "Boas-vindas"
	}
	return "Boas-vindas, " + d.Name
}

// renderWelcome devolve assunto, corpo HTML e corpo texto do email.
func renderWelcome(d welcomeData) (subject, html, text string) {
	subject = "Seu acesso ao E-monitor está pronto"
	if d.Name != "" {
		subject = d.Name + ", seu acesso ao E-monitor está pronto"
	}
	var hb, tb bytes.Buffer
	if err := htmlTemplate.ExecuteTemplate(&hb, "welcome.html", d); err != nil {
		// Templates são embed + parseados no init: erro aqui é bug de
		// programação, não condição de runtime. Cai no texto, que sempre vai.
		hb.Reset()
	}
	if err := textTemplate.ExecuteTemplate(&tb, "welcome.txt", d); err != nil {
		tb.WriteString("Acesse " + d.Link + " para concluir seu cadastro no E-monitor.")
	}
	return subject, hb.String(), tb.String()
}
