package mcptest

// escrita.go — falsos dos colaboradores das ações de escrita (internal/acoes):
// o supervisor de workers e o cliente do hub. Servem aos testes das ações, dos
// handlers HTTP que as chamam e das tools do MCP (a paridade de efeito compara
// as chamadas que cada caminho fez ao MESMO tipo de falso).
//
// ⚠️ Não importam internal/acoes: acoes importa este pacote nos seus testes, e o
// ciclo derrubaria a compilação. Satisfazem acoes.SupervisorDeCampanha e
// acoes.AvisoHub (e handlers.CampaignSupervisor) só estruturalmente.

import (
	"context"
	"net/http"
	"sync"

	"github.com/google/uuid"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

// Sincrono é o EmSegundoPlano dos testes: roda o gatilho assíncrono na hora, e o
// teste observa o efeito sem esperar nem fazer polling.
func Sincrono(f func()) { f() }

// ChamadaSupervisor é uma chamada registrada ao SupervisorFalso.
type ChamadaSupervisor struct {
	Op        string // "UpdateStations", "Reload", "Start" ou "StopWorkers"
	Campanha  uuid.UUID
	Emissoras []uuid.UUID // só em UpdateStations
}

// SupervisorFalso registra o que as ações pedem ao supervisor, sem tocar worker
// nem NATS. Se Campanhas estiver preenchido, UpdateStations também grava
// target_stations como o supervisor real faz (o banco primeiro) — para os
// testes que comparam a linha gravada.
type SupervisorFalso struct {
	Campanhas *catalog.Campaigns

	// Erros programados (nil = sucesso).
	ErroUpdateStations error
	ErroReload         error

	mu       sync.Mutex
	chamadas []ChamadaSupervisor
}

func (s *SupervisorFalso) registra(c ChamadaSupervisor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chamadas = append(s.chamadas, c)
}

func (s *SupervisorFalso) UpdateStations(id uuid.UUID, emissoras []uuid.UUID) error {
	s.registra(ChamadaSupervisor{Op: "UpdateStations", Campanha: id, Emissoras: append([]uuid.UUID{}, emissoras...)})
	if s.ErroUpdateStations != nil {
		return s.ErroUpdateStations
	}
	if s.Campanhas != nil {
		return s.Campanhas.UpdateTargetStations(context.Background(), id, emissoras)
	}
	return nil
}

func (s *SupervisorFalso) Reload(id uuid.UUID) error {
	s.registra(ChamadaSupervisor{Op: "Reload", Campanha: id})
	return s.ErroReload
}

// Start e StopWorkersForCampaign completam handlers.CampaignSupervisor.
func (s *SupervisorFalso) Start(id uuid.UUID) error {
	s.registra(ChamadaSupervisor{Op: "Start", Campanha: id})
	return nil
}
func (s *SupervisorFalso) StopWorkersForCampaign(id uuid.UUID) {
	s.registra(ChamadaSupervisor{Op: "StopWorkers", Campanha: id})
}

// Chamadas devolve uma cópia, na ordem em que aconteceram.
func (s *SupervisorFalso) Chamadas() []ChamadaSupervisor {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ChamadaSupervisor(nil), s.chamadas...)
}

// Contagem diz quantas vezes a operação foi chamada para a campanha.
func (s *SupervisorFalso) Contagem(op string, campanha uuid.UUID) int {
	n := 0
	for _, c := range s.Chamadas() {
		if c.Op == op && c.Campanha == campanha {
			n++
		}
	}
	return n
}

// EventoHub é um evento emitido ao hub (campanha.upsert, cliente.upsert).
type EventoHub struct {
	Tipo  string
	Dados any
}

// HubFalso faz as vezes do cliente do hub: registra os eventos emitidos e as
// conferências de código, e responde ao que o teste programar.
type HubFalso struct {
	// SemConfiguracao liga o "esta instalação não tem hub" (Configured() = false).
	SemConfiguracao bool
	// Conferir responde a ConferirCodigo. nil = o hub não conhece o código (404).
	Conferir func(codigo string) (*hub.CampanhaDoHub, error)
	// ErroEmitir é devolvido por Emitir/EmitirComResposta (nil = aceito).
	ErroEmitir error
	// RespostaEmitir é o corpo do 200 do hub (nil = aceito, ação "atualiza").
	RespostaEmitir *hub.RespostaEvento

	mu         sync.Mutex
	eventos    []EventoHub
	conferidos []string
}

func (h *HubFalso) Configured() bool { return !h.SemConfiguracao }

func (h *HubFalso) registra(tipo string, dados any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.eventos = append(h.eventos, EventoHub{Tipo: tipo, Dados: dados})
}

func (h *HubFalso) Emitir(_ context.Context, tipo string, dados any) error {
	h.registra(tipo, dados)
	return h.ErroEmitir
}

func (h *HubFalso) EmitirComResposta(_ context.Context, tipo string, dados any) (*hub.RespostaEvento, error) {
	h.registra(tipo, dados)
	if h.ErroEmitir != nil {
		return nil, h.ErroEmitir
	}
	if h.RespostaEmitir != nil {
		return h.RespostaEmitir, nil
	}
	return &hub.RespostaEvento{Acao: "atualiza"}, nil
}

func (h *HubFalso) ConferirCodigo(_ context.Context, codigo string) (*hub.CampanhaDoHub, error) {
	h.mu.Lock()
	h.conferidos = append(h.conferidos, codigo)
	h.mu.Unlock()
	if h.Conferir == nil {
		return nil, &hub.Error{Status: http.StatusNotFound, Message: "código não encontrado na Central de Clientes"}
	}
	return h.Conferir(codigo)
}

// Eventos devolve uma cópia dos eventos emitidos, na ordem.
func (h *HubFalso) Eventos() []EventoHub {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]EventoHub(nil), h.eventos...)
}

// EventosDoTipo filtra Eventos por tipo.
func (h *HubFalso) EventosDoTipo(tipo string) []EventoHub {
	var out []EventoHub
	for _, e := range h.Eventos() {
		if e.Tipo == tipo {
			out = append(out, e)
		}
	}
	return out
}

// Conferidos devolve os códigos (já canônicos) que a barreira perguntou ao hub.
func (h *HubFalso) Conferidos() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.conferidos...)
}

// CampanhaDoClienteLigado monta a resposta do hub para um código cujo cliente do
// hub aponta (ponte) para o cliente local informado.
func CampanhaDoClienteLigado(nome string, clienteLocal uuid.UUID) *hub.CampanhaDoHub {
	c := &hub.CampanhaDoHub{HubCampaignID: "hub-" + uuid.NewString()[:8], Nome: nome, Inicio: "2026-12-01", Fim: "2027-02-28"}
	c.Cliente.ID = "hub-cli-" + uuid.NewString()[:8]
	c.Cliente.Nome = "Cliente do hub"
	c.Cliente.IDNaPlataforma = clienteLocal.String()
	return c
}
