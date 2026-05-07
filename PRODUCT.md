# Product

## Register

product

## Users

Dois tipos de usuário:
- **Operadores internos** da equipe de monitoramento: verificam status de emissoras em tempo real, gerenciam campanhas e comerciais, investigam detecções.
- **Clientes anunciantes** (agências e anunciantes diretos): acessam o histórico de veiculação dos seus comerciais para confirmar que foram ao ar.

Contexto de uso: operadores ficam com o sistema aberto durante o dia; clientes acessam pontualmente para checar relatórios.

## Product Purpose

Radiocheck monitora a veiculação de comerciais em emissoras de rádio AM/FM via streaming. É o sistema que registra quando e onde um comercial foi ao ar, com evidência de áudio. É um serviço interno do ecossistema E-radios — não um produto autônomo.

Sucesso: o operador sabe em <30s se um comercial veiculou. O cliente abre o sistema e encontra suas detecções sem precisar de treinamento.

## Brand Personality

Confiável, preciso, real-time.

Tom: técnico mas acessível. Direto, sem jargão desnecessário. Nunca alarmista. Nunca vago.

## Anti-references

Nenhuma referência negativa explícita — o critério é simples: deve parecer E-radios. Qualquer coisa que quebre essa continuidade visual é errada por definição.

Padrões a evitar:
- Dashboards de BI genéricos (estilo Metabase, Grafana padrão)
- Interfaces de sistema interno sem cuidado visual (cinzas neutros, tabelas brutas)
- SaaS com muitos gradientes e glassmorphism decorativo fora de contexto

## Design Principles

1. **Status em primeiro lugar.** Toda tela deve responder a pergunta "o que está acontecendo agora" antes de qualquer outra coisa. Real-time é a proposta central.
2. **Evidência, não apenas afirmação.** Cada detecção tem timestamp, emissora, clip de áudio. O sistema mostra prova, não só o resultado.
3. **Continuidade com E-radios.** Radiocheck é uma extensão do sistema, não um produto separado. Usuário que já usa E-radios deve se sentir em casa imediatamente.
4. **Clareza sob pressão.** Operadores leem o sistema enquanto fazem outras coisas. Status, badges e indicadores precisam ser instantaneamente compreensíveis.
5. **Confiança pela precisão.** Timestamps exatos, contagens corretas, estados sem ambiguidade. Nunca exibir dado aproximado quando o dado exato está disponível.

## Accessibility & Inclusion

WCAG AA como base. Sem requisitos especiais declarados. Garantir contraste adequado mesmo nas paletas vibrantes do design system (Rosa Digital sobre branco requer atenção).
