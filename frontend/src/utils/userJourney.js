/*
 * userJourney.js — traduz o fluxo cru de requests HTTP de um usuário (vindo de
 * /admin/monitoring/actor-detail) numa jornada legível: uma sequência de ações
 * em português, agrupada por sessão, com horários.
 *
 * NÃO há backend novo aqui. A fonte é `system_metrics` (1 row por request), que
 * guarda o PADRÃO de rota do chi (`/v1/internal/campaigns/{id}`), não o ID real.
 * Por isso as ações falam "uma campanha", nunca "campanha #248" — o dado não
 * carrega o ID. Ver docs/features/admin-monitoring-user-journey.md.
 *
 * Fluxo: describeEvent() mapeia (método, rota) → {area, action, category};
 * buildJourney() ordena cronologicamente, quebra em sessões por intervalo de
 * inatividade e colapsa rajadas repetidas (polling) numa linha só.
 */

// ── Categorias ──────────────────────────────────────────────────────────────
// A categoria dirige o ícone e a cor da linha na timeline. A "área" (Campanhas,
// Detecções…) é o chip de contexto. Um evento = onde (área) + o quê (categoria).
export const CATEGORY = {
  session: 'session',       // login / sessão
  navigate: 'navigate',     // abriu uma tela / consultou algo (GET de leitura)
  create: 'create',         // criou um recurso
  update: 'update',         // editou / alterou estado
  destructive: 'destructive', // excluiu / cancelou / desativou / bloqueou
  export: 'export',         // relatório / CSV / download
  evidence: 'evidence',     // ouviu áudio / abriu evidência / comprovante
  admin: 'admin',           // rotina operacional de admin (tiering, calibração…)
  other: 'other',
}

// ── Normalização de rota ────────────────────────────────────────────────────
// O RoutePattern do chi vem com o prefixo de versão e, em sub-routers montados
// (r.Route("/commercials", …)), com barra final na rota de listagem
// (`/v1/internal/commercials/`). Tiramos o prefixo e a barra final para casar
// com as chaves do dicionário.
export function normalizeRoute(route) {
  if (!route) return ''
  let p = String(route).split('?')[0]
  if (p.startsWith('/v1/internal')) p = p.slice('/v1/internal'.length)
  else if (p.startsWith('/v1')) p = p.slice('/v1'.length)
  if (p === '') p = '/'
  if (p.length > 1 && p.endsWith('/')) p = p.slice(0, -1)
  return p
}

// ── Rotas de fundo (ruído mecânico, não é ação do usuário) ──────────────────
// /auth/me e web-vitals disparam sozinhas no carregamento/refresh do SPA. Health
// é probe interno. Ficam de fora da jornada.
const BACKGROUND = new Set([
  'GET /auth/me',
  'POST /web-vitals',
  'GET /health',
])

export function isBackground(req) {
  const method = (req.method || 'GET').toUpperCase()
  return BACKGROUND.has(`${method} ${normalizeRoute(req.route)}`)
}

// ── Dicionário rota → ação ──────────────────────────────────────────────────
// Chave: "MÉTODO /rota-normalizada". Valor: { area, action, category }.
// Cobre o que o frontend admin realmente chama; o resto cai no fallback abaixo.
const A = (area, action, category) => ({ area, action, category })

const EVENT_MAP = {
  // Sessão & conta
  'POST /auth/login': A('Sessão', 'Entrou na plataforma', CATEGORY.session),
  'PATCH /auth/me': A('Conta', 'Editou o próprio perfil', CATEGORY.update),
  'POST /auth/me/password': A('Conta', 'Trocou a própria senha', CATEGORY.update),

  // Campanhas
  'GET /campaigns': A('Campanhas', 'Abriu a lista de campanhas', CATEGORY.navigate),
  'GET /campaigns/financials': A('Campanhas', 'Consultou o financeiro das campanhas', CATEGORY.navigate),
  'GET /campaigns/{id}': A('Campanhas', 'Abriu uma campanha', CATEGORY.navigate),
  'GET /campaigns/{campaignID}/daily-summary': A('Campanhas', 'Abriu o resumo diário de uma campanha', CATEGORY.navigate),
  'POST /campaigns': A('Campanhas', 'Criou uma campanha', CATEGORY.create),
  'PUT /campaigns/{id}': A('Campanhas', 'Editou uma campanha', CATEGORY.update),
  'PUT /campaigns/{id}/stations': A('Campanhas', 'Alterou as emissoras de uma campanha', CATEGORY.update),
  'PUT /campaigns/{id}/fixed-cpm': A('Campanhas', 'Definiu o CPM fixo de uma campanha', CATEGORY.update),
  'DELETE /campaigns/{id}': A('Campanhas', 'Excluiu uma campanha', CATEGORY.destructive),
  'POST /campaigns/{id}/cancel': A('Campanhas', 'Cancelou uma campanha', CATEGORY.destructive),
  'PUT /campaigns/{id}/start': A('Campanhas', 'Iniciou uma campanha', CATEGORY.update),
  'PUT /campaigns/{id}/pause': A('Campanhas', 'Pausou uma campanha', CATEGORY.update),
  'GET /campaigns/{campaignID}/materials': A('Campanhas', 'Viu os materiais de uma campanha', CATEGORY.navigate),
  'POST /campaigns/{campaignID}/materials': A('Campanhas', 'Vinculou um material a uma campanha', CATEGORY.update),
  'PUT /campaigns/{campaignID}/materials/{materialID}/stations': A('Campanhas', 'Ajustou as emissoras de um material na campanha', CATEGORY.update),
  'DELETE /campaigns/{campaignID}/materials/{materialID}': A('Campanhas', 'Desvinculou um material de uma campanha', CATEGORY.destructive),
  'GET /campaigns/{campaignID}/distribution-rules': A('Campanhas', 'Viu as regras de distribuição', CATEGORY.navigate),
  'POST /campaigns/{campaignID}/distribution-rules': A('Campanhas', 'Criou uma regra de distribuição', CATEGORY.create),
  'PUT /campaigns/{campaignID}/distribution-rules/{ruleID}': A('Campanhas', 'Editou uma regra de distribuição', CATEGORY.update),
  'DELETE /campaigns/{campaignID}/distribution-rules/{ruleID}': A('Campanhas', 'Excluiu uma regra de distribuição', CATEGORY.destructive),
  'GET /campaigns/{campaignID}/distribution-overrides': A('Campanhas', 'Consultou os overrides de distribuição', CATEGORY.navigate),
  'PUT /campaigns/{campaignID}/distribution-overrides': A('Campanhas', 'Ajustou um override de distribuição', CATEGORY.update),
  'DELETE /campaigns/{campaignID}/distribution-overrides': A('Campanhas', 'Removeu um override de distribuição', CATEGORY.destructive),
  'GET /campaigns/{campaignID}/pricing': A('Campanhas', 'Viu a precificação de uma campanha', CATEGORY.navigate),
  'PUT /campaigns/{campaignID}/pricing/{stationID}': A('Campanhas', 'Definiu o preço de uma emissora', CATEGORY.update),
  'DELETE /campaigns/{campaignID}/pricing/{stationID}': A('Campanhas', 'Removeu o preço de uma emissora', CATEGORY.destructive),

  // Detecções
  'GET /detections': A('Detecções', 'Consultou as detecções', CATEGORY.navigate),
  'GET /detections/aggregate-by-material': A('Detecções', 'Viu detecções agrupadas por material', CATEGORY.navigate),
  'GET /detections/{id}': A('Detecções', 'Abriu uma detecção', CATEGORY.navigate),
  'GET /detections/{id}/evidence': A('Detecções', 'Ouviu a evidência de uma detecção', CATEGORY.evidence),
  'GET /detections/{id}/evidence/url': A('Detecções', 'Abriu o áudio de uma evidência', CATEGORY.evidence),
  'GET /detections/{id}/proof/url': A('Detecções', 'Abriu um comprovante', CATEGORY.evidence),
  'POST /detections/manual': A('Detecções', 'Registrou uma veiculação manual', CATEGORY.create),
  'POST /detections/manual/batch': A('Detecções', 'Registrou veiculações manuais em lote', CATEGORY.create),
  'POST /detections/{id}/evidence': A('Detecções', 'Anexou evidência a uma detecção', CATEGORY.update),
  'POST /detections/{id}/ignore': A('Detecções', 'Desconsiderou uma veiculação', CATEGORY.destructive),
  'POST /detections/{id}/restore': A('Detecções', 'Restaurou uma veiculação', CATEGORY.update),
  'GET /detections/export': A('Detecções', 'Exportou detecções (CSV)', CATEGORY.export),

  // Clientes
  'GET /clients': A('Clientes', 'Abriu a lista de clientes', CATEGORY.navigate),
  'GET /clients/{clientID}/materials': A('Materiais', 'Viu os materiais de um cliente', CATEGORY.navigate),
  'POST /clients': A('Clientes', 'Criou um cliente', CATEGORY.create),
  'PUT /clients/{id}': A('Clientes', 'Editou um cliente', CATEGORY.update),
  'DELETE /clients/{id}': A('Clientes', 'Excluiu um cliente', CATEGORY.destructive),
  'POST /clients/{id}/deactivate': A('Clientes', 'Desativou um cliente', CATEGORY.destructive),
  'POST /clients/{id}/activate': A('Clientes', 'Reativou um cliente', CATEGORY.update),
  'GET /clients/{clientID}/api-keys': A('Clientes', 'Viu as API keys de um cliente', CATEGORY.navigate),
  'POST /clients/{clientID}/api-keys': A('Clientes', 'Gerou uma API key', CATEGORY.create),
  'DELETE /clients/{clientID}/api-keys/{keyID}': A('Clientes', 'Revogou uma API key', CATEGORY.destructive),
  'GET /clients/{id}/webhook': A('Clientes', 'Viu a config de webhook de um cliente', CATEGORY.navigate),
  'GET /clients/{id}/webhook-deliveries': A('Clientes', 'Viu as entregas de webhook', CATEGORY.navigate),
  'PATCH /clients/{id}/webhook': A('Clientes', 'Alterou a config de webhook', CATEGORY.update),
  'POST /clients/{id}/webhook-test': A('Clientes', 'Disparou um webhook de teste', CATEGORY.update),

  // Relatórios
  'GET /reports/campaigns/{id}/consolidated.csv': A('Relatórios', 'Exportou o relatório consolidado (CSV)', CATEGORY.export),
  'GET /reports/campaigns/{id}/summary': A('Relatórios', 'Gerou o relatório de uma campanha', CATEGORY.export),

  // Painéis de visão
  'GET /insights': A('Insights', 'Abriu o dashboard de veiculação', CATEGORY.navigate),
  'GET /live-map': A('Mapa ao Vivo', 'Abriu o mapa ao vivo', CATEGORY.navigate),
  'GET /management-overview': A('Gerencial', 'Abriu a visão gerencial', CATEGORY.navigate),

  // Emissoras & tipos de material
  'GET /stations': A('Emissoras', 'Abriu a lista de emissoras', CATEGORY.navigate),
  'GET /stations/{id}': A('Emissoras', 'Abriu uma emissora', CATEGORY.navigate),
  'GET /stations/{id}/threshold': A('Emissoras', 'Viu o threshold de uma emissora', CATEGORY.navigate),
  'POST /stations': A('Emissoras', 'Cadastrou uma emissora', CATEGORY.create),
  'PUT /stations/{id}': A('Emissoras', 'Editou uma emissora', CATEGORY.update),
  'PATCH /stations/{id}/stream-url': A('Emissoras', 'Trocou o stream de uma emissora', CATEGORY.update),
  'POST /stations/{id}/connection-test': A('Emissoras', 'Testou a conexão de uma emissora', CATEGORY.update),
  'GET /material-types': A('Materiais', 'Viu os tipos de material', CATEGORY.navigate),
  'POST /material-types': A('Materiais', 'Criou um tipo de material', CATEGORY.create),
  'PUT /material-types/{id}': A('Materiais', 'Editou um tipo de material', CATEGORY.update),
  'DELETE /material-types/{id}': A('Materiais', 'Excluiu um tipo de material', CATEGORY.destructive),

  // Comerciais (legado)
  'GET /commercials': A('Comerciais', 'Abriu a lista de comerciais', CATEGORY.navigate),
  'POST /commercials': A('Comerciais', 'Subiu um comercial', CATEGORY.create),
  'GET /commercials/{id}': A('Comerciais', 'Abriu um comercial', CATEGORY.navigate),
  'GET /commercials/{id}/audio': A('Comerciais', 'Ouviu um comercial', CATEGORY.evidence),
  'PUT /commercials/{id}/stations': A('Comerciais', 'Alterou as emissoras de um comercial', CATEGORY.update),
  'DELETE /commercials/{id}': A('Comerciais', 'Excluiu um comercial', CATEGORY.destructive),

  // Materiais (biblioteca)
  'POST /materials': A('Materiais', 'Subiu um material', CATEGORY.create),
  'GET /materials/{id}': A('Materiais', 'Abriu um material', CATEGORY.navigate),
  'GET /materials/{id}/audio': A('Materiais', 'Ouviu um material', CATEGORY.evidence),
  'POST /materials/{id}/similarity/acknowledge': A('Materiais', 'Reconheceu um alerta de duplicata', CATEGORY.update),
  'PATCH /materials/{id}/type': A('Materiais', 'Mudou o tipo de um material', CATEGORY.update),
  'PATCH /materials/{id}/script': A('Materiais', 'Editou o script de um material', CATEGORY.update),
  'DELETE /materials/{id}': A('Materiais', 'Excluiu um material', CATEGORY.destructive),

  // Saúde do stream & workers
  'GET /stream-health': A('Saúde do Stream', 'Abriu a saúde dos streams', CATEGORY.navigate),
  'GET /stream-health/{stationId}': A('Saúde do Stream', 'Abriu o detalhe de um stream', CATEGORY.navigate),
  'GET /workers': A('Workers', 'Consultou o status dos workers', CATEGORY.navigate),

  // Admin — rotinas operacionais
  'POST /admin/evidence/tiering/run': A('Admin', 'Rodou o tiering de evidência', CATEGORY.admin),
  'POST /admin/stations/{id}/threshold/refresh': A('Admin', 'Recalculou o threshold de uma emissora', CATEGORY.admin),
  'POST /admin/calibration/run': A('Admin', 'Rodou a calibração', CATEGORY.admin),
  'GET /admin/system-health': A('Admin', 'Abriu a saúde do sistema', CATEGORY.navigate),
  'GET /admin/station-failures': A('Admin', 'Abriu as falhas de emissora', CATEGORY.navigate),
  'GET /admin/campaign-failures': A('Admin', 'Abriu as falhas por campanha', CATEGORY.navigate),
  'GET /admin/campaign-failures/{id}': A('Admin', 'Abriu o detalhe de falhas de uma campanha', CATEGORY.navigate),
  'GET /admin/daily-failures-digest': A('Admin', 'Abriu o resumo diário de falhas', CATEGORY.navigate),
  'POST /admin/daily-failures-digest/ack': A('Admin', 'Confirmou o resumo diário de falhas', CATEGORY.update),

  // Notificações (sininho)
  'GET /admin/notifications': A('Notificações', 'Viu as notificações', CATEGORY.navigate),
  'POST /admin/notifications/mark-read': A('Notificações', 'Marcou notificações como lidas', CATEGORY.update),
  'POST /admin/notifications/mark-all-read': A('Notificações', 'Marcou todas as notificações como lidas', CATEGORY.update),

  // Monitoramento (esta tela)
  'GET /admin/monitoring/overview': A('Monitoramento', 'Esteve no monitoramento', CATEGORY.navigate),
  'GET /admin/monitoring/routes': A('Monitoramento', 'Esteve no monitoramento', CATEGORY.navigate),
  'GET /admin/monitoring/errors': A('Monitoramento', 'Esteve no monitoramento', CATEGORY.navigate),
  'GET /admin/monitoring/slow': A('Monitoramento', 'Esteve no monitoramento', CATEGORY.navigate),
  'GET /admin/monitoring/timeline': A('Monitoramento', 'Esteve no monitoramento', CATEGORY.navigate),
  'GET /admin/monitoring/vitals': A('Monitoramento', 'Esteve no monitoramento', CATEGORY.navigate),
  'GET /admin/monitoring/top-actors': A('Monitoramento', 'Esteve no monitoramento', CATEGORY.navigate),
  'GET /admin/monitoring/actor-detail': A('Monitoramento', 'Inspecionou a jornada de um usuário', CATEGORY.navigate),
  'GET /admin/monitoring/blocked-ips': A('Monitoramento', 'Esteve no monitoramento', CATEGORY.navigate),
  'POST /admin/monitoring/block-ip': A('Monitoramento', 'Bloqueou um IP', CATEGORY.destructive),
  'DELETE /admin/monitoring/block-ip/{ip}': A('Monitoramento', 'Desbloqueou um IP', CATEGORY.update),
  'POST /admin/monitoring/block-user/{userId}': A('Monitoramento', 'Bloqueou um usuário', CATEGORY.destructive),

  // Usuários (admin)
  'GET /admin/users': A('Usuários', 'Abriu a lista de usuários', CATEGORY.navigate),
  'POST /admin/users': A('Usuários', 'Criou um usuário', CATEGORY.create),
  'GET /admin/users/{id}': A('Usuários', 'Abriu um usuário', CATEGORY.navigate),
  'PATCH /admin/users/{id}': A('Usuários', 'Editou um usuário', CATEGORY.update),
  'DELETE /admin/users/{id}': A('Usuários', 'Excluiu um usuário', CATEGORY.destructive),
  'POST /admin/users/{id}/password': A('Usuários', 'Resetou a senha de um usuário', CATEGORY.update),
}

// Primeiro segmento da rota → área, para o fallback de rotas não mapeadas.
const AREA_FROM_SEG = {
  campaigns: 'Campanhas', detections: 'Detecções', materials: 'Materiais',
  clients: 'Clientes', stations: 'Emissoras', commercials: 'Comerciais',
  reports: 'Relatórios', insights: 'Insights', 'live-map': 'Mapa ao Vivo',
  'management-overview': 'Gerencial', 'stream-health': 'Saúde do Stream',
  workers: 'Workers', admin: 'Admin', auth: 'Sessão', 'material-types': 'Materiais',
}

function titleize(seg) {
  if (!seg) return ''
  return seg.split('-').map((w) => w.charAt(0).toUpperCase() + w.slice(1)).join(' ')
}

// Fallback gracioso: rota que não está no dicionário vira "verbo + área" a
// partir do método e do primeiro segmento. Nunca deve parecer quebrado.
function fallbackEvent(method, norm) {
  const segs = norm.split('/').filter((s) => s && !s.startsWith('{'))
  const first = segs[0] || ''
  const area = AREA_FROM_SEG[first] || titleize(first) || 'Plataforma'
  let action
  let category
  switch (method) {
    case 'POST': action = `Executou uma ação em ${area}`; category = CATEGORY.update; break
    case 'PUT':
    case 'PATCH': action = `Atualizou algo em ${area}`; category = CATEGORY.update; break
    case 'DELETE': action = `Removeu algo em ${area}`; category = CATEGORY.destructive; break
    default: action = `Acessou ${area}`; category = CATEGORY.navigate; break
  }
  return { area, action, category, fallback: true }
}

// describeEvent — traduz um request cru em {area, action, category}.
export function describeEvent(req) {
  const method = (req.method || 'GET').toUpperCase()
  const norm = normalizeRoute(req.route)
  const hit = EVENT_MAP[`${method} ${norm}`]
  if (hit) return { ...hit }
  return fallbackEvent(method, norm)
}

// severityOf — severidade visual da linha. Status manda; senão a categoria.
export function severityOf(category, status) {
  if (status >= 500) return 'error'
  if (status >= 400) return 'warn'
  if (category === CATEGORY.destructive) return 'destructive'
  return 'normal'
}

const SEV_RANK = { normal: 0, destructive: 1, warn: 2, error: 3 }
function worstSeverity(a, b) {
  return SEV_RANK[b] > SEV_RANK[a] ? b : a
}

// ── buildJourney ────────────────────────────────────────────────────────────
// requests: array vindo de actor-detail (route, method, statusCode, duration,
// timestamp, ip). Pode vir em qualquer ordem; reordenamos crescente.
//
// opts.gapMinutes: intervalo de inatividade que abre uma nova sessão (default 30).
// opts.capLimit: se requests.length atinge esse valor, marcamos `capped` (o
//   endpoint devolve no máximo 300 — o começo do período pode ter sido cortado).
export function buildJourney(requests, opts = {}) {
  const gapMs = (opts.gapMinutes ?? 30) * 60 * 1000
  const capLimit = opts.capLimit ?? 300

  const capped = Array.isArray(requests) && requests.length >= capLimit

  const events = (requests || [])
    .filter((r) => !isBackground(r))
    .map((r) => {
      const d = describeEvent(r)
      const t = new Date(r.timestamp).getTime()
      const status = r.statusCode ?? 0
      return {
        type: 'event',
        ts: r.timestamp,
        t: Number.isFinite(t) ? t : 0,
        area: d.area,
        action: d.action,
        category: d.category,
        severity: severityOf(d.category, status),
        status,
        method: (r.method || 'GET').toUpperCase(),
        route: normalizeRoute(r.route),
        durationMs: r.duration ?? null,
        ip: r.ip || null,
        fallback: !!d.fallback,
      }
    })
    .sort((a, b) => a.t - b.t)

  // Quebra em sessões por gap de inatividade.
  const sessions = []
  let cur = null
  for (const ev of events) {
    if (!cur || ev.t - cur.lastT > gapMs) {
      cur = { start: ev.ts, startT: ev.t, lastT: ev.t, raw: [] }
      sessions.push(cur)
    }
    cur.raw.push(ev)
    cur.lastT = ev.t
    cur.end = ev.ts
    cur.endT = ev.t
  }

  // Dentro de cada sessão, colapsa rajadas consecutivas da mesma ação (polling,
  // refetch) numa linha só, preservando a ordem cronológica.
  const outSessions = sessions.map((s, idx) => {
    const items = []
    for (const ev of s.raw) {
      const prev = items[items.length - 1]
      // Grupo e evento carregam ambos `area`/`action` — comparação direta.
      const sameAsPrev = prev && prev.action === ev.action && prev.area === ev.area
      if (sameAsPrev) {
        if (prev.type === 'event') {
          // promove o evento anterior a grupo
          const g = {
            type: 'group',
            area: prev.area,
            action: prev.action,
            category: prev.category,
            severity: prev.severity,
            start: prev.ts,
            startT: prev.t,
            end: ev.ts,
            endT: ev.t,
            count: 2,
            errorCount: (prev.status >= 400 ? 1 : 0) + (ev.status >= 400 ? 1 : 0),
            samples: [prev, ev],
          }
          items[items.length - 1] = g
        } else {
          prev.count += 1
          prev.end = ev.ts
          prev.endT = ev.t
          prev.severity = worstSeverity(prev.severity, ev.severity)
          if (ev.status >= 400) prev.errorCount += 1
          if (prev.samples.length < 60) prev.samples.push(ev)
        }
      } else {
        items.push(ev)
      }
    }

    const errorCount = s.raw.filter((e) => e.status >= 400).length
    return {
      id: `s${idx}`,
      start: s.start,
      startT: s.startT,
      end: s.end,
      endT: s.endT,
      durationMs: s.endT - s.startT,
      entryArea: s.raw[0]?.area ?? '—',
      entryAction: s.raw[0]?.action ?? '—',
      eventCount: s.raw.length,
      errorCount,
      items,
    }
  })

  // Resumo do usuário no período.
  const areaCounts = new Map()
  for (const ev of events) areaCounts.set(ev.area, (areaCounts.get(ev.area) || 0) + 1)
  const areas = [...areaCounts.entries()]
    .map(([area, count]) => ({ area, count }))
    .sort((a, b) => b.count - a.count)

  const summary = {
    firstSeen: events.length ? events[0].ts : null,
    lastSeen: events.length ? events[events.length - 1].ts : null,
    actionCount: events.length,
    sessionCount: outSessions.length,
    errorCount: events.filter((e) => e.status >= 500).length,
    warnCount: events.filter((e) => e.status >= 400 && e.status < 500).length,
    destructiveCount: events.filter((e) => e.category === CATEGORY.destructive).length,
    areas,
  }

  return { sessions: outSessions, summary, capped, totalEvents: events.length }
}
