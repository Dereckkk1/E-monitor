import { useMemo, useState, useEffect, useCallback } from 'react'
import {
  BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip,
  ResponsiveContainer, Cell, ReferenceLine,
} from 'recharts'
import { useFailuresDaily } from '../api/hooks'
import './FailuresDailyView.css'

/* ── Paleta ────────────────────────────────────────────────────────────────
 * Uma série por vez (a métrica é escolhida no filtro), então UMA cor pra todas
 * as barras — nada de barra mais escura onde é maior, que gastaria o canal de
 * cor repetindo a informação que a altura já dá. O único desvio é o pior dia,
 * que ganha o passo mais escuro do MESMO tom + rótulo direto: isso é ênfase,
 * não escala de valor.
 *
 * Par validado (scripts/validate_palette.js, surface #fff): ΔE 18.7 deutan /
 * 19.2 normal, ambos ≥ 3:1 de contraste. Não troque sem revalidar.
 * ------------------------------------------------------------------------ */
const BAR = '#ec4899'
const BAR_PEAK = '#a4114f'
const GRID = '#e2e8f0'
const AXIS_INK = '#6b7280'

const fmtBR = new Intl.NumberFormat('pt-BR')

const WEEKDAYS = ['dom', 'seg', 'ter', 'qua', 'qui', 'sex', 'sáb']
const WEEKDAY_LONG = ['domingo', 'segunda', 'terça', 'quarta', 'quinta', 'sexta', 'sábado']
// Semana começa na segunda: o padrão que interessa é "toda segunda cai", e
// domingo/sábado juntos no fim deixam o fim de semana legível como bloco.
const WEEKDAY_ORDER = [1, 2, 3, 4, 5, 6, 0]

function pad2(n) { return String(n).padStart(2, '0') }
function iso(d) { return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}` }

// Parse local — new Date('2026-07-01') seria UTC e renderiza 30/06 em offset
// negativo, jogando o mês inteiro um dia pra trás no eixo X.
function parseLocal(s) {
  const [y, m, d] = s.split('-').map(Number)
  return new Date(y, m - 1, d)
}

function fmtDuration(sec) {
  if (!sec) return '0min'
  if (sec < 60) return `${sec}s`
  return fmtMinutes(Math.floor(sec / 60))
}

function fmtMinutes(min) {
  if (!min) return '0min'
  if (min < 60) return `${min}min`
  const h = Math.floor(min / 60)
  const rem = min - h * 60
  return rem > 0 ? `${h}h${pad2(rem)}` : `${h}h`
}

/* ── Métricas ─────────────────────────────────────────────────────────────
 * As três respondem perguntas diferentes sobre o mesmo dia: quantas rádios
 * quebraram (operação), quanto isso custou em inserção (comercial) e quanto
 * tempo de ar se perdeu (infra).
 * ------------------------------------------------------------------------ */
const countFmt = {
  fmt: v => fmtBR.format(Math.round(v)),
  // Média de contagem merece uma casa: "3,5 emissoras/dia" diz mais que "4".
  fmtAvg: v => fmtBR.format(Math.round(v * 10) / 10),
  axisFmt: v => fmtBR.format(v),
}

const METRICS = [
  {
    id: 'stations',
    toValue: d => d.stations || 0,
    tab: 'Emissoras',
    title: 'Emissoras com falha por dia',
    unit: 'emissoras',
    hint: 'Emissoras distintas que perderam pelo menos uma inserção programada no dia.',
    ...countFmt,
  },
  {
    id: 'deficit',
    toValue: d => d.deficit || 0,
    tab: 'Veiculações perdidas',
    title: 'Veiculações perdidas por dia',
    unit: 'veiculações',
    hint: 'Soma do déficit do dia — o que foi programado e não foi ao ar.',
    ...countFmt,
  },
  {
    // Plotado em MINUTOS, não segundos. Com segundos o recharts escolhe ticks
    // como 2000/4000/6000/8000 e o formatador arredondava 6000 e 8000 pro
    // MESMO "2h" — o eixo repetia rótulo e mentia. Em minutos os ticks caem
    // redondos (0/25/50/75/100) e cada um vira um rótulo distinto.
    id: 'down',
    toValue: d => Math.round((d.down_seconds || 0) / 60),
    tab: 'Tempo fora do ar',
    title: 'Tempo fora do ar por dia',
    unit: 'fora do ar',
    hint: 'Soma do downtime das emissoras que perderam inserção no dia. Queda sem campanha agendada não entra — igual ao resto da página.',
    fmt: v => fmtMinutes(Math.round(v)),
    fmtAvg: v => fmtMinutes(Math.round(v)),
    axisFmt: v => fmtMinutes(Math.round(v)),
  },
]

/* ── Presets de período ───────────────────────────────────────────────────
 * O backend recusa `from` com mais de 90 dias (mesmo teto do /admin/station-
 * failures), então todo preset cabe dentro da janela — nenhum botão da UI
 * pode produzir um 400.
 * ------------------------------------------------------------------------ */
function buildPresets(today) {
  const y = today.getFullYear()
  const m = today.getMonth()
  const firstThis = new Date(y, m, 1)
  const firstPrev = new Date(y, m - 1, 1)
  const lastPrev = new Date(y, m, 0)
  // `label` é o texto do botão; `phrase` é a forma preposicionada usada em
  // frase corrida ("Nenhuma falha NESTE MÊS", não "em este mês").
  return [
    { id: 'this-month', label: 'Este mês',    phrase: 'neste mês',              from: iso(firstThis), to: iso(today) },
    { id: 'last-month', label: 'Mês passado', phrase: 'no mês passado',         from: iso(firstPrev), to: iso(lastPrev) },
    { id: 'last-30',    label: '30 dias',     phrase: 'nos últimos 30 dias',    from: iso(new Date(y, m, today.getDate() - 29)), to: iso(today) },
    { id: 'last-90',    label: '90 dias',     phrase: 'nos últimos 90 dias',    from: iso(new Date(y, m, today.getDate() - 89)), to: iso(today) },
  ]
}

// Largura real do elemento. O nº de rótulos do eixo X tem que sair da LARGURA
// disponível, não da contagem de pontos: 31 datas com um rótulo a cada 3 é
// confortável em 1300px e vira uma tarja ilegível em 320px.
function useElementWidth() {
  const [width, setWidth] = useState(0)
  const [node, setNode] = useState(null)
  useEffect(() => {
    if (!node || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(([entry]) => {
      setWidth(entry.contentRect.width)
    })
    ro.observe(node)
    return () => ro.disconnect()
  }, [node])
  return [setNode, width]
}

// Espaço mínimo por rótulo de data no eixo X. "01/07" mede ~34px; o resto é a
// folga que impede dois rótulos de encostarem.
const MIN_PX_PER_TICK = 92

function tickInterval(count, width) {
  if (!count) return 0
  if (!width) return count > 24 ? 2 : 0 // antes do primeiro measure
  const maxTicks = Math.max(2, Math.floor(width / MIN_PX_PER_TICK))
  return Math.max(0, Math.ceil(count / maxTicks) - 1)
}

function usePrefersReducedMotion() {
  const [reduced, setReduced] = useState(
    () => typeof window !== 'undefined'
      && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
  )
  useEffect(() => {
    const mq = window.matchMedia?.('(prefers-reduced-motion: reduce)')
    if (!mq) return
    const on = e => setReduced(e.matches)
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [])
  return reduced
}

/* ── Agregações do painel de padrão ───────────────────────────────────────
 * Os dois painéis comparam grupos com quantidades DIFERENTES de dias (o 3º
 * decêndio tem 11; "30 dias" pode cair com 20 dias num decêndio e 10 noutro).
 * Por isso a barra carrega a MÉDIA por dia, não o total — comparar somas de
 * grupos desiguais diria que o 3º decêndio é sempre o pior.
 * ------------------------------------------------------------------------ */
function groupStats(days, toValue, keyOf, buckets) {
  const acc = new Map(buckets.map(b => [b.key, { ...b, total: 0, days: 0 }]))
  for (const d of days) {
    const k = keyOf(parseLocal(d.date))
    const slot = acc.get(k)
    if (!slot) continue
    slot.total += toValue(d)
    slot.days += 1
  }
  const out = [...acc.values()].map(s => ({
    ...s,
    avg: s.days > 0 ? s.total / s.days : 0,
  }))
  const max = Math.max(...out.map(s => s.avg), 0)
  return out.map(s => ({ ...s, pct: max > 0 ? (s.avg / max) * 100 : 0, isMax: max > 0 && s.avg === max }))
}

/* Frase-veredito: diz em palavras o que as barras mostram. Só aponta um
 * campeão quando a separação é grande o bastante pra não ser ruído — abaixo
 * de 1,25× a diferença é oscilação, e cravar "as segundas são o problema" em
 * cima disso mandaria alguém investigar um padrão que não existe. */
const VERDICT_MIN_RATIO = 1.25

function verdictOf(rows) {
  const ranked = [...rows].filter(r => r.days > 0).sort((a, b) => b.avg - a.avg)
  if (ranked.length < 2 || ranked[0].avg <= 0) return null
  const [top, ...rest] = ranked
  const others = rest.reduce((s, r) => s + r.avg, 0) / rest.length
  // Todos os outros zerados: o topo é o único com falha, e isso é destaque
  // suficiente pra afirmar — mas sem razão calculável (divisão por zero).
  if (others <= 0) return { flat: false, top, ratio: null }
  const ratio = top.avg / others
  if (ratio < VERDICT_MIN_RATIO) return { flat: true }
  return { flat: false, top, ratio }
}

// "2,4" em pt-BR; sem razão calculável some com o "×" na frase.
function fmtRatio(ratio) {
  return ratio == null ? '' : fmtBR.format(Math.round(ratio * 10) / 10)
}

const DECADE_BUCKETS = [
  { key: 0, label: 'Dias 1–10' },
  { key: 1, label: 'Dias 11–20' },
  { key: 2, label: 'Dias 21–fim' },
]
const decadeOf = d => (d.getDate() <= 10 ? 0 : d.getDate() <= 20 ? 1 : 2)

const WEEKDAY_BUCKETS = WEEKDAY_ORDER.map(i => ({ key: i, label: WEEKDAYS[i], long: WEEKDAY_LONG[i] }))

/* ── Barras horizontais do painel ─────────────────────────────────────────
 * CSS puro em vez de um segundo recharts: são 3 e 7 barras, e o track fixo
 * mantém as etiquetas alinhadas de um painel pro outro.
 * ------------------------------------------------------------------------ */
function PatternBars({ rows, metric, caption, describeRow }) {
  return (
    <ul className="fd-pattern-list">
      {rows.map(r => (
        <li key={r.key} className={`fd-pattern-row${r.isMax ? ' fd-pattern-row--max' : ''}`}>
          <span className="fd-pattern-label">{r.label}</span>
          <span className="fd-pattern-track">
            <span className="fd-pattern-fill" style={{ width: `${r.pct}%` }} />
          </span>
          <span className="fd-pattern-value">{metric.fmtAvg(r.avg)}</span>
          <span className="fd-sr-only">{describeRow(r)}</span>
        </li>
      ))}
      <li className="fd-pattern-caption">{caption}</li>
    </ul>
  )
}

function ChartTooltip({ active, payload, metric }) {
  if (!active || !payload?.length) return null
  const d = payload[0].payload
  const date = parseLocal(d.date)
  return (
    <div className="fd-tooltip">
      <div className="fd-tooltip-head">
        {WEEKDAY_LONG[date.getDay()]}, {pad2(date.getDate())}/{pad2(date.getMonth() + 1)}
      </div>
      <dl className="fd-tooltip-rows">
        <div className={metric.id === 'stations' ? 'fd-tooltip-row fd-tooltip-row--on' : 'fd-tooltip-row'}>
          <dt>Emissoras</dt><dd>{fmtBR.format(d.stations)}</dd>
        </div>
        <div className={metric.id === 'deficit' ? 'fd-tooltip-row fd-tooltip-row--on' : 'fd-tooltip-row'}>
          <dt>Veiculações perdidas</dt><dd>{fmtBR.format(d.deficit)}</dd>
        </div>
        <div className={metric.id === 'down' ? 'fd-tooltip-row fd-tooltip-row--on' : 'fd-tooltip-row'}>
          <dt>Tempo fora</dt><dd>{fmtDuration(d.down_seconds)}</dd>
        </div>
      </dl>
      {d.stations > 0 && <div className="fd-tooltip-foot">Clique pra abrir o dia</div>}
    </div>
  )
}

function DailySkeleton() {
  return (
    <div className="fd-skel" aria-hidden="true">
      <div className="fd-skel-kpis">
        {[0, 1, 2, 3].map(i => <div key={i} className="fd-skel-kpi" />)}
      </div>
      <div className="fd-skel-chart">
        {Array.from({ length: 28 }).map((_, i) => (
          <span
            key={i}
            className="fd-skel-bar"
            /* Alturas fixas, não aleatórias: um skeleton que muda de forma a
               cada render pisca em vez de segurar o layout. */
            style={{ height: `${18 + ((i * 37) % 62)}%` }}
          />
        ))}
      </div>
      <div className="fd-skel-panels">
        <div className="fd-skel-panel" />
        <div className="fd-skel-panel" />
      </div>
    </div>
  )
}

function DailyEmptyState({ phrase }) {
  return (
    <div className="fd-empty">
      <div className="fd-empty-text">
        <div className="fd-empty-mark" aria-hidden="true">
          <svg width="44" height="44" viewBox="0 0 44 44" fill="none">
            <circle cx="22" cy="22" r="20" stroke="currentColor" strokeWidth="1.5" opacity=".25" />
            <path d="M14 22.5l5.5 5.5L31 16" stroke="currentColor" strokeWidth="2"
                  strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </div>
        <h2>Nenhuma falha {phrase}.</h2>
        <p>
          Todo dia do período entregou o que estava programado. Quando algum dia falhar,
          ele vira uma barra aqui — e o painel abaixo aponta se o problema se concentra
          em alguma parte do mês ou num dia da semana.
        </p>
      </div>
      <div className="fd-empty-preview" aria-hidden="true">
        <div className="fd-empty-preview-label">Com falhas, o período aparece assim:</div>
        <div className="fd-empty-chart">
          {[22, 8, 40, 15, 62, 30, 12, 48, 88, 35, 20, 55, 28, 10, 44].map((h, i) => (
            <span key={i} className={`fd-empty-bar${h === 88 ? ' fd-empty-bar--peak' : ''}`}
                  style={{ height: `${h}%` }} />
          ))}
        </div>
        <div className="fd-empty-legend">
          <span className="fd-empty-dot fd-empty-dot--peak" /> pior dia do período
        </div>
      </div>
    </div>
  )
}

export default function FailuresDailyView({ minDownSeconds = 60, onPickDay }) {
  const today = useMemo(() => {
    const n = new Date()
    return new Date(n.getFullYear(), n.getMonth(), n.getDate())
  }, [])
  const presets = useMemo(() => buildPresets(today), [today])

  const [presetId, setPresetId] = useState('this-month')
  const [range, setRange] = useState(() => {
    const p = buildPresets(today).find(x => x.id === 'this-month')
    return { from: p.from, to: p.to }
  })
  const [metricId, setMetricId] = useState('stations')
  const [showTable, setShowTable] = useState(false)
  const reducedMotion = usePrefersReducedMotion()
  const [chartRef, chartWidth] = useElementWidth()

  const metric = METRICS.find(m => m.id === metricId) ?? METRICS[0]

  const applyPreset = useCallback(id => {
    const p = presets.find(x => x.id === id)
    if (!p) return
    setPresetId(id)
    setRange({ from: p.from, to: p.to })
  }, [presets])

  // Range manual: o input não pode gerar um pedido que o backend recusa, então
  // o `min` do campo trava em hoje-90d e o `max` em hoje.
  const minDate = useMemo(() => iso(new Date(today.getFullYear(), today.getMonth(), today.getDate() - 90)), [today])
  const maxDate = useMemo(() => iso(today), [today])

  const setCustom = (which, value) => {
    if (!value) return
    setPresetId('custom')
    setRange(prev => {
      const next = { ...prev, [which]: value }
      // `from` depois de `to` seria 400 — arrasta a outra ponta junto em vez de
      // deixar o usuário num estado inválido.
      if (next.from > next.to) {
        if (which === 'from') next.to = next.from
        else next.from = next.to
      }
      return next
    })
  }

  const { data, isLoading, isFetching, error } = useFailuresDaily({
    from: range.from, to: range.to, minDownSeconds,
  })

  // Referência estável: `data?.days ?? []` cria um array novo a cada render e
  // invalida todos os useMemo abaixo, refazendo as agregações à toa.
  const days = useMemo(() => data?.days ?? [], [data])
  const summary = data?.summary

  const chartRows = useMemo(() => days.map(d => ({
    ...d,
    value: metric.toValue(d),
    label: (() => {
      const p = parseLocal(d.date)
      return `${pad2(p.getDate())}/${pad2(p.getMonth() + 1)}`
    })(),
  })), [days, metric])

  const maxValue = useMemo(
    () => chartRows.reduce((m, r) => Math.max(m, r.value), 0),
    [chartRows]
  )
  const avgValue = useMemo(
    () => (chartRows.length ? chartRows.reduce((s, r) => s + r.value, 0) / chartRows.length : 0),
    [chartRows]
  )
  const totalValue = useMemo(
    () => chartRows.reduce((s, r) => s + r.value, 0),
    [chartRows]
  )
  // Primeiro dia com o valor máximo — desempate pelo mais antigo, igual ao
  // PeakDate do backend, pra que o KPI e a barra destacada nunca discordem.
  const peakDate = useMemo(() => {
    if (maxValue <= 0) return null
    return chartRows.find(r => r.value === maxValue)?.date ?? null
  }, [chartRows, maxValue])

  const decades = useMemo(
    () => groupStats(days, metric.toValue, decadeOf, DECADE_BUCKETS),
    [days, metric]
  )
  const weekdays = useMemo(
    () => groupStats(days, metric.toValue, d => d.getDay(), WEEKDAY_BUCKETS),
    [days, metric]
  )

  const decadeVerdict  = useMemo(() => verdictOf(decades, {}),  [decades])
  const weekdayVerdict = useMemo(() => verdictOf(weekdays, {}), [weekdays])

  const hasAnyFailure = maxValue > 0 || (summary?.days_with_failure ?? 0) > 0

  const { rangeLabel, rangePhrase } = useMemo(() => {
    const preset = presets.find(p => p.id === presetId)
    if (preset) return { rangeLabel: preset.label.toLowerCase(), rangePhrase: preset.phrase }
    const f = parseLocal(range.from), t = parseLocal(range.to)
    const fs = `${pad2(f.getDate())}/${pad2(f.getMonth() + 1)}`
    const ts = `${pad2(t.getDate())}/${pad2(t.getMonth() + 1)}`
    return {
      rangeLabel: `${fs} a ${ts}`,
      rangePhrase: fs === ts ? `em ${fs}` : `entre ${fs} e ${ts}`,
    }
  }, [presetId, presets, range])

  // `interval` conta quantos rótulos PULAR entre um e o próximo — derivado da
  // largura medida do card, então 320px e 1600px recebem densidades diferentes.
  const xInterval = tickInterval(chartRows.length, chartWidth)

  const handleBarClick = useCallback(entry => {
    if (!entry?.date || !onPickDay) return
    if (!entry.stations) return // dia sem falha não tem o que abrir
    onPickDay(entry.date)
  }, [onPickDay])

  if (isLoading && !data) {
    return <DailySkeleton />
  }

  if (error) {
    return (
      <div className="fd-error" role="alert">
        Não foi possível carregar a série diária. {String(error.message || error)}
      </div>
    )
  }

  return (
    <div className={`fd${isFetching ? ' fd--refetching' : ''}`}>
      {/* ── Filtros: escopam TUDO abaixo (KPIs, gráfico, painéis, tabela) ── */}
      <div className="fd-filters">
        <div className="fd-filter-group">
          <span className="fd-filter-label" id="fd-period-label">Período</span>
          <div className="fd-presets" role="group" aria-labelledby="fd-period-label">
            {presets.map(p => (
              <button
                key={p.id}
                type="button"
                className={`fd-preset${presetId === p.id ? ' fd-preset--active' : ''}`}
                aria-pressed={presetId === p.id}
                onClick={() => applyPreset(p.id)}
              >
                {p.label}
              </button>
            ))}
          </div>
        </div>

        <div className="fd-filter-group fd-filter-group--dates">
          <label className="fd-date">
            <span>De</span>
            <input type="date" value={range.from} min={minDate} max={maxDate}
                   onChange={e => setCustom('from', e.target.value)} />
          </label>
          <label className="fd-date">
            <span>Até</span>
            <input type="date" value={range.to} min={minDate} max={maxDate}
                   onChange={e => setCustom('to', e.target.value)} />
          </label>
        </div>

        <div className="fd-filter-group">
          <span className="fd-filter-label" id="fd-metric-label">Métrica</span>
          <div className="fd-metrics" role="group" aria-labelledby="fd-metric-label">
            {METRICS.map(m => (
              <button
                key={m.id}
                type="button"
                className={`fd-metric${metricId === m.id ? ' fd-metric--active' : ''}`}
                aria-pressed={metricId === m.id}
                onClick={() => setMetricId(m.id)}
              >
                {m.tab}
              </button>
            ))}
          </div>
        </div>
      </div>

      {!hasAnyFailure ? (
        <DailyEmptyState phrase={rangePhrase} />
      ) : (
        <>
          {/* ── KPIs do período ─────────────────────────────────────────── */}
          <section className="fd-kpi-card">
            <dl className="fd-kpis" aria-label={`Resumo de ${rangeLabel}`}>
              <div className="fd-kpi">
                <dd>{summary?.days_with_failure ?? 0}<span className="fd-kpi-of">/{summary?.days_total ?? days.length}</span></dd>
                <dt>dias com falha</dt>
              </div>
              <div className="fd-kpi-sep" aria-hidden="true" />
              <div className="fd-kpi">
                <dd>{metric.fmt(maxValue)}</dd>
                <dt>
                  pior dia
                  {peakDate && (
                    <span className="fd-kpi-note">
                      {' · '}{pad2(parseLocal(peakDate).getDate())}/{pad2(parseLocal(peakDate).getMonth() + 1)}
                    </span>
                  )}
                </dt>
              </div>
              <div className="fd-kpi-sep" aria-hidden="true" />
              <div className="fd-kpi">
                <dd>{metric.fmtAvg(avgValue)}</dd>
                <dt>média por dia</dt>
              </div>
              <div className="fd-kpi-sep" aria-hidden="true" />
              <div className="fd-kpi">
                <dd>{metric.fmt(totalValue)}</dd>
                <dt>total no período</dt>
              </div>
            </dl>
          </section>

          {/* ── Gráfico principal ───────────────────────────────────────── */}
          <section className="fd-card">
            <header className="fd-card-head">
              <div>
                <h2 className="fd-card-title">{metric.title}</h2>
                <p className="fd-card-hint">{metric.hint}</p>
              </div>
              <button
                type="button"
                className="fd-table-toggle"
                aria-pressed={showTable}
                onClick={() => setShowTable(v => !v)}
              >
                {showTable ? 'Ver gráfico' : 'Ver tabela'}
              </button>
            </header>

            {showTable ? (
              <div className="fd-table-wrap">
                <table className="fd-table">
                  <caption className="fd-sr-only">
                    {metric.title} {rangePhrase}. Todos os dias do período, incluindo os sem falha.
                  </caption>
                  <thead>
                    <tr>
                      <th scope="col">Dia</th>
                      <th scope="col">Emissoras</th>
                      <th scope="col">Veiculações perdidas</th>
                      <th scope="col">Tempo fora</th>
                      <th scope="col" aria-label="Ações" />
                    </tr>
                  </thead>
                  <tbody>
                    {days.map(d => {
                      const p = parseLocal(d.date)
                      return (
                        <tr key={d.date} className={d.date === peakDate ? 'fd-table-row--peak' : ''}>
                          <th scope="row">
                            {pad2(p.getDate())}/{pad2(p.getMonth() + 1)}
                            <span className="fd-table-wday">{WEEKDAYS[p.getDay()]}</span>
                          </th>
                          <td>{fmtBR.format(d.stations)}</td>
                          <td>{fmtBR.format(d.deficit)}</td>
                          <td>{fmtDuration(d.down_seconds)}</td>
                          <td>
                            {d.stations > 0 && onPickDay && (
                              <button type="button" className="fd-table-open"
                                      onClick={() => onPickDay(d.date)}>
                                Abrir dia
                              </button>
                            )}
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            ) : (
              <>
                <div
                  className="fd-chart"
                  ref={chartRef}
                  role="img"
                  aria-label={
                    `${metric.title} ${rangePhrase}. ` +
                    `${summary?.days_with_failure ?? 0} de ${summary?.days_total ?? days.length} dias tiveram falha. ` +
                    `Pior dia: ${peakDate ? `${pad2(parseLocal(peakDate).getDate())}/${pad2(parseLocal(peakDate).getMonth() + 1)}` : '—'}, ` +
                    `com ${metric.fmt(maxValue)}. Média de ${metric.fmtAvg(avgValue)} por dia. ` +
                    `A tabela equivalente tem todos os valores.`
                  }
                >
                  <ResponsiveContainer width="100%" height="100%">
                    <BarChart
                      data={chartRows}
                      margin={{ top: 20, right: 8, bottom: 4, left: -8 }}
                      barCategoryGap="18%"
                      onClick={state => handleBarClick(state?.activePayload?.[0]?.payload)}
                    >
                      <CartesianGrid stroke={GRID} vertical={false} />
                      <XAxis
                        dataKey="label"
                        tick={{ fontSize: 11, fill: AXIS_INK }}
                        tickLine={false}
                        axisLine={{ stroke: GRID }}
                        interval={xInterval}
                        minTickGap={4}
                      />
                      <YAxis
                        tick={{ fontSize: 11, fill: AXIS_INK }}
                        tickLine={false}
                        axisLine={false}
                        width={52}
                        allowDecimals={false}
                        tickFormatter={metric.axisFmt}
                      />
                      {/* Sem rótulo flutuante: ele colidia com a borda direita
                          e passava por cima da última barra. O número mora no
                          KPI "média por dia" e a legenda do rodapé explica a
                          linha — nada fica sem explicação. */}
                      {avgValue > 0 && (
                        <ReferenceLine
                          y={avgValue}
                          stroke={AXIS_INK}
                          strokeDasharray="4 4"
                          strokeWidth={1}
                        />
                      )}
                      <Tooltip
                        cursor={{ fill: 'rgba(232,30,117,0.06)' }}
                        content={props => <ChartTooltip {...props} metric={metric} />}
                      />
                      <Bar
                        dataKey="value"
                        radius={[4, 4, 0, 0]}
                        maxBarSize={24}
                        isAnimationActive={!reducedMotion}
                        animationDuration={220}
                        animationEasing="ease-out"
                      >
                        {chartRows.map(r => (
                          <Cell
                            key={r.date}
                            fill={r.date === peakDate ? BAR_PEAK : BAR}
                            cursor={r.stations > 0 ? 'pointer' : 'default'}
                          />
                        ))}
                      </Bar>
                    </BarChart>
                  </ResponsiveContainer>
                </div>
                <p className="fd-chart-foot">
                  <span className="fd-swatch fd-swatch--peak" aria-hidden="true" />
                  <span>Pior dia em destaque</span>
                  <span className="fd-foot-sep" aria-hidden="true">·</span>
                  <span className="fd-swatch fd-swatch--avg" aria-hidden="true" />
                  <span>Média do período ({metric.fmtAvg(avgValue)})</span>
                  <span className="fd-foot-sep" aria-hidden="true">·</span>
                  <span>Clique numa barra pra abrir o dia em <strong>Por emissora</strong></span>
                </p>
              </>
            )}
          </section>

          {/* ── Onde o mês dói ──────────────────────────────────────────── */}
          <section className="fd-panels">
            <div className="fd-card fd-card--panel">
              <h2 className="fd-card-title">Onde no mês dói mais</h2>
              <p className="fd-card-hint">
                Média por dia em cada terço do mês — comparar somas seria injusto,
                os blocos têm quantidades de dias diferentes.
              </p>
              <PatternBars
                rows={decades}
                metric={metric}
                caption={`Média de ${metric.unit} por dia, ${rangeLabel}.`}
                describeRow={r => `${r.label}: média de ${metric.fmtAvg(r.avg)} por dia em ${r.days} dias.`}
              />
              {decadeVerdict && (
                <p className="fd-verdict">
                  {decadeVerdict.flat
                    ? 'As falhas se distribuem de forma parecida ao longo do mês — nenhum terço se destaca.'
                    : decadeVerdict.ratio == null
                      ? <>Só houve falha em <strong>{decadeVerdict.top.label.toLowerCase()}</strong>.</>
                      : <>Concentra em <strong>{decadeVerdict.top.label.toLowerCase()}</strong>: {fmtRatio(decadeVerdict.ratio)}× a média dos outros dois terços.</>}
                </p>
              )}
            </div>

            <div className="fd-card fd-card--panel">
              <h2 className="fd-card-title">Por dia da semana</h2>
              <p className="fd-card-hint">
                Revela padrão recorrente — manutenção de segunda, provedor que cai
                no fim de semana.
              </p>
              <PatternBars
                rows={weekdays}
                metric={metric}
                caption={`Média de ${metric.unit} por dia, ${rangeLabel}.`}
                describeRow={r => `${r.long}: média de ${metric.fmtAvg(r.avg)} por dia em ${r.days} ocorrências.`}
              />
              {weekdayVerdict && (
                <p className="fd-verdict">
                  {weekdayVerdict.flat
                    ? 'Nenhum dia da semana se destaca — a falha não segue o calendário.'
                    : weekdayVerdict.ratio == null
                      ? <>Só houve falha <strong>{weekdayVerdict.top.key === 0 || weekdayVerdict.top.key === 6 ? 'no' : 'na'} {weekdayVerdict.top.long}</strong>.</>
                      : <><span className="fd-verdict-cap">{weekdayVerdict.top.long}</span> é o pior dia: {fmtRatio(weekdayVerdict.ratio)}× a média dos outros.</>}
                </p>
              )}
            </div>
          </section>
        </>
      )}
    </div>
  )
}
