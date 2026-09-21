package hub

import "testing"

/*
O espelho Go da `normalizaCodigo` do hub (`backend/src/services/codigoDaCampanha.ts`).

⚠️ Estes casos são os MESMOS do lado de lá de propósito. A tabela §10 da spec
lista "leitura tolerante" como uma das linhas que NÃO PODEM DIVERGIR entre os
dois repositórios, e duas implementações da mesma regra divergem na primeira
pressa. Se um dia mexerem no alfabeto ou no tamanho lá, este arquivo é o que
fica vermelho aqui.
*/
func TestNormalizaCodigo(t *testing.T) {
	casos := []struct {
		rotulo string
		bruto  string
		quer   string
	}{
		{"canônico, como o hub escreve", "EH-7K4M2X", "EH-7K4M2X"},
		{"minúsculo e sem hífen (quem digita)", "eh7k4m2x", "EH-7K4M2X"},
		{"com espaço no lugar do hífen", "EH 7K4M2X", "EH-7K4M2X"},
		{"com espaço em volta (quem cola)", "  EH-7K4M2X  ", "EH-7K4M2X"},
		{"só o corpo, sem prefixo", "7K4M2X", "EH-7K4M2X"},
		{"só o corpo, minúsculo", "7k4m2x", "EH-7K4M2X"},
		{"pontuação no meio", "EH.7K4M-2X", "EH-7K4M2X"},

		{"vazio", "", ""},
		{"só pontuação", "---", ""},
		{"curto demais", "7K4M2", ""},
		{"longo demais", "EH-7K4M2XY", ""},
		// O alfabeto não tem O, 0, I, 1, S nem 5 — são os pares que se confundem
		// ditando por telefone. Um código com eles não é código.
		{"letra fora do alfabeto (O)", "EH-7K4M2O", ""},
		{"dígito fora do alfabeto (0)", "EH-7K4M20", ""},
		{"dígito fora do alfabeto (1)", "EH-7K4M21", ""},
		{"letra fora do alfabeto (S)", "EH-7K4M2S", ""},

		/*
			⚠️ O caso que a spec §3.1 existe para explicar, e o único que uma
			implementação por `strings.HasPrefix` erraria.

			`E` e `H` ESTÃO no alfabeto, então existe código cujo CORPO começa
			com "EH": `EH-EHK2M4`. Cortar o prefixo por prefixo comeria dois
			caracteres do corpo de quem digitasse só o corpo — que é entrada que
			esta mesma função manda aceitar. São ~1 em 900 códigos, e a pessoa
			leria "não encontrado" sem ter errado nada. A decisão é por TAMANHO:
			8 caracteres começando com EH é o código inteiro, 6 é só o corpo.
		*/
		{"corpo que começa com EH, com prefixo", "EH-EHK2M4", "EH-EHK2M4"},
		{"corpo que começa com EH, SEM prefixo", "EHK2M4", "EH-EHK2M4"},
	}

	for _, c := range casos {
		t.Run(c.rotulo, func(t *testing.T) {
			got := NormalizaCodigo(c.bruto)
			if got != c.quer {
				t.Fatalf("NormalizaCodigo(%q) = %q, quero %q", c.bruto, got, c.quer)
			}
		})
	}
}

// Normalizar duas vezes tem de dar o mesmo: a forma canônica é ponto fixo.
// Sem isto, gravar um código já normalizado poderia alterá-lo de novo.
func TestNormalizaCodigoEhIdempotente(t *testing.T) {
	uma := NormalizaCodigo("eh7k4m2x")
	duas := NormalizaCodigo(uma)
	if uma != duas {
		t.Fatalf("normalizar duas vezes deu %q e %q", uma, duas)
	}
}
