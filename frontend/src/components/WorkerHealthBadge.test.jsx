import { describe, test, expect } from 'vitest'
import { screen } from '@testing-library/react'
import WorkerHealthBadge from './WorkerHealthBadge'
import { renderComProviders } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'

describe('WorkerHealthBadge', () => {
  test('mostra o status do verificador e uma linha por worker, com risco de travamento', async () => {
    mockApi({
      'GET /workers': {
        clap_verifier: true,
        workers: [
          { station_id: '00000000-0000-0000-0000-000000000001', stall_risk: false, last_pcm_at: null },
          { station_id: '00000000-0000-0000-0000-000000000002', stall_risk: true, last_pcm_at: '2026-03-10T15:00:00Z' },
        ],
      },
    })
    renderComProviders(<WorkerHealthBadge />)
    expect(screen.getByText('Carregando status…')).toBeTruthy()
    expect(await screen.findByText('Online')).toBeTruthy()
    expect(screen.getAllByText('00000000…').length).toBe(2)
    expect(screen.getByText('OK')).toBeTruthy()
    expect(screen.getByText('Lento')).toBeTruthy()
    expect(screen.getByText('–')).toBeTruthy()   // sem último PCM
  })

  test('verificador fora do ar aparece como Offline', async () => {
    mockApi({ 'GET /workers': { clap_verifier: false, workers: [] } })
    renderComProviders(<WorkerHealthBadge />)
    expect(await screen.findByText('Offline')).toBeTruthy()
  })

  test('erro da API mostra a falha em vez da tabela', async () => {
    mockApi({ 'GET /workers': erroApi(500, {}) })
    renderComProviders(<WorkerHealthBadge />)
    expect(await screen.findByText('Falha ao carregar status')).toBeTruthy()
  })
})
