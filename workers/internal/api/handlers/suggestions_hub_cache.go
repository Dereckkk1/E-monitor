package handlers

import (
	"strings"
	"sync"
	"time"
)

/*
cacheNaoLidas segura a contagem do badge por autor (spec do hub §4.4).

A sidebar pede `/suggestions/unread-count` de minuto em minuto, em toda aba
aberta de toda pessoa. Sem isto, cada pedido viraria uma ida ao hub, e o teto da
porta de lá é por IP — ou seja, desta plataforma inteira.

Em memória do processo, de propósito: são poucas dezenas de internos (o mapa não
cresce além disso), e perder o cache num restart custa só uma ida ao hub.
Quem ESCREVE (criar, comentar, marcar lida) esquece a entrada do autor, para o
badge não mentir 30 s depois de a própria pessoa agir.
*/
type cacheNaoLidas struct {
	mu    sync.Mutex
	ttl   time.Duration
	agora func() time.Time
	itens map[string]itemNaoLidas
}

type itemNaoLidas struct {
	n   int
	ate time.Time
}

func novoCacheNaoLidas(ttl time.Duration) *cacheNaoLidas {
	return &cacheNaoLidas{ttl: ttl, agora: time.Now, itens: map[string]itemNaoLidas{}}
}

func chaveDoAutor(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func (c *cacheNaoLidas) pega(email string) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.itens[chaveDoAutor(email)]
	if !ok || !c.agora().Before(it.ate) {
		return 0, false
	}
	return it.n, true
}

func (c *cacheNaoLidas) guarda(email string, n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.itens[chaveDoAutor(email)] = itemNaoLidas{n: n, ate: c.agora().Add(c.ttl)}
}

func (c *cacheNaoLidas) esquece(email string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.itens, chaveDoAutor(email))
}
