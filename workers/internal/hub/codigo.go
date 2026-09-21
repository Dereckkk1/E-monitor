package hub

import "strings"

/*
O formato do código da campanha no E-Hub, espelhado deste lado.

⚠️ Isto é uma SEGUNDA implementação de uma regra que já existe no hub
(`backend/src/services/codigoDaCampanha.ts`), e segunda implementação é coisa
que diverge. Ela existe porque a tabela §10 da spec de 2026-09-18 põe "leitura
tolerante — normaliza antes de mandar" do lado do E-monitor: o código que este
repositório GRAVA em `campaigns.hub_code` tem de ser a forma canônica, e quem
digita escreve `eh7k4m2x`.

O hub também normaliza na entrada, então mandar cru funcionaria hoje. O que NÃO
funciona é gravar cru: `EH-7K4M2X` e `eh7k4m2x` seriam o mesmo código para o hub
e códigos diferentes aqui — e a decisão 5 da spec diz que dois PIs compartilham
um código de propósito, ou seja, agrupar por ele tem significado.

O que não pode divergir está em `codigo_test.go`, com os mesmos casos do teste
de lá.
*/

// ALFABETO tem 30 símbolos. Faltam de propósito `O`, `0`, `I`, `1`, `S` e `5` —
// os pares que se confundem ditando por telefone, que é como este código
// circula.
const alfabeto = "2346789ABCDEFGHJKLMNPQRTUVWXYZ"

const prefixo = "EH-"

// tamanho é o do CORPO, sem o prefixo.
const tamanho = 6

// letrasDoPrefixo é o prefixo sem o hífen — DERIVADO, nunca escrito à mão: é
// com ele que o texto limpo (que já perdeu a pontuação) é comparado.
var letrasDoPrefixo = strings.ReplaceAll(prefixo, "-", "")

// NormalizaCodigo devolve a forma canônica (`EH-7K4M2X`) de um código digitado
// de qualquer jeito, ou string vazia quando o que veio não é um código.
//
// ⚠️ O corte do prefixo é por TAMANHO do texto limpo, NUNCA por
// `strings.HasPrefix("EH")`. `E` e `H` estão no alfabeto, então existe código
// cujo CORPO começa com "EH" — `EH-EHK2M4`. Cortando por prefixo, quem digitasse
// só o corpo `EHK2M4` perderia dois caracteres dele e leria "código não
// encontrado" sem ter errado nada. São ~1 em 900 códigos. Depois de tirar o que
// não é alfanumérico sobram duas formas válidas e só duas: 8 caracteres
// começando com EH (o código inteiro) ou 6 (só o corpo).
func NormalizaCodigo(bruto string) string {
	limpo := make([]rune, 0, len(bruto))
	for _, r := range strings.ToUpper(bruto) {
		if (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') {
			limpo = append(limpo, r)
		}
	}

	var corpo string
	switch {
	case len(limpo) == tamanho+len(letrasDoPrefixo) && strings.HasPrefix(string(limpo), letrasDoPrefixo):
		corpo = string(limpo[len(letrasDoPrefixo):])
	case len(limpo) == tamanho:
		corpo = string(limpo)
	default:
		return ""
	}

	for _, r := range corpo {
		if !strings.ContainsRune(alfabeto, r) {
			return ""
		}
	}
	return prefixo + corpo
}
