package acoes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

// PrazoConferenciaHub é o teto de espera de UMA conferência de código contra o
// hub — a da barreira de criar/editar campanha e a da rota da tela.
//
// ⚠️ Existe porque o `hub.New` fixa `http.Client{Timeout: 10s}`, e esses 10s NÃO
// podem ser mexidos: é o mesmo cliente do `Exchange`, que roda dentro de um
// login e precisa do fôlego. Aqui quem espera é uma pessoa (ou o modelo), e um
// hub que aceita a conexão e não responde fazia a espera inteira para chegar ao
// MESMO desfecho que dois segundos já dão: segue, e o job de reemissão leva a
// campanha quando o hub voltar. Medido em 2026-09-21: 10,0 s no POST, devolvendo
// o mesmo 201.
const PrazoConferenciaHub = 2 * time.Second

func (s *Servico) hubConfigurado() bool {
	return s.Hub != nil && s.Hub.Configured()
}

func recusaDoHub(msg string) *Recusa {
	return &Recusa{Codigo: CodigoHub, Mensagem: msg}
}

// ConferirCodigoHub é a barreira de verdade do §4.4 da spec do código da
// campanha, compartilhada por criar e editar campanha (a rota e o MCP).
//
// Devolve o código na forma CANÔNICA, ou uma *Recusa{codigo_hub} quando não dá
// para seguir. TRÊS desfechos, e eles não são o mesmo:
//   - o hub diz que o código é de OUTRO cliente (ou a ligação não está completa)
//     → recusa. É erro de quem digitou, e a mensagem diz de quem é o código;
//   - o hub diz 404, ou o texto nem é um código → recusa, idem;
//   - o hub NÃO respondeu (mudo, 401, 429, 5xx) → SEGUE. Decisão 2 da spec: uma
//     queda do hub não pode parar o cadastro de campanha no E-monitor. O
//     `hub_notified_at` fica nulo e o job de reemissão leva a campanha quando o
//     hub voltar. Recusar aqui seria o contrário do que a decisão 2 pede.
//
// ⚠️ E o que volta é CANÔNICO. Sem isto, `EH-7K4M2X` e `eh7k4m2x` viram códigos
// diferentes aqui enquanto para o hub são o mesmo, e a §10 exige a forma
// canônica na coluna. Vale inclusive quando o hub não respondeu — aí a
// normalização é local (hub.NormalizaCodigo, sem tocar a rede).
//
// Código vazio = "sem código": a decisão 6 permite (campanha antiga, ou criada
// por carga); quem exige o campo é a tela, na criação.
func (s *Servico) ConferirCodigoHub(ctx context.Context, clienteID uuid.UUID, bruto string) (string, error) {
	canonico := hub.NormalizaCodigo(bruto)
	if canonico == "" {
		if strings.TrimSpace(bruto) != "" {
			// Veio alguma coisa, e não é código. Recusar daqui evita gravar lixo
			// na coluna que o job vai ler de 15 em 15 minutos para sempre.
			return "", recusaDoHub("código do hub inválido")
		}
		return "", nil
	}
	if !s.hubConfigurado() {
		// Sem hub não há com quem conferir, e a normalização local já entrega o
		// que a §10 exige na coluna.
		return canonico, nil
	}

	// ⚠️ Deadline PRÓPRIO, e curto, SÓ para a ida ao hub: o resto (a ligação,
	// no banco) segue com o contexto de quem chamou.
	ctxHub, cancel := context.WithTimeout(ctx, PrazoConferenciaHub)
	defer cancel()

	doHub, err := s.Hub.ConferirCodigo(ctxHub, canonico)
	var he *hub.Error
	switch {
	case err == nil:
		if s.Ligacoes != nil {
			// A regra da ligação SUBSTITUI a de "outro cliente": ela também cobre
			// esse caso (hub apontando para outro cliente em uso → bloqueado) e
			// separa dele o duplicado vazio, que tem conserto.
			f, ferr := s.Ligacoes.Fatos(ctx, clienteID, doHub.Cliente.ID, doHub.Cliente.IDNaPlataforma, doHub.Cliente.CNPJ)
			if ferr == nil {
				if l := catalog.EstadoDaLigacao(*f, doHub.Cliente.ID, doHub.Cliente.Nome); l.Estado != catalog.EstadoLigado {
					return "", recusaDoHub(l.Mensagem)
				}
			} else {
				// Deliberado, como o hub fora do ar: o banco falhar não trava o salvar.
				zap.L().Warn("barreira da ligação não conferiu; seguindo",
					zap.Error(ferr), zap.String("hub_code", canonico))
			}
		} else if !clienteDoHubConfere(doHub.Cliente.IDNaPlataforma, clienteID) {
			return "", recusaDoHub(fmt.Sprintf(
				"esse código é da campanha %q, de outro cliente (%s)",
				doHub.Nome, doHub.Cliente.Nome))
		}
	case errors.As(err, &he) && he.Status == http.StatusNotFound:
		return "", recusaDoHub("código do hub não encontrado")
	default:
		// Não deu para conferir. Loga e segue — ver a decisão 2 acima.
		zap.L().Warn("nao deu para conferir o codigo do hub",
			zap.String("hub_code", canonico), zap.Error(err))
	}
	return canonico, nil
}

// clienteDoHubConfere diz se a ponte que o hub devolveu aponta para ESTE cliente.
//
// ⚠️ Compara UUID com UUID, e não string com string. Deste lado o valor é sempre
// `uuid.String()` — minúsculo, canônico. No hub, `Client.emonitorClientId` é
// `String` com `trim` e mais nada, digitado no formulário de `/admin/clientes`.
// Um UUID colado em MAIÚSCULAS (que é exatamente como o Compass o mostra) fazia
// TODA campanha daquele cliente levar 422 dizendo "é de outro cliente (Y)" — onde
// Y é o nome do PRÓPRIO cliente. Falha fechada, com a mensagem mais confusa
// possível.
//
// ⚠️ E "não é UUID" é ponte AUSENTE, não divergência — o mesmo tratamento que a
// §6.1 da spec dá ao cliente do hub sem `emonitorClientId`. Lixo naquele campo
// não é afirmação de que a campanha é de outro cliente; é afirmação de que
// ninguém ligou os dois cadastros direito. Recusar aqui barraria o cliente
// inteiro por um erro de digitação do outro lado.
func clienteDoHubConfere(idNaPlataforma string, local uuid.UUID) bool {
	doHub, err := uuid.Parse(strings.TrimSpace(idNaPlataforma))
	if err != nil {
		return true
	}
	return doHub == local
}

// ─────────────────────────────── avisos ao hub ───────────────────────────────

// AvisarHubDaCampanha manda `campanha.upsert` FORA do caminho da resposta.
//
// # Por que goroutine, e por que context.Background
//
// A criação da campanha NÃO PODE depender do hub estar de pé. E o contexto do
// pedido morre quando a resposta é escrita: usá-lo cancelaria a emissão no exato
// instante em que ela começa — defeito intermitente e silencioso, a pior
// combinação possível.
//
// # O preço desta escolha, escrito para quem for mexer
//
// Evento perdido é campanha que nunca chega ao hub, e não há nada em tela
// nenhuma dizendo que falta alguma. Quem conserta isso é a varredura periódica
// do hubnotify (a cada 15 min). Até lá, isto é atraso invisível, não erro
// visível.
//
// É a versão "solta" (sempre goroutine) que a reemissão do botão Ligar também
// usa; as ações passam por Servico.avisarHubDaCampanha, que respeita o
// EmSegundoPlano injetado nos testes.
func AvisarHubDaCampanha(h AvisoHub, repo *catalog.Campaigns, c *catalog.Campaign) {
	if h == nil || !h.Configured() || c == nil {
		return
	}
	// Os campos são copiados AQUI, síncrono, de propósito: a goroutine não pode
	// ler `c` depois que o chamador seguiu adiante e talvez o alterou.
	dados := UpsertDaCampanha(c)
	go EmitirCampanhaNoHub(h, repo, c.ID, dados)
}

func (s *Servico) avisarHubDaCampanha(c *catalog.Campaign) {
	if !s.hubConfigurado() || c == nil {
		return
	}
	dados := UpsertDaCampanha(c)
	h, repo := s.Hub, s.Campanhas
	s.emSegundoPlano(func() { EmitirCampanhaNoHub(h, repo, c.ID, dados) })
}

// UpsertDaCampanha monta o `campanha.upsert` de uma campanha daqui.
func UpsertDaCampanha(c *catalog.Campaign) hub.CampanhaUpsert {
	return hub.CampanhaUpsert{
		IDNaPlataforma:        c.ID.String(),
		IDClienteNaPlataforma: c.ClientID.String(),
		// ⚠️ SEM esta linha, nada da série funciona. O hub casa a campanha pelo
		// código; um `campanha.upsert` sem ele é ignorado com
		// `platform.campanha.ignorada` motivo `sem-codigo` (§6.1) — sem erro, sem
		// retentativa, e sem nada em tela nenhuma denunciando. A campanha nasceria
		// certa aqui e nunca chegaria lá.
		//
		// Vazio é significativo e não é omissão — mas ele só CONGELA quando o hub
		// já tem proposta daquela fonte: `campanhaDaPlataforma.ts` exige um
		// `Client` com a ponte casando E uma proposta achada para cair em `congela`
		// (§6.5); fora disso é `sem-codigo`, ignorado. Pelo `Create`, onde nunca há
		// proposta anterior, vazio é sempre ignorado.
		HubCode: deref(c.HubCode),
	}
}

// EmitirCampanhaNoHub é a emissão SÍNCRONA que AvisarHubDaCampanha roda numa
// goroutine. Separada para quem já está fora do caminho da resposta (a
// reemissão do botão Ligar) emitir em sequência, sem abrir uma goroutine por
// campanha.
func EmitirCampanhaNoHub(h AvisoHub, repo *catalog.Campaigns, id uuid.UUID, dados hub.CampanhaUpsert) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resposta, err := h.EmitirComResposta(ctx, "campanha.upsert", dados)
	if err == nil && resposta.Ignorado() {
		// ⚠️ 200 NÃO é entrega. O hub responde 200 com
		// `{"acao":"ignorado","motivo":"codigo-inexistente"}` quando DESCARTA o
		// evento pela regra dele (§6.1). Marcar aqui tiraria a campanha da fila de
		// reemissão para sempre sem ela nunca ter chegado — e nada em tela nenhuma
		// denunciaria.
		zap.L().Error("hub: campanha.upsert foi IGNORADO pelo hub",
			zap.String("campaign_id", id.String()),
			zap.String("motivo", resposta.Motivo))
		return
	}
	if err != nil {
		// Log e mais nada: não há a quem devolver o erro, e repetir aqui seria
		// inventar uma fila sem durabilidade. Só o `campaign_id` e o erro; o
		// payload NÃO vai para o log.
		zap.L().Error("hub: campanha.upsert falhou",
			zap.String("campaign_id", id.String()), zap.Error(err))
		return
	}
	// A confirmação é o que TIRA a campanha da fila do job de reemissão. Sem ela,
	// uma campanha entregue com sucesso continuaria sendo reenviada de 15 em 15
	// minutos para sempre.
	//
	// ⚠️ Só depois do sucesso. Marcar um envio que falhou transformaria "tentou e
	// não deu" em "já avisou", e a campanha sairia da fila sem nunca ter chegado —
	// fila que esquece é pior que fila que repete.
	//
	// ⚠️ E com o `ctx` desta emissão, que nasce de `context.Background()`: o
	// contexto do pedido já morreu quando a resposta foi escrita.
	if repo != nil {
		// O código vai junto: a marcação só vale se a coluna ainda disser o que
		// ESTE evento entregou. Ver o comentário do repositório.
		if err := repo.MarcarHubNotificada(ctx, id, dados.HubCode); err != nil {
			zap.L().Warn("hub: entregou a campanha mas nao marcou hub_notified_at",
				zap.String("campaign_id", id.String()), zap.Error(err))
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ptrOuNil devolve nil para string vazia — é o par do `NULLIF` que o
// `AtualizarHubCode` usa, para a resposta dizer o mesmo que a coluna passou a
// guardar.
func ptrOuNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
