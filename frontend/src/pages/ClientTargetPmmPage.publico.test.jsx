// Público-alvo do cliente na tela "PMM no target": edita clients.target_label
// sem sair da tela. Enter salva, Escape cancela, e o PUT leva o cliente
// INTEIRO (o PUT /clients/:id sobrescreve o registro — mandar só o rótulo
// apagaria nome, contato, CNPJ...).
import { describe, test, expect, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import ClientTargetPmmPage from './ClientTargetPmmPage'
import { renderComProviders } from '../test-utils/render'
import { mockApi } from '../test-utils/api'

const CLIENTE = {
  id: 'c1', name: 'Cliente Teste', logo_url: null,
  contact_email: 'contato@example.com', contact_name: 'Ana', phone: null,
  cnpj: '12.345.678/0001-90', cep: null, city: 'Cidade Teste', state: 'SC',
  target_label: null,
}

let chamadas

function abrir() {
  renderComProviders(<ClientTargetPmmPage />, {
    rota: '/clients/c1/target-pmm', caminho: '/clients/:id/target-pmm',
  })
}

async function editarPublico(texto) {
  const definir = await screen.findByRole('button', { name: /definir público-alvo/ })
  // fica desabilitado até o cliente chegar (sem cliente não há o que gravar)
  await waitFor(() => expect(definir.disabled).toBe(false))
  fireEvent.click(definir)
  const campo = screen.getByRole('textbox', { name: 'Público-alvo do cliente' })
  fireEvent.change(campo, { target: { value: texto } })
  return campo
}

describe('ClientTargetPmmPage — público-alvo', () => {
  beforeEach(() => {
    chamadas = mockApi({
      'GET /clients': { data: [CLIENTE] },
      'GET /clients/c1/target-pmm': { data: [] },
      'PUT /clients/c1': (config) => ({ ...CLIENTE, ...JSON.parse(config.data) }),
    })
  })

  test('Enter salva o público e manda o cliente inteiro no PUT', async () => {
    abrir()
    const campo = await editarPublico('  Homens 25-49, classe AB  ')

    fireEvent.keyDown(campo, { key: 'Enter' })

    await waitFor(() => expect(chamadas.some(c => c.chave === 'PUT /clients/c1')).toBe(true))
    const put = chamadas.find(c => c.chave === 'PUT /clients/c1')
    expect(put.data).toEqual({
      name: 'Cliente Teste', logo_url: null, contact_email: 'contato@example.com',
      contact_name: 'Ana', phone: null, cnpj: '12.345.678/0001-90', cep: null,
      city: 'Cidade Teste', state: 'SC',
      target_label: 'Homens 25-49, classe AB', // aparado
    })
    // salvou: sai do modo edição
    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Público-alvo do cliente' })).toBeNull())
  })

  test('Escape cancela sem gravar nada', async () => {
    abrir()
    const campo = await editarPublico('Mulheres 18-34')

    fireEvent.keyDown(campo, { key: 'Escape' })

    expect(screen.queryByRole('textbox', { name: 'Público-alvo do cliente' })).toBeNull()
    expect(chamadas.some(c => c.metodo === 'PUT')).toBe(false)
  })

  test('Enter sem mudar o texto só fecha a edição (sem PUT)', async () => {
    abrir()
    const campo = await editarPublico('   ')

    fireEvent.keyDown(campo, { key: 'Enter' })

    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Público-alvo do cliente' })).toBeNull())
    expect(chamadas.some(c => c.metodo === 'PUT')).toBe(false)
  })
})
