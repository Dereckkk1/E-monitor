package hub

// O vocabulário do hub difere do daqui em UM tipo: o E-monitor diz `feature`,
// o hub diz `ideia` (spec do hub §4.1). Status, urgência, prioridade e esforço
// têm os mesmos valores dos dois lados; o hub tem um status a mais, `retirada`.

func TipoParaHub(t string) string {
	if t == "feature" {
		return "ideia"
	}
	return t
}

func TipoDoHub(t string) string {
	if t == "ideia" {
		return "feature"
	}
	return t
}

// Os eventos que o E-monitor já nomeava voltam com o nome daqui, para a
// timeline (`eventPhrase` no front) não mudar. Os outros passam com o nome do
// hub, e o front ganha frase para eles.
var eventosDoHub = map[string]string{
	"criada":     "created",
	"status":     "status_changed",
	"prioridade": "priority_changed",
	"resposta":   "feedback_given",
	"reaberta":   "reopened",
}

func EventoDoHub(t string) string {
	if v, ok := eventosDoHub[t]; ok {
		return v
	}
	return t
}
