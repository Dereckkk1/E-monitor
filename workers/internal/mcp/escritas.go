package mcp

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/acoes"
	"radiocheck/internal/mcp/auditoria"
)

// Escritas reúne o que as tools de escrita usam (plano Fatia 3, Task 4). Vive
// em ConfigServidor.Escritas; nil falha fechado (ferramentaEscrita devolve
// [interno] antes de qualquer coisa).
type Escritas struct {
	// Acoes: as escritas de negócio que os handlers HTTP também chamam (regra
	// de ouro da spec §3: a tool não reimplementa).
	Acoes *acoes.Servico
	// Confirmacoes marca o uso único do token do nível confirma.
	Confirmacoes *Confirmacoes
	// Segredo é o JWT_SECRET: assina o token (com separação de domínio).
	Segredo []byte
	// Leitura monta previews e resultados no formato das tools obter_*.
	Leitura *Leitura
	// DB grava a auditoria (audit_log); *pgxpool.Pool serve. Falso nos testes.
	DB auditoria.Execer
	// Agora é o relógio injetável (validade do token).
	Agora func() time.Time
}

func (E *Escritas) agora() time.Time {
	if E.Agora != nil {
		return E.Agora()
	}
	return time.Now()
}

// NovasEscritas monta as dependências das tools de escrita sobre o pool.
func NovasEscritas(pool *pgxpool.Pool, segredo []byte, servico *acoes.Servico, leitura *Leitura) *Escritas {
	E := &Escritas{Acoes: servico, Segredo: segredo, Leitura: leitura}
	if pool != nil { // evita o typed-nil dentro da interface
		E.Confirmacoes = &Confirmacoes{DB: pool}
		E.DB = pool
	}
	return E
}
