// Retenção local de evidência (§11.4 variante de prod). Censuras com mais de
// EVIDENCE_RETENTION_DAYS dias são apagadas automaticamente do storage e a
// detecção fica com evidence_status='expired' — a veiculação continua válida,
// só o áudio some. Docs: docs/features/evidence-local-retention.md.
//
// ⚠️ Mantenha EVIDENCE_RETENTION_DAYS igual ao env EVIDENCE_RETENTION_DAYS do
// backend (workers/internal/config/config.go, default 30). Se um dia expor a
// retenção pela API, troque esta constante pelo valor vindo do servidor.
export const EVIDENCE_RETENTION_DAYS = 30

// Título curto do estado (chip / cabeçalho).
export const EVIDENCE_EXPIRED_TITLE = 'Áudio expirado pela retenção'

// Mensagem voltada ao cliente, mostrada quando ele tenta ouvir uma censura
// antiga já apagada. Fonte única de verdade — reescreva aqui.
export const EVIDENCE_EXPIRED_MESSAGE =
  `Censuras de materiais vinculados com mais de ${EVIDENCE_RETENTION_DAYS} dias ` +
  `são apagadas automaticamente pelo sistema. A veiculação continua registrada e ` +
  `válida — apenas o áudio não fica disponível após esse período. Agradecemos a compreensão.`

// Versão curta pra tooltip de botão de play desabilitado.
export const EVIDENCE_EXPIRED_SHORT = `Áudio expirado — censuras com mais de ${EVIDENCE_RETENTION_DAYS} dias são apagadas automaticamente`
