// dates.js — datas de calendário sem o deslocamento de fuso. A API manda DATE do
// Postgres como "2026-05-14" ou "2026-05-14T00:00:00Z"; `new Date()` direto vira o
// dia anterior em São Paulo (o fuso dos testes é fixo em America/Sao_Paulo).
import { describe, test, expect, afterEach, vi } from 'vitest'
import {
  parseLocalDate, pad2, monthFromDate, isoFromDate, monthToRange, monthLabel, rangeLabel,
  defaultRangeForCampaign, campaignRangeISO, startOfLocalToday, enumerateVisibleDays, formatCampaignPeriod,
} from './dates.js'

afterEach(() => { vi.useRealTimers() })

const ymd = d => [d.getFullYear(), d.getMonth() + 1, d.getDate()]

describe('parseLocalDate', () => {
  test.each(['2026-05-14', '2026-05-14T00:00:00Z', '2026-05-14T23:59:59-03:00'])(
    '%s vira meia-noite LOCAL de 14/05/2026', (valor) => {
      const d = parseLocalDate(valor)
      expect(ymd(d)).toEqual([2026, 5, 14])
      expect([d.getHours(), d.getMinutes(), d.getSeconds(), d.getMilliseconds()]).toEqual([0, 0, 0, 0])
    })

  test('o new Date nativo erraria o dia (a razão de existir do helper)', () => {
    expect(new Date('2026-05-14T00:00:00Z').getDate()).toBe(13)
    expect(parseLocalDate('2026-05-14T00:00:00Z').getDate()).toBe(14)
  })

  test('instância de Date passa direto (mesma referência)', () => {
    const d = new Date(2026, 0, 5, 13, 30)
    expect(parseLocalDate(d)).toBe(d)
  })

  test.each([null, undefined, '', 'amanhã', '14/05/2026', '2026-5-14', {}])(
    'valor inválido (%j) devolve Date inválida pro chamador checar', (valor) => {
      expect(Number.isNaN(parseLocalDate(valor).getTime())).toBe(true)
    })
})

describe('formatadores de mês e dia', () => {
  test('pad2', () => {
    expect(pad2(3)).toBe('03')
    expect(pad2(12)).toBe('12')
    expect(pad2('7')).toBe('07')
  })

  test('monthFromDate e isoFromDate usam o calendário local', () => {
    const d = new Date(2026, 2, 9, 23, 59) // 09/03/2026 23:59 local (já é dia 10 em UTC)
    expect(monthFromDate(d)).toBe('2026-03')
    expect(isoFromDate(d)).toBe('2026-03-09')
  })

  test('monthToRange cobre o mês inteiro, inclusive fevereiro bissexto', () => {
    const { start, end } = monthToRange('2028-02')
    expect(ymd(start)).toEqual([2028, 2, 1])
    expect(ymd(end)).toEqual([2028, 2, 29])
    expect([end.getHours(), end.getMinutes(), end.getSeconds()]).toEqual([23, 59, 59])
    expect(ymd(monthToRange('2026-02').end)).toEqual([2026, 2, 28])
    expect(ymd(monthToRange('2026-12').end)).toEqual([2026, 12, 31])
  })

  test('monthLabel em português', () => {
    expect(monthLabel('2026-03')).toBe('março de 2026')
    expect(monthLabel('2026-12')).toBe('dezembro de 2026')
  })
})

describe('rangeLabel', () => {
  test('sem uma das pontas é vazio', () => {
    expect(rangeLabel('', '2026-03-05')).toBe('')
    expect(rangeLabel('2026-03-05', null)).toBe('')
  })
  test('mesmo dia', () => { expect(rangeLabel('2026-03-05', '2026-03-05')).toBe('5 de mar') })
  test('mesmo mês', () => { expect(rangeLabel('2026-03-05', '2026-03-09')).toBe('5–9 mar') })
  test('meses diferentes', () => { expect(rangeLabel('2026-02-28', '2026-03-02')).toBe('28 fev – 2 mar') })
  test('mesmo mês de anos diferentes não é "mesmo mês"', () => {
    expect(rangeLabel('2025-03-05', '2026-03-09')).toBe('5 mar – 9 mar')
  })
})

describe('defaultRangeForCampaign: mês ∩ vigência', () => {
  const camp = { start_date: '2026-03-10T00:00:00Z', end_date: '2026-04-20T00:00:00Z' }

  test('mês que começa antes da campanha: começa na campanha e vai até o fim do mês', () => {
    expect(defaultRangeForCampaign('2026-03', camp)).toEqual({ start: '2026-03-10', end: '2026-03-31' })
  })
  test('mês que termina antes do fim da campanha cobre o mês inteiro', () => {
    expect(defaultRangeForCampaign('2026-04', { ...camp, start_date: '2026-01-01' })).toEqual({ start: '2026-04-01', end: '2026-04-20' })
  })
  test('campanha dentro do mês', () => {
    expect(defaultRangeForCampaign('2026-03', { start_date: '2026-03-10', end_date: '2026-03-12' }))
      .toEqual({ start: '2026-03-10', end: '2026-03-12' })
  })
  test('sem interseção devolve vazio', () => {
    expect(defaultRangeForCampaign('2026-06', camp)).toEqual({ start: '', end: '' })
  })
  test('sem mês ou sem datas da campanha devolve vazio', () => {
    expect(defaultRangeForCampaign('', camp)).toEqual({ start: '', end: '' })
    expect(defaultRangeForCampaign('2026-03', null)).toEqual({ start: '', end: '' })
    expect(defaultRangeForCampaign('2026-03', { start_date: '2026-03-01' })).toEqual({ start: '', end: '' })
  })
})

describe('campaignRangeISO', () => {
  test('vigência inteira em YYYY-MM-DD, sem escorregar de dia', () => {
    expect(campaignRangeISO({ start_date: '2026-03-01T00:00:00Z', end_date: '2026-03-31T00:00:00Z' }))
      .toEqual({ start: '2026-03-01', end: '2026-03-31' })
  })
  test('sem datas ou com data inválida: vazio (limites livres no seletor)', () => {
    expect(campaignRangeISO(undefined)).toEqual({ start: '', end: '' })
    expect(campaignRangeISO({ start_date: '2026-03-01' })).toEqual({ start: '', end: '' })
    expect(campaignRangeISO({ start_date: 'x', end_date: 'y' })).toEqual({ start: '', end: '' })
  })
})

describe('startOfLocalToday', () => {
  test('meia-noite local do dia corrente', () => {
    vi.setSystemTime(new Date('2026-03-10T15:45:12Z')) // 12:45 em São Paulo
    const t = startOfLocalToday()
    expect(ymd(t)).toEqual([2026, 3, 10])
    expect([t.getHours(), t.getMinutes(), t.getSeconds(), t.getMilliseconds()]).toEqual([0, 0, 0, 0])
  })
})

describe('enumerateVisibleDays', () => {
  const dias = (lista) => lista.map(d => isoFromDate(d))
  const hoje = new Date(2026, 2, 20) // 20/03/2026

  test('span visível explícito (/detections): exatamente esse intervalo, podendo atravessar o mês', () => {
    const r = enumerateVisibleDays({
      month: new Date(2026, 2, 1), visibleStart: '2026-02-27', visibleEnd: '2026-03-02', today: hoje,
    })
    expect(dias(r)).toEqual(['2026-02-27', '2026-02-28', '2026-03-01', '2026-03-02'])
  })

  test('modo wizard: mês ∩ vigência da campanha', () => {
    const r = enumerateVisibleDays({
      month: new Date(2026, 2, 1), campaignStart: '2026-03-05', campaignEnd: '2026-03-08', today: hoje,
    })
    expect(dias(r)).toEqual(['2026-03-05', '2026-03-06', '2026-03-07', '2026-03-08'])
  })

  test('mês inteiro quando a campanha cobre o mês todo', () => {
    const r = enumerateVisibleDays({
      month: new Date(2026, 1, 10), campaignStart: '2026-01-01', campaignEnd: '2026-12-31',
      today: new Date(2026, 11, 31),
    })
    expect(r).toHaveLength(28)
    expect(dias(r)[0]).toBe('2026-02-01')
    expect(dias(r)[27]).toBe('2026-02-28')
  })

  test('nunca passa de hoje (dia futuro viraria déficit fantasma)', () => {
    const r = enumerateVisibleDays({
      month: new Date(2026, 2, 1), campaignStart: '2026-03-01', campaignEnd: '2026-03-31', today: hoje,
    })
    expect(dias(r).at(-1)).toBe('2026-03-20')
    expect(r).toHaveLength(20)
  })

  test('capAtToday=false enumera o futuro também', () => {
    const r = enumerateVisibleDays({
      month: new Date(2026, 2, 1), campaignStart: '2026-03-01', campaignEnd: '2026-03-31', today: hoje, capAtToday: false,
    })
    expect(r).toHaveLength(31)
  })

  test('sem "today" usa o relógio real', () => {
    vi.setSystemTime(new Date('2026-03-03T15:00:00Z'))
    const r = enumerateVisibleDays({ month: new Date(2026, 2, 1), campaignStart: '2026-03-01', campaignEnd: '2026-03-31' })
    expect(dias(r)).toEqual(['2026-03-01', '2026-03-02', '2026-03-03'])
  })

  test('campanha toda no futuro ou fora do mês: lista vazia', () => {
    expect(enumerateVisibleDays({
      month: new Date(2026, 2, 1), campaignStart: '2026-04-01', campaignEnd: '2026-04-30', today: hoje,
    })).toEqual([])
    expect(enumerateVisibleDays({
      month: new Date(2026, 2, 1), campaignStart: '2026-03-25', campaignEnd: '2026-03-31', today: hoje,
    })).toEqual([])
  })

  test('cada dia é meia-noite local e instâncias independentes', () => {
    const r = enumerateVisibleDays({ month: new Date(2026, 2, 1), visibleStart: '2026-03-01', visibleEnd: '2026-03-02', today: hoje })
    expect(r.every(d => d.getHours() === 0)).toBe(true)
    r[0].setDate(15)
    expect(r[1].getDate()).toBe(2)
  })
})

describe('formatCampaignPeriod', () => {
  test('dd-mm-aaaa – dd-mm-aaaa', () => {
    expect(formatCampaignPeriod('2026-03-01T00:00:00Z', '2026-03-31')).toBe('01-03-2026 – 31-03-2026')
  })
  test('ponta ausente ou data inválida: vazio', () => {
    expect(formatCampaignPeriod('', '2026-03-31')).toBe('')
    expect(formatCampaignPeriod('2026-03-01', null)).toBe('')
    expect(formatCampaignPeriod('abc', '2026-03-31')).toBe('')
  })
})
