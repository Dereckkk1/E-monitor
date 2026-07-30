package postsale

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound cobre relatório inexistente, token inválido, token revogado e
// token de relatório ainda em draft. Um erro só pros quatro casos é
// deliberado: o endpoint público não pode virar oráculo de "este link existiu".
var ErrNotFound = errors.New("postsale: não encontrado")

// ErrAlreadySent vira 409 no handler: republicar mudaria o que o cliente já viu.
var ErrAlreadySent = errors.New("postsale: relatório já enviado")

// Report é o pós-venda como o admin o enxerga (draft ou enviado).
type Report struct {
	ID           uuid.UUID   `json:"id"`
	ClientID     uuid.UUID   `json:"client_id"`
	ClientName   string      `json:"client_name"`
	Title        string      `json:"title"`
	IntroMessage string      `json:"intro_message"`
	Status       string      `json:"status"`
	SentAt       *time.Time  `json:"sent_at"`
	CreatedAt    time.Time   `json:"created_at"`
	Blocks       []BlockRow  `json:"blocks"`
	Recipients   []Recipient `json:"recipients"`
}

// BlockRow é uma campanha do relatório, com o período e o que o admin editou.
type BlockRow struct {
	ID           uuid.UUID    `json:"id"`
	CampaignID   uuid.UUID    `json:"campaign_id"`
	CampaignName string       `json:"campaign_name"`
	From         time.Time    `json:"period_from"`
	To           time.Time    `json:"period_to"`
	Position     int          `json:"position"`
	CheckingText string       `json:"checking_text"`
	CheckingRows []StationRow `json:"checking_rows"`
	// CheckingEdited distingue "ainda não mexi no Checking" de "apaguei todas as
	// linhas de propósito". Sem esse bit, `[]` é ambíguo entre os dois — e o
	// wizard, que salva o bloco no passo 2 antes de qualquer edição, fazia o
	// documento sair com o Checking vazio (migration 0059).
	CheckingEdited bool         `json:"checking_edited"`
	KPIOverrides   KPIOverrides `json:"kpi_overrides"`
	Assets         Assets       `json:"-"` // chaves S3 nunca vão pro frontend
}

// KPIOverrides são os números que o admin digitou à mão, quando o valor do
// sistema não é o que vai ser cobrado (acordo fechado fora da plataforma).
//
// Ponteiro nil = "não sobrescrevi, use o do sistema". Zero é um override
// legítimo (bonificação zerada, por exemplo), então não dá pra usar o valor
// zero como sentinela.
//
// CPM NÃO entra aqui de propósito: é derivado de valor ÷ impactos × 1000. Um CPM
// digitado à mão contradiria os dois números exibidos ao lado dele.
type KPIOverrides struct {
	ValorEntregue *float64 `json:"valor_entregue,omitempty"`
	Impactos      *int64   `json:"impactos,omitempty"`
	Bonificacao   *float64 `json:"bonificacao,omitempty"`
}

// Any diz se há algum override — o payload marca o bloco como ajustado à mão.
func (o KPIOverrides) Any() bool {
	return o.ValorEntregue != nil || o.Impactos != nil || o.Bonificacao != nil
}

// Assets são as chaves S3 dos artefatos gerados no publish. Ficam fora do JSON
// (tag "-") de propósito: o cliente recebe só as ROTAS, que revalidam o token e
// presignam na hora — chave de bucket nunca sai daqui.
//
// O mapa é gravado como objeto próprio além de entrar no zip: o documento
// mostra a imagem na tela, e ninguém vai abrir um zip pra ver o mapa.
type Assets struct {
	BundleZIP   string `json:"bundle_zip,omitempty"`
	MapPNG      string `json:"map_png,omitempty"`
	InsightsPNG string `json:"insights_png,omitempty"`
}

// AssetKind é o que o endpoint público aceita servir por imagem.
type AssetKind string

const (
	AssetMap      AssetKind = "map"
	AssetInsights AssetKind = "insights"
)

// Recipient é um destinatário e o estado do link dele.
type Recipient struct {
	ID          uuid.UUID  `json:"id"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Token       string     `json:"token"`
	EmailStatus string     `json:"email_status"`
	EmailError  *string    `json:"email_error,omitempty"`
	OpenedAt    *time.Time `json:"opened_at"`
	OpenCount   int        `json:"open_count"`
	RevokedAt   *time.Time `json:"revoked_at"`
}

// RecipientInput é quem vai receber, antes de virar linha.
type RecipientInput struct {
	UserID *uuid.UUID
	Email  string
	Name   string
	Token  string
}

// ResolvedReport é o que o endpoint público precisa: o payload congelado e o
// id do destinatário (pra contabilizar a abertura).
type ResolvedReport struct {
	RecipientID uuid.UUID
	ReportID    uuid.UUID
	Payload     []byte
	OpenCount   int
}

// CampaignMeta é o mínimo que o snapshot precisa saber da campanha.
type CampaignMeta struct {
	ID        uuid.UUID
	ClientID  uuid.UUID
	Name      string
	Status    string
	StartDate time.Time
	EndDate   time.Time
}

// CreateDraftInput é o passo 1 do wizard.
type CreateDraftInput struct {
	ClientID     uuid.UUID
	Title        string
	IntroMessage string
	CreatedBy    *uuid.UUID
}

// ListItem é a linha da listagem /admin/pos-venda.
type ListItem struct {
	ID         uuid.UUID  `json:"id"`
	ClientID   uuid.UUID  `json:"client_id"`
	ClientName string     `json:"client_name"`
	ClientLogo *string    `json:"client_logo_url"`
	Title      string     `json:"title"`
	Status     string     `json:"status"`
	SentAt     *time.Time `json:"sent_at"`
	CreatedAt  time.Time  `json:"created_at"`
	Campaigns  int        `json:"campaigns_count"`
	Recipients int        `json:"recipients_count"`
	Opened     int        `json:"opened_count"`
}
