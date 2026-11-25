import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import FailuresDailyView from './FailuresDailyView'
import { renderComProviders } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'

// "hoje" = quarta, 15/07/2026. Só o Date é falso: React Query e waitFor seguem em tempo real.
beforeEach(() => { vi.useFakeTimers({ toFake: ['Date'], now: new Date(2026, 6, 15, 12) }) })
afterEach(() => { vi.useRealTimers() })

const SERIE = {
  summary: { days_with_failure: 3, days_total: 15 },
  days: [
    { date: '2026-07-01', stations: 0, deficit: 0, down_seconds: 0 },     // quarta
    { date: '2026-07-06', stations: 3, deficit: 12, down_seconds: 7200 }, // segunda
    { date: '2026-07-07', stations: 1, deficit: 2, down_seconds: 300 },   // terça
    { date: '2026-07-13', stations: 5, deficit: 20, down_seconds: 3600 }, // segunda
  ],
}

function kpi(rotulo) {
  const dt = screen.getByText((_, el) => el.tagName === 'DT' && el.textContent.startsWith(rotulo))
  return dt.parentElement.querySelector('dd').textContent
}

async function montar(props = {}, resposta = SERIE) {
  const chamadas = mockApi({ 'GET /admin/failures-daily': resposta })
  const r = renderComProviders(<FailuresDailyView {...props} />)
  return { chamadas, ...r }
}

describe('FailuresDailyView — período e carregamento', () => {
  test('mostra o skeleton enquanto carrega e pede o mês corrente (de 01/07 até hoje)', async () => {
    const { chamadas, container } = await montar()
    expect(container.querySelector('.fd-skel')).not.toBeNull()
    expect(await screen.findByText('dias com falha')).toBeTruthy()
    const busca = chamadas.find(c => c.chave === 'GET /admin/failures-daily')
    expect(busca.params).toEqual({ from: '2026-07-01', to: '2026-07-15', min_down_seconds: 60 })
  })

  test('minDownSeconds vai na query', async () => {
    const { chamadas } = await montar({ minDownSeconds: 300 })
    await screen.findByText('dias com falha')
    expect(chamadas[0].params.min_down_seconds).toBe(300)
  })

  test('presets: Mês passado, 30 e 90 dias refazem a busca com as datas certas e marcam o botão', async () => {
    const { chamadas } = await montar()
    await screen.findByText('dias com falha')
    expect(screen.getByRole('button', { name: 'Este mês' }).getAttribute('aria-pressed')).toBe('true')

    fireEvent.click(screen.getByRole('button', { name: 'Mês passado' }))
    await waitFor(() => expect(chamadas.some(c => c.params?.from === '2026-06-01' && c.params?.to === '2026-06-30')).toBe(true))
    expect(screen.getByRole('button', { name: 'Mês passado' }).getAttribute('aria-pressed')).toBe('true')
    expect(screen.getByRole('button', { name: 'Este mês' }).getAttribute('aria-pressed')).toBe('false')

    fireEvent.click(screen.getByRole('button', { name: '30 dias' }))
    await waitFor(() => expect(chamadas.some(c => c.params?.from === '2026-06-16' && c.params?.to === '2026-07-15')).toBe(true))

    fireEvent.click(screen.getByRole('button', { name: '90 dias' }))
    await waitFor(() => expect(chamadas.some(c => c.params?.from === '2026-04-17' && c.params?.to === '2026-07-15')).toBe(true))
  })

  test('datas manuais: limite de 90 dias nos inputs e "de" depois de "até" arrasta a outra ponta', async () => {
    const { chamadas, container } = await montar()
    await screen.findByText('dias com falha')
    const [de, ate] = container.querySelectorAll('input[type="date"]')
    expect(de.getAttribute('min')).toBe('2026-04-16')   // hoje − 90 dias
    expect(de.getAttribute('max')).toBe('2026-07-15')
    expect(ate.getAttribute('max')).toBe('2026-07-15')

    fireEvent.change(ate, { target: { value: '2026-07-05' } }) // antes de "de" (01/07 ainda vale)
    fireEvent.change(de, { target: { value: '2026-07-10' } })  // "de" passa de "até" → até acompanha
    await waitFor(() => expect(chamadas.some(c => c.params?.from === '2026-07-10' && c.params?.to === '2026-07-10')).toBe(true))
    // nenhum preset fica marcado em período manual
    for (const nome of ['Este mês', 'Mês passado', '30 dias', '90 dias']) {
      expect(screen.getByRole('button', { name: nome }).getAttribute('aria-pressed')).toBe('false')
    }
  })

  test('periodoInicial que bate com um preset já abre com ele marcado', async () => {
    const { chamadas } = await montar({ periodoInicial: { from: '2026-06-01', to: '2026-06-30' } })
    await screen.findByText('dias com falha')
    expect(chamadas[0].params).toMatchObject({ from: '2026-06-01', to: '2026-06-30' })
    expect(screen.getByRole('button', { name: 'Mês passado' }).getAttribute('aria-pressed')).toBe('true')
  })

  test('periodoInicial livre (dentro de 90 dias) vira período manual; fora do limite cai no mês corrente', async () => {
    const livre = await montar({ periodoInicial: { from: '2026-07-02', to: '2026-07-10' } })
    await screen.findByText('dias com falha')
    expect(livre.chamadas[0].params).toMatchObject({ from: '2026-07-02', to: '2026-07-10' })
    expect(screen.getByRole('button', { name: 'Este mês' }).getAttribute('aria-pressed')).toBe('false')
    livre.unmount()

    const velho = await montar({ periodoInicial: { from: '2025-01-01', to: '2025-01-31' } })
    await screen.findByText('dias com falha')
    expect(velho.chamadas[0].params).toMatchObject({ from: '2026-07-01', to: '2026-07-15' })
  })

  test('erro da API mostra o alerta em vez da série', async () => {
    mockApi({ 'GET /admin/failures-daily': erroApi(500, { error: 'falhou' }) })
    renderComProviders(<FailuresDailyView />)
    const alerta = await screen.findByRole('alert')
    expect(alerta.textContent).toMatch(/Não foi possível carregar a série diária/)
  })
})

describe('FailuresDailyView — KPIs e métricas', () => {
  test('métrica Emissoras: dias com falha, pior dia (com data), média e total', async () => {
    await montar()
    await screen.findByText('dias com falha')
    expect(kpi('dias com falha')).toBe('3/15')
    expect(kpi('pior dia')).toBe('5')
    expect(screen.getByText((_, el) => el.className === 'fd-kpi-note' && el.textContent === ' · 13/07')).toBeTruthy()
    expect(kpi('média por dia')).toBe('2,3')   // 9 / 4 dias, uma casa decimal
    expect(kpi('total no período')).toBe('9')
    expect(screen.getByText('Emissoras com falha por dia')).toBeTruthy()
  })

  test('métrica Veiculações perdidas troca título e números', async () => {
    await montar()
    await screen.findByText('dias com falha')
    fireEvent.click(screen.getByRole('button', { name: 'Veiculações perdidas' }))
    expect(screen.getByText('Veiculações perdidas por dia')).toBeTruthy()
    expect(kpi('pior dia')).toBe('20')
    expect(kpi('média por dia')).toBe('8,5')
    expect(kpi('total no período')).toBe('34')
    expect(screen.getByRole('button', { name: 'Veiculações perdidas' }).getAttribute('aria-pressed')).toBe('true')
  })

  test('métrica Tempo fora do ar é plotada em minutos e formatada em horas', async () => {
    await montar()
    await screen.findByText('dias com falha')
    fireEvent.click(screen.getByRole('button', { name: 'Tempo fora do ar' }))
    expect(screen.getByText('Tempo fora do ar por dia')).toBeTruthy()
    expect(kpi('pior dia')).toBe('2h')           // 7200 s
    expect(kpi('total no período')).toBe('3h05') // 120 + 5 + 60 = 185 min
    expect(kpi('média por dia')).toBe('46min')   // 185 / 4
  })

  test('sem nenhuma falha no período mostra o estado vazio com a frase do período', async () => {
    await montar({}, { summary: { days_with_failure: 0, days_total: 15 }, days: [{ date: '2026-07-01', stations: 0, deficit: 0, down_seconds: 0 }] })
    expect(await screen.findByRole('heading', { name: 'Nenhuma falha neste mês.' })).toBeTruthy()
    expect(screen.queryByText('dias com falha')).toBeNull()
    // trocar o período muda a frase
    fireEvent.click(screen.getByRole('button', { name: '90 dias' }))
    expect(await screen.findByRole('heading', { name: 'Nenhuma falha nos últimos 90 dias.' })).toBeTruthy()
  })

  test('período manual no vazio usa "entre dd/mm e dd/mm"', async () => {
    await montar({ periodoInicial: { from: '2026-07-02', to: '2026-07-10' } },
      { summary: { days_with_failure: 0, days_total: 9 }, days: [] })
    expect(await screen.findByRole('heading', { name: 'Nenhuma falha entre 02/07 e 10/07.' })).toBeTruthy()
  })
})

describe('FailuresDailyView — tabela e padrões', () => {
  test('Ver tabela lista todos os dias (inclusive sem falha) e "Abrir dia" só nos dias com falha', async () => {
    const onPickDay = vi.fn()
    await montar({ onPickDay })
    await screen.findByText('dias com falha')
    fireEvent.click(screen.getByRole('button', { name: 'Ver tabela' }))
    const tabela = screen.getByRole('table')
    const linhas = within(tabela).getAllByRole('row')
    expect(linhas.length).toBe(1 + 4)  // cabeçalho + 4 dias
    expect(within(tabela).getByText('06/07', { exact: false })).toBeTruthy()
    expect(within(tabela).getAllByRole('button', { name: 'Abrir dia' }).length).toBe(3)
    // o dia de pico (13/07, 5 emissoras) vem marcado
    expect(linhas[4].className).toBe('fd-table-row--peak')
    fireEvent.click(within(tabela).getAllByRole('button', { name: 'Abrir dia' })[0])
    expect(onPickDay).toHaveBeenCalledWith('2026-07-06')
    // volta pro gráfico
    fireEvent.click(screen.getByRole('button', { name: 'Ver gráfico' }))
    expect(screen.queryByRole('table')).toBeNull()
  })

  test('sem onPickDay a tabela não oferece "Abrir dia"', async () => {
    await montar()
    await screen.findByText('dias com falha')
    fireEvent.click(screen.getByRole('button', { name: 'Ver tabela' }))
    expect(screen.queryByRole('button', { name: 'Abrir dia' })).toBeNull()
  })

  test('tempo fora na tabela usa s / min / h conforme o tamanho', async () => {
    await montar({}, {
      summary: { days_with_failure: 3, days_total: 3 },
      days: [
        { date: '2026-07-01', stations: 1, deficit: 1, down_seconds: 45 },
        { date: '2026-07-02', stations: 1, deficit: 1, down_seconds: 300 },
        { date: '2026-07-03', stations: 1, deficit: 1, down_seconds: 5400 },
      ],
    })
    await screen.findByText('dias com falha')
    fireEvent.click(screen.getByRole('button', { name: 'Ver tabela' }))
    const tabela = screen.getByRole('table')
    expect(within(tabela).getByText('45s')).toBeTruthy()
    expect(within(tabela).getByText('5min')).toBeTruthy()
    expect(within(tabela).getByText('1h30')).toBeTruthy()
  })

  test('painel por dia da semana aponta a segunda como o pior dia (razão acima de 1,25×)', async () => {
    await montar()
    await screen.findByText('dias com falha')
    expect(screen.getByText((_, el) => el.className === 'fd-verdict'
      && el.textContent === 'segunda é o pior dia: 8× a média dos outros.')).toBeTruthy()
    // e o painel por terço do mês aponta o 2º terço
    expect(screen.getByText((_, el) => el.className === 'fd-verdict'
      && el.textContent === 'Concentra em dias 11–20: 3,8× a média dos outros dois terços.')).toBeTruthy()
  })

  test('falhas parecidas em todos os dias dizem que nada se destaca', async () => {
    await montar({}, {
      summary: { days_with_failure: 7, days_total: 7 },
      days: ['2026-07-06', '2026-07-07', '2026-07-08', '2026-07-09', '2026-07-10', '2026-07-11', '2026-07-12']
        .map(date => ({ date, stations: 2, deficit: 4, down_seconds: 600 })),
    })
    await screen.findByText('dias com falha')
    expect(screen.getByText('Nenhum dia da semana se destaca — a falha não segue o calendário.')).toBeTruthy()
  })

  test('falha em um único dia da semana (sem razão calculável) vira "Só houve falha na/no …"', async () => {
    await montar({}, {
      summary: { days_with_failure: 1, days_total: 3 },
      days: [
        { date: '2026-07-06', stations: 4, deficit: 8, down_seconds: 600 },  // segunda
        { date: '2026-07-07', stations: 0, deficit: 0, down_seconds: 0 },
        { date: '2026-07-08', stations: 0, deficit: 0, down_seconds: 0 },
      ],
    })
    await screen.findByText('dias com falha')
    expect(screen.getByText((_, el) => el.className === 'fd-verdict'
      && el.textContent === 'Só houve falha na segunda.')).toBeTruthy()
    // só um terço do mês tem dias: sem comparação possível, não há veredito de terço
    expect(screen.queryByText(/Concentra em/)).toBeNull()
  })

  test('as barras do painel de padrão trazem a média por dia e o texto de leitor de tela', async () => {
    await montar()
    await screen.findByText('dias com falha')
    expect(screen.getByText('segunda: média de 4 por dia em 2 ocorrências.')).toBeTruthy()
    expect(screen.getByText('Dias 11–20: média de 5 por dia em 1 dias.')).toBeTruthy()
    expect(screen.getAllByText(/Média de emissoras por dia, este mês\./).length).toBe(2)
  })
})
