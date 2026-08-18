// Valor grande dos cards do /insights, com corpo que se ajusta sozinho.
//
// Por que existe: o valor era `font-size: 30px` fixo. Cards vivem em trilhas
// de grid de 180–260px (ver .in-row--cards em InsightsPage.css), então um
// valor longo — "R$ 299.315,84", "1.299.315" — transbordava o card e invadia
// o vizinho. Encolher o corpo pra todo mundo resolveria o transbordo mas
// jogaria fora a hierarquia visual dos valores curtos ("R$ 9,43").
//
// O ajuste depende de DUAS coisas: a largura do card (container query, `cqi`)
// e o nº de caracteres do valor — que o CSS não enxerga. Daí o `--len` inline:
// o CSS calcula `largura_util ÷ (len × largura_media_do_glifo)` e limita o
// resultado entre um piso e o corpo de projeto. Ver .in-card-value.
export default function CardValue({ children, className = '' }) {
  const text = String(children ?? '')
  return (
    <div
      className={`in-card-value ${className}`.trim()}
      style={{ '--len': text.length }}
    >
      {text}
    </div>
  )
}
