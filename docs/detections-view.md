# Detections View — Guia Operacional

Documenta a `/detections` refatorada pelo Plano 3.

> Spec: [`docs/superpowers/specs/2026-05-11-campaign-wizard-design.md`](superpowers/specs/2026-05-11-campaign-wizard-design.md) §7
> Plano: [`docs/superpowers/plans/2026-05-11-plano-3-detections-refactor.md`](superpowers/plans/2026-05-11-plano-3-detections-refactor.md)

## O que mudou

A `/detections` antes era um calendário station × day com hits flat. Não comparava com o plano da campanha. Agora é uma grade station × material × day idêntica à etapa 4 do wizard, alimentada pela view `daily_play_summary`.

## Cores

Veja [`distribution-rules.md`](distribution-rules.md) pra detalhes da semântica. Resumo:

| Cor | Significado |
|-----|-------------|
| Cinza | Esperado (plano) |
| Verde | Tocou dentro da faixa |
| Vermelho | Saldo devedor (esperado − tocou − fora-faixa) |
| Azul (+N) | Bônus (excesso na faixa OU sem regra) |
| Amarelo (+N) | Tocou na data, fora da faixa |
| Roxo (+N) | Tocou fora da data da campanha |

## Como usar

1. Selecione uma campanha no dropdown do topo
2. Use as pills "Mês atual" / "Mês anterior" ou o input de mês pra navegar
3. **CoverageSummary** no topo mostra:
   - Cobertura % (verde ÷ esperado) — verde se ≥95%, amarelo 80-94%, vermelho <80%
   - Totais do mês por categoria
4. **Busca** filtra emissora E material (nome, cidade, dial, banda, título do material)
5. **Clique em célula**: abre `DayDetailModal` com breakdown por categoria + lista de detecções
6. **Clique no header da emissora**: abre `HealthDrawer` com saúde do stream

## Empty states

- **Sem campanha selecionada** → instrução pra escolher uma
- **Campanha sem materiais/regras** → CTA pra editar a campanha (rota `/campaigns/:id/edit`)
- **Sem detections no período** → mensagem neutra

## Limitações conhecidas

- Pesquisar texto não é fuzzy — só substring case-insensitive em campos pré-definidos
- `daily_play_summary` é VIEW não-materializada — pode lentificar com volume alto (ver F-84)
- Não há export CSV/PDF do relatório (futuro F-102)
