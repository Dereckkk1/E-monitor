package auth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Sem banco, o verificador LIBERA. É o mesmo princípio da falha de consulta:
// esta checagem é uma camada extra sobre o JWT, e transformá-la em bloqueio
// quando não dá para medir derrubaria o sistema inteiro por causa dela.
func TestVerificadorAtivo_SemBancoLibera(t *testing.T) {
	v := NovoVerificadorAtivo(nil, time.Minute)
	if !v.Ativo(context.Background(), uuid.New()) {
		t.Fatal("sem banco tem de liberar")
	}
}

func TestVerificadorAtivo_NilLibera(t *testing.T) {
	var v *VerificadorAtivo
	if !v.Ativo(context.Background(), uuid.New()) {
		t.Fatal("verificador nil tem de liberar")
	}
}

func TestVerificadorAtivo_IdVazioLibera(t *testing.T) {
	v := NovoVerificadorAtivo(nil, time.Minute)
	if !v.Ativo(context.Background(), uuid.Nil) {
		t.Fatal("uuid zero tem de liberar")
	}
}

// O cache é o que torna a checagem barata o bastante para ficar no caminho
// quente. Estes testes exercitam a mecânica dele sem precisar de Postgres,
// escrevendo direto na estrutura — é o único jeito de provar a EXPIRAÇÃO sem
// esperar um minuto de relógio.
func TestVerificadorAtivo_CacheDentroDoTTL(t *testing.T) {
	v := NovoVerificadorAtivo(nil, time.Minute)
	agora := time.Now()
	v.guarda("u1", false, agora)

	ativo, ok := v.doCache("u1", agora.Add(30*time.Second))
	if !ok {
		t.Fatal("dentro do TTL tem de haver cache")
	}
	if ativo {
		t.Fatal("o valor guardado era inativo")
	}
}

func TestVerificadorAtivo_CacheExpira(t *testing.T) {
	v := NovoVerificadorAtivo(nil, time.Minute)
	agora := time.Now()
	v.guarda("u1", false, agora)

	if _, ok := v.doCache("u1", agora.Add(61*time.Second)); ok {
		// Se o cache não expirasse, uma reativação levaria até sempre para valer.
		t.Fatal("passado o TTL, o cache tem de estar vencido")
	}
}

func TestVerificadorAtivo_EsqueceRemove(t *testing.T) {
	v := NovoVerificadorAtivo(nil, time.Minute)
	id := uuid.New()
	agora := time.Now()
	v.guarda(id.String(), false, agora)

	v.Esquece(id)

	if _, ok := v.doCache(id.String(), agora); ok {
		// Sem isto, desativar alguém e conferir em seguida mostraria a pessoa
		// ainda dentro, e o operador concluiria que o botão não funciona.
		t.Fatal("Esquece tem de descartar a entrada")
	}
}

func TestVerificadorAtivo_TTLZeroViraUmMinuto(t *testing.T) {
	v := NovoVerificadorAtivo(nil, 0)
	if v.ttl != time.Minute {
		t.Fatalf("ttl esperado 1min, veio %v", v.ttl)
	}
}
