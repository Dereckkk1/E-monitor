package auth

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VerificadorAtivo responde "esta pessoa ainda pode usar o sistema?" sem pagar
// uma consulta por requisição.
//
// # Por que isto existe
//
// O `RequireJWT` valida assinatura e expiração, e mais nada. Como o token vale
// 8 horas, marcar `is_active = false` não tira ninguém que já esteja dentro —
// só barra o próximo login. Isso deixava DOIS controles sem efeito prático:
//
//  1. O botão "bloquear usuário" do /admin/monitoring, cujo próprio comentário
//     registra a lacuna e a chama de follow-up "JWT revogação imediata".
//  2. O `user.deactivate` que o hub empurra (RFC-001 §9.2) — a Central de
//     Clientes desativa a pessoa e ela continua trabalhando aqui por até 8h.
//
// Um controle de segurança com um desvio silencioso é pior que a ausência dele:
// quem clica acredita que cortou.
//
// # Por que cache, e por que 60 segundos
//
// Consultar o banco em toda requisição autenticada é correto e caro no lugar
// mais quente do sistema. Com TTL de 60s, cada usuário custa no máximo uma
// consulta por minuto — e a janela de revogação cai de 8 horas para um minuto.
// A diferença entre 0s e 60s não muda nenhuma decisão de operação; a diferença
// entre 60s e 8h muda todas.
//
// Descartada uma deny-list em Redis (o padrão que o hub usa no `iatfloor`): o
// Go deste repositório NÃO usa Redis — não está no `go.mod`, aparece só como
// sonda de health. Seria dependência nova no caminho quente.
type VerificadorAtivo struct {
	db  *pgxpool.Pool
	ttl time.Duration

	mu    sync.RWMutex
	cache map[string]entradaAtivo
}

type entradaAtivo struct {
	ativo bool
	ate   time.Time
}

// Acima disto, a limpeza roda antes de inserir. Não é gestão de memória séria —
// é um teto para o mapa não crescer para sempre num processo de vida longa. A
// base real tem dezenas de usuários; o teto existe para o dia em que não tiver.
const maxEntradasAtivo = 10_000

func NovoVerificadorAtivo(db *pgxpool.Pool, ttl time.Duration) *VerificadorAtivo {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &VerificadorAtivo{db: db, ttl: ttl, cache: map[string]entradaAtivo{}}
}

func (v *VerificadorAtivo) doCache(userID string, agora time.Time) (bool, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	e, ok := v.cache[userID]
	if !ok || agora.After(e.ate) {
		return false, false
	}
	return e.ativo, true
}

func (v *VerificadorAtivo) guarda(userID string, ativo bool, agora time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.cache) >= maxEntradasAtivo {
		for k, e := range v.cache {
			if agora.After(e.ate) {
				delete(v.cache, k)
			}
		}
	}
	v.cache[userID] = entradaAtivo{ativo: ativo, ate: agora.Add(v.ttl)}
}

// Ativo diz se o usuário pode seguir. Consulta o banco no máximo uma vez por
// TTL por usuário.
//
// # A falha do banco libera, e isso é deliberado
//
// Postgres fora do ar já é uma indisponibilidade. Se aqui a resposta fosse
// "bloqueia", o sistema inteiro deslogaria todo mundo por causa dela — trocar
// uma indisponibilidade parcial por uma total, e ainda por cima no exato momento
// em que ninguém consegue investigar. O JWT continua validado; o que se perde é
// só a checagem extra, enquanto durar a falha. E o resultado da falha NÃO é
// gravado no cache: assim que o banco volta, a checagem volta na primeira
// requisição, e não daqui a um minuto.
func (v *VerificadorAtivo) Ativo(ctx context.Context, userID uuid.UUID) bool {
	if v == nil || v.db == nil || userID == uuid.Nil {
		return true
	}
	chave := userID.String()
	agora := time.Now()
	if ativo, ok := v.doCache(chave, agora); ok {
		return ativo
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var ativo bool
	// `deleted_at IS NULL` junto: usuário apagado é tão inativo quanto o
	// desativado, e deixá-lo passar aqui seria uma porta que ninguém pensou em
	// fechar porque a linha "não existe mais" na cabeça de quem apagou.
	err := v.db.QueryRow(ctx,
		`SELECT is_active AND deleted_at IS NULL FROM users WHERE id = $1`,
		userID).Scan(&ativo)
	if err != nil {
		// Inclui o caso "usuário não existe": um JWT assinado por nós para um id
		// que sumiu do banco. Liberar aqui é consistente com o resto — e o token
		// expira em no máximo 8h de qualquer forma.
		return true
	}

	v.guarda(chave, ativo, agora)
	return ativo
}

// Esquece descarta o que estiver em cache para um usuário.
//
// Serve a quem acabou de mudar `is_active` e não quer esperar o TTL — o handler
// de bloqueio do admin e o receptor do sync do hub. Sem isto, desativar alguém e
// conferir em seguida mostraria a pessoa ainda dentro, e o operador concluiria
// que o botão não funciona.
func (v *VerificadorAtivo) Esquece(userID uuid.UUID) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.cache, userID.String())
}
