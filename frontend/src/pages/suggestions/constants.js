// Metadados compartilhados da Central de Sugestões.
// Mantém labels/ordem/tons em um só lugar (DRY) — usados por pills, filtros,
// board e o form de criação. Os values batem com os CHECKs da migration 0051.

export const STATUS = {
  nova:         { label: 'Nova',         hint: 'Recém-chegada, ainda não triada' },
  em_analise:   { label: 'Em análise',   hint: 'Sendo avaliada pelo dev' },
  aceita:       { label: 'Aceita',       hint: 'No backlog, vai ser feita' },
  em_progresso: { label: 'Em progresso', hint: 'Em desenvolvimento agora' },
  concluida:    { label: 'Concluída',    hint: 'Implementada' },
  recusada:     { label: 'Recusada',     hint: 'Não será feita' },
}
// Ordem canônica do fluxo (usada em selects e no board).
export const STATUS_ORDER = ['nova', 'em_analise', 'aceita', 'em_progresso', 'concluida', 'recusada']
// Colunas do board — recusada fica fora (acessível por filtro), pra não poluir.
export const BOARD_COLUMNS = ['nova', 'em_analise', 'aceita', 'em_progresso', 'concluida']

export const TYPE = {
  bug:      { label: 'Bug',         icon: '🐞' },
  melhoria: { label: 'Melhoria',    icon: '✨' },
  feature:  { label: 'Nova feature', icon: '🚀' },
  duvida:   { label: 'Dúvida',      icon: '❓' },
}
export const TYPE_ORDER = ['bug', 'melhoria', 'feature', 'duvida']

// Prioridade que o AUTOR pede.
export const REQ_PRIORITY = {
  baixa: { label: 'Baixa' },
  media: { label: 'Média' },
  alta:  { label: 'Alta' },
}
export const REQ_PRIORITY_ORDER = ['baixa', 'media', 'alta']

// Prioridade REAL que o dev define na triage.
export const DEV_PRIORITY = {
  urgente: { label: 'Urgente', rank: 0 },
  alta:    { label: 'Alta',    rank: 1 },
  media:   { label: 'Média',   rank: 2 },
  baixa:   { label: 'Baixa',   rank: 3 },
}
export const DEV_PRIORITY_ORDER = ['urgente', 'alta', 'media', 'baixa']

export const EFFORT = {
  P: { label: 'Pequeno' },
  M: { label: 'Médio' },
  G: { label: 'Grande' },
}
export const EFFORT_ORDER = ['P', 'M', 'G']

// Telas/áreas do app — vira dropdown no form. Espelha a sidebar.
export const TARGET_SCREENS = [
  'Emissoras', 'Clientes', 'Tipos de material',
  'Campanhas', 'Veiculações', 'Materiais e Distribuição', 'Mapa ao Vivo',
  'Relatório data/hora', 'Indicadores / Dashboard',
  'Streams', 'Workers',
  'Visão geral', 'Visão Gerencial', 'Monitoramento', 'Falhas por emissora',
  'Usuários', 'Sugestões', 'Login', 'Minha conta',
  'Geral / outra',
]

// Ordena por prioridade do dev (nulls por último), depois por atualização.
export function devPriorityRank(p) {
  return DEV_PRIORITY[p]?.rank ?? 99
}
