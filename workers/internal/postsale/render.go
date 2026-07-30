package postsale

import (
	"bytes"
	"embed"
	htmltpl "html/template"
	texttpl "text/template"
)

//go:embed templates/*.html templates/*.txt
var templatesFS embed.FS

var (
	emailHTML = htmltpl.Must(htmltpl.ParseFS(templatesFS, "templates/post_sale.html"))
	emailText = texttpl.Must(texttpl.ParseFS(templatesFS, "templates/post_sale.txt"))
)

// emailCampaign é uma linha da lista de campanhas do email.
type emailCampaign struct {
	Name   string
	Period string
}

// emailData é o contrato dos dois templates. Não renomeie campos sem atualizar
// os arquivos em templates/.
type emailData struct {
	Name       string // primeiro nome, pra saudação
	ClientName string
	Link       string // URL pessoal do pós-venda
	BaseURL    string // base pública que serve as imagens (logo) do email
	Campaigns  []emailCampaign
}

// Greeting evita "Olá, !" quando o cadastro do usuário não tem nome.
func (d emailData) Greeting() string {
	if d.Name == "" {
		return "Olá"
	}
	return "Olá, " + d.Name
}

// renderEmail devolve assunto, corpo HTML e corpo texto.
func renderEmail(d emailData) (subject, html, text string) {
	subject = "Seu pós-venda da " + d.ClientName + " está pronto"
	if d.Name != "" {
		subject = d.Name + ", seu pós-venda da " + d.ClientName + " está pronto"
	}
	var hb, tb bytes.Buffer
	if err := emailHTML.ExecuteTemplate(&hb, "post_sale.html", d); err != nil {
		// Templates são embed + parseados no init: erro aqui é bug de
		// programação, não condição de runtime. Zera o HTML e deixa o texto
		// (que sempre vai) carregar o email.
		hb.Reset()
	}
	if err := emailText.ExecuteTemplate(&tb, "post_sale.txt", d); err != nil {
		tb.WriteString("Seu pós-venda está disponível em " + d.Link)
	}
	return subject, hb.String(), tb.String()
}
