// userJourney.js — traduz o fluxo cru de requests de um usuário (monitoramento) numa
// jornada legível: ação em português, sessões por inatividade, rajadas colapsadas.
import { describe, test, expect } from 'vitest'
import {
  CATEGORY, normalizeRoute, isBackground, describeEvent, severityOf, buildJourney,
} from './userJourney.js'

// instante base + minutos, em ISO UTC
const T0 = Date.UTC(2026, 2, 10, 12, 0, 0)
const em = (min, seg = 0) => new Date(T0 + min * 60_000 + seg * 1000).toISOString()
const req = (method, route, min, extra = {}) => ({
  method, route, timestamp: em(min), statusCode: 200, duration: 12, ip: 'ip-teste-1', ...extra,
})

describe('normalizeRoute', () => {
  test.each([
    ['/v1/internal/campaigns/{id}', '/campaigns/{id}'],
    ['/v1/internal/commercials/', '/commercials'], // barra final do sub-router
    ['/v1/campaigns', '/campaigns'],
    ['/v1/internal', '/'],
    ['/v1/internal/campaigns?page=2', '/campaigns'],
    ['/campaigns/', '/campaigns'],
    ['/', '/'],
  ])('%s -> %s', (entrada, esperado) => {
    expect(normalizeRoute(entrada)).toBe(esperado)
  })

  test.each([null, undefined, ''])('vazio (%j) -> string vazia', (v) => {
    expect(normalizeRoute(v)).toBe('')
  })
})

describe('isBackground', () => {
  test('rotas que o SPA dispara sozinho não são ação do usuário', () => {
    expect(isBackground({ method: 'GET', route: '/v1/internal/auth/me' })).toBe(true)
    expect(isBackground({ method: 'POST', route: '/v1/internal/web-vitals' })).toBe(true)
    expect(isBackground({ method: 'GET', route: '/v1/internal/health' })).toBe(true)
  })
  test('o método importa, e sem método vale GET', () => {
    expect(isBackground({ method: 'PATCH', route: '/v1/internal/auth/me' })).toBe(false)
    expect(isBackground({ route: '/v1/internal/auth/me' })).toBe(true)
    expect(isBackground({ method: 'get', route: '/auth/me' })).toBe(true)
  })
})

describe('describeEvent', () => {
  test('rota do dicionário vira área + ação + categoria em português', () => {
    expect(describeEvent({ method: 'POST', route: '/v1/internal/campaigns' })).toEqual({
      area: 'Campanhas', action: 'Criou uma campanha', category: CATEGORY.create,
    })
    expect(describeEvent({ method: 'DELETE', route: '/v1/internal/clients/{id}' })).toEqual({
      area: 'Clientes', action: 'Excluiu um cliente', category: CATEGORY.destructive,
    })
    expect(describeEvent({ method: 'GET', route: '/v1/internal/detections/{id}/evidence' }).category).toBe(CATEGORY.evidence)
    expect(describeEvent({ method: 'GET', route: '/v1/internal/detections/export' }).category).toBe(CATEGORY.export)
  })

  test('método em minúsculas e rota com barra final e query ainda casam', () => {
    expect(describeEvent({ method: 'get', route: '/v1/internal/commercials/?q=a' })).toEqual({
      area: 'Comerciais', action: 'Abriu a lista de comerciais', category: CATEGORY.navigate,
    })
  })

  test('retorna cópia: mexer no resultado não suja o dicionário', () => {
    const a = describeEvent({ method: 'POST', route: '/auth/login' })
    a.action = 'alterado'
    expect(describeEvent({ method: 'POST', route: '/auth/login' }).action).toBe('Entrou na plataforma')
  })

  test.each([
    ['POST', '/v1/internal/campaigns/{id}/novidade', 'Campanhas', 'Executou uma ação em Campanhas', CATEGORY.update],
    ['PUT', '/v1/internal/stations/{id}/algo', 'Emissoras', 'Atualizou algo em Emissoras', CATEGORY.update],
    ['PATCH', '/v1/internal/materials/{id}/foo', 'Materiais', 'Atualizou algo em Materiais', CATEGORY.update],
    ['DELETE', '/v1/internal/reports/x', 'Relatórios', 'Removeu algo em Relatórios', CATEGORY.destructive],
    ['GET', '/v1/internal/live-map/extra', 'Mapa ao Vivo', 'Acessou Mapa ao Vivo', CATEGORY.navigate],
    ['GET', '/v1/internal/stream-health/x/y', 'Saúde do Stream', 'Acessou Saúde do Stream', CATEGORY.navigate],
  ])('rota fora do dicionário (%s %s) cai num fallback legível', (method, route, area, action, category) => {
    expect(describeEvent({ method, route })).toEqual({ area, action, category, fallback: true })
  })

  test('primeiro segmento desconhecido vira título; sem segmento vira "Plataforma"', () => {
    expect(describeEvent({ method: 'GET', route: '/v1/internal/novo-recurso-teste' }))
      .toMatchObject({ area: 'Novo Recurso Teste', action: 'Acessou Novo Recurso Teste', fallback: true })
    expect(describeEvent({ method: 'GET', route: '/v1/internal' }))
      .toMatchObject({ area: 'Plataforma', action: 'Acessou Plataforma' })
    expect(describeEvent({ method: 'GET', route: '' })).toMatchObject({ area: 'Plataforma' })
  })

  test('segmentos de parâmetro ({id}) são pulados até o primeiro literal', () => {
    expect(describeEvent({ method: 'GET', route: '/{id}/live-map' })).toMatchObject({ area: 'Mapa ao Vivo' })
    expect(describeEvent({ method: 'GET', route: '/{id}' })).toMatchObject({ area: 'Plataforma' })
  })
})

describe('severityOf', () => {
  test('o status manda: 5xx erro, 4xx aviso', () => {
    expect(severityOf(CATEGORY.navigate, 500)).toBe('error')
    expect(severityOf(CATEGORY.destructive, 503)).toBe('error')
    expect(severityOf(CATEGORY.navigate, 404)).toBe('warn')
    expect(severityOf(CATEGORY.destructive, 409)).toBe('warn')
  })
  test('sem erro, destrutivo se destaca e o resto é normal', () => {
    expect(severityOf(CATEGORY.destructive, 200)).toBe('destructive')
    expect(severityOf(CATEGORY.update, 200)).toBe('normal')
    expect(severityOf(CATEGORY.navigate, 0)).toBe('normal')
  })
})

describe('buildJourney', () => {
  test('sem requests: jornada vazia', () => {
    expect(buildJourney([])).toMatchObject({
      sessions: [], capped: false, totalEvents: 0,
      summary: { firstSeen: null, lastSeen: null, actionCount: 0, sessionCount: 0, errorCount: 0, warnCount: 0, destructiveCount: 0, areas: [] },
    })
    expect(buildJourney(undefined).totalEvents).toBe(0)
  })

  test('ordena cronologicamente e descarta o ruído de fundo', () => {
    const j = buildJourney([
      req('GET', '/v1/internal/campaigns', 5),
      req('GET', '/v1/internal/auth/me', 1),
      req('POST', '/v1/internal/web-vitals', 2),
      req('POST', '/v1/internal/auth/login', 0),
    ])
    expect(j.totalEvents).toBe(2)
    const itens = j.sessions[0].items
    expect(itens.map(i => i.action)).toEqual(['Entrou na plataforma', 'Abriu a lista de campanhas'])
    expect(j.summary.firstSeen).toBe(em(0))
    expect(j.summary.lastSeen).toBe(em(5))
  })

  test('campos do evento: severidade, método, rota normalizada, duração e IP', () => {
    const j = buildJourney([req('delete', '/v1/internal/campaigns/{id}', 0, { statusCode: 200, duration: 33, ip: 'ip-teste-2' })])
    expect(j.sessions[0].items[0]).toMatchObject({
      type: 'event', area: 'Campanhas', action: 'Excluiu uma campanha', category: CATEGORY.destructive,
      severity: 'destructive', status: 200, method: 'DELETE', route: '/campaigns/{id}', durationMs: 33, ip: 'ip-teste-2', fallback: false,
    })
  })

  test('campos opcionais ausentes: duração e IP nulos, status 0, timestamp inválido vira t=0', () => {
    const j = buildJourney([{ method: 'GET', route: '/v1/internal/campaigns', timestamp: 'quebrado' }])
    expect(j.sessions[0].items[0]).toMatchObject({ status: 0, durationMs: null, ip: null, t: 0 })
  })

  test('rota fora do dicionário marca fallback', () => {
    const j = buildJourney([req('GET', '/v1/internal/inexistente', 0)])
    expect(j.sessions[0].items[0].fallback).toBe(true)
  })

  test('inatividade acima de 30 min abre nova sessão (padrão) com entrada e duração', () => {
    const j = buildJourney([
      req('POST', '/v1/internal/auth/login', 0),
      req('GET', '/v1/internal/campaigns', 10),
      req('GET', '/v1/internal/clients', 41), // 31 min depois do anterior
      req('GET', '/v1/internal/insights', 50),
    ])
    expect(j.sessions).toHaveLength(2)
    expect(j.sessions[0]).toMatchObject({
      id: 's0', start: em(0), end: em(10), durationMs: 10 * 60_000, entryArea: 'Sessão', entryAction: 'Entrou na plataforma', eventCount: 2,
    })
    expect(j.sessions[1]).toMatchObject({
      id: 's1', start: em(41), end: em(50), durationMs: 9 * 60_000, entryArea: 'Clientes', eventCount: 2,
    })
    expect(j.summary.sessionCount).toBe(2)
  })

  test('exatamente 30 min ainda é a mesma sessão; o gap é configurável', () => {
    const reqs = [req('GET', '/v1/internal/campaigns', 0), req('GET', '/v1/internal/clients', 30)]
    expect(buildJourney(reqs).sessions).toHaveLength(1)
    expect(buildJourney(reqs, { gapMinutes: 10 }).sessions).toHaveLength(2)
  })

  test('rajada da mesma ação (polling) vira um grupo com contagem, período e amostras', () => {
    const j = buildJourney([
      req('GET', '/v1/internal/workers', 0),
      req('GET', '/v1/internal/workers', 1),
      req('GET', '/v1/internal/workers', 2, { statusCode: 500 }),
      req('GET', '/v1/internal/campaigns', 3),
    ])
    const [grupo, solto] = j.sessions[0].items
    expect(grupo).toMatchObject({
      type: 'group', area: 'Workers', action: 'Consultou o status dos workers',
      count: 3, errorCount: 1, start: em(0), end: em(2), severity: 'error',
    })
    expect(grupo.samples).toHaveLength(3)
    expect(solto).toMatchObject({ type: 'event', action: 'Abriu a lista de campanhas' })
  })

  test('dois eventos viram grupo de 2 e contam erro 4xx; severidade do grupo é a pior', () => {
    const j = buildJourney([
      req('GET', '/v1/internal/campaigns/{id}', 0, { statusCode: 404 }),
      req('GET', '/v1/internal/campaigns/{id}', 1),
    ])
    const [g] = j.sessions[0].items
    expect(g).toMatchObject({ type: 'group', count: 2, errorCount: 1, severity: 'warn' })
  })

  test('ações iguais separadas por outra ação não colapsam', () => {
    const j = buildJourney([
      req('GET', '/v1/internal/campaigns', 0),
      req('GET', '/v1/internal/clients', 1),
      req('GET', '/v1/internal/campaigns', 2),
    ])
    expect(j.sessions[0].items.map(i => i.type)).toEqual(['event', 'event', 'event'])
  })

  test('rajadas não atravessam sessões', () => {
    const j = buildJourney([
      req('GET', '/v1/internal/workers', 0),
      req('GET', '/v1/internal/workers', 120),
    ])
    expect(j.sessions).toHaveLength(2)
    expect(j.sessions.every(s => s.items.length === 1 && s.items[0].type === 'event')).toBe(true)
  })

  test('grupo guarda no máximo 60 amostras, mas conta todas', () => {
    const reqs = Array.from({ length: 80 }, (_, i) => req('GET', '/v1/internal/workers', 0, { timestamp: em(0, i) }))
    const [g] = buildJourney(reqs).sessions[0].items
    expect(g.count).toBe(80)
    expect(g.samples).toHaveLength(60)
  })

  test('marca "capped" quando o endpoint bateu o teto (começo do período pode ter sido cortado)', () => {
    const reqs = Array.from({ length: 300 }, (_, i) => req('GET', '/v1/internal/campaigns', 0, { timestamp: em(0, i) }))
    expect(buildJourney(reqs).capped).toBe(true)
    expect(buildJourney(reqs.slice(0, 299)).capped).toBe(false)
    expect(buildJourney(reqs.slice(0, 5), { capLimit: 5 }).capped).toBe(true)
  })

  test('resumo: contagens por tipo de problema e áreas ordenadas por uso', () => {
    const j = buildJourney([
      req('GET', '/v1/internal/campaigns', 0),
      req('GET', '/v1/internal/clients', 1),
      req('GET', '/v1/internal/campaigns', 2, { statusCode: 500 }),
      req('DELETE', '/v1/internal/campaigns/{id}', 3),
      req('GET', '/v1/internal/campaigns/{id}', 4, { statusCode: 404 }),
    ])
    expect(j.summary).toMatchObject({
      actionCount: 5, sessionCount: 1, errorCount: 1, warnCount: 1, destructiveCount: 1,
    })
    expect(j.summary.areas).toEqual([{ area: 'Campanhas', count: 4 }, { area: 'Clientes', count: 1 }])
    // erros da sessão contam 4xx e 5xx
    expect(j.sessions[0].errorCount).toBe(2)
  })

  test('requests chegando fora de ordem não alteram o resultado', () => {
    const reqs = [req('GET', '/v1/internal/campaigns', 2), req('GET', '/v1/internal/clients', 0), req('GET', '/v1/internal/insights', 1)]
    const a = buildJourney(reqs)
    const b = buildJourney([...reqs].reverse())
    expect(a.sessions[0].items.map(i => i.action)).toEqual(b.sessions[0].items.map(i => i.action))
    expect(a.sessions[0].items.map(i => i.action)).toEqual([
      'Abriu a lista de clientes', 'Abriu o dashboard de veiculação', 'Abriu a lista de campanhas',
    ])
  })
})
