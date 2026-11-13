import { test, expect } from '@playwright/test'

/**
 * A tela de sugestões depois que a fila foi para o hub (spec do hub 2026-10-26
 * §4). A API daqui é falseada com `page.route` no JSON que o
 * SuggestionsHubHandler devolve; o que se prova é a TELA: a ordem
 * anexo → criação, a chave de envio, o aviso da pessoa gestora, `retirada`, o
 * selo de origem e o rascunho que sobrevive ao hub fora do ar.
 */

const ID = '6650aaaaaaaaaaaaaaaaaaaa'
const LISTA = /\/v1\/internal\/suggestions(\?.*)?$/
const UNREAD = /\/v1\/internal\/suggestions\/unread-count$/
const STAGE = /\/v1\/internal\/suggestions\/attachments$/
const DETALHE = new RegExp(`/v1/internal/suggestions/${ID}$`)
const LIDA = new RegExp(`/v1/internal/suggestions/${ID}/read$`)
const SUMMARY = /\/v1\/internal\/suggestions\/summary$/

const LINHA = {
  id: ID, ref_num: 42, origin: 'hub', type: 'feature', title: 'Filtro por cliente', description: 'prévia',
  status: 'retirada', requester_priority: 'media', awaiting_author: false, created_by: 'u1',
  created_by_name: 'QA', comment_count: 0, unread: false,
  created_at: '2026-10-26T12:00:00Z', updated_at: '2026-10-26T12:00:00Z',
}
const DETALHE_JSON = { ...LINHA, description: 'Quero filtrar.', comments: [], attachments: [], events: [] }

const PNG = Buffer.from('89504e470d0a1a0a0000000d4948445200000001000000010806000000' +
  '1f15c4890000000d49444154789c6360000002000154a24f5d0000000049454e44ae426082', 'hex')

function sessao(page, email = 'qa@teste.local') {
  return page.addInitScript((e) => {
    sessionStorage.setItem('rc_token', 'jwt.de.teste')
    sessionStorage.setItem('rc_user', JSON.stringify({
      id: '00000000-0000-0000-0000-000000000009', name: 'QA', email: e, role: 'admin', client_id: null,
    }))
  }, email)
}

const json = (r, status, body) => r.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })

async function rotasBase(page, pedidos) {
  await page.route(UNREAD, r => json(r, 200, { count: 0 }))
  await page.route(LIDA, r => r.fulfill({ status: 204 }))
  await page.route(DETALHE, r => json(r, 200, DETALHE_JSON))
  await page.route(SUMMARY, r => { pedidos.push('summary'); return json(r, 410, { error: 'x' }) })
}

test('autor vê retirada e o selo de origem', async ({ page }) => {
  const pedidos = []
  await sessao(page)
  await rotasBase(page, pedidos)
  await page.route(LISTA, r => json(r, 200, { data: [LINHA] }))
  await page.goto('/admin/suggestions')

  await expect(page.getByText('Filtro por cliente')).toBeVisible()
  await expect(page.locator('.sug-card').getByText('Retirada')).toBeVisible()
  await expect(page.locator('.sug-card').getByText('via Hub')).toBeVisible()
})

test('o anexo sobe ANTES e a criação leva o id e a chave', async ({ page }) => {
  const ordem = []
  let corpoCriacao = null
  await sessao(page)
  await rotasBase(page, [])
  await page.route(STAGE, r => { ordem.push('stage'); return json(r, 201, { id: 'an1' }) })
  await page.route(LISTA, r => {
    if (r.request().method() === 'POST') {
      ordem.push('create')
      corpoCriacao = r.request().postDataJSON()
      return json(r, 201, DETALHE_JSON)
    }
    return json(r, 200, { data: [] })
  })
  await page.goto('/admin/suggestions')

  await page.getByRole('button', { name: /nova sugestão/i }).first().click()
  await page.locator('#sug-title').fill('Filtro por cliente')
  await page.locator('#sug-desc').fill('Quero filtrar.')
  await page.locator('.sug-modal input[type=file]').setInputFiles({ name: 'print.png', mimeType: 'image/png', buffer: PNG })
  await page.getByRole('button', { name: 'Enviar sugestão' }).click()

  await expect.poll(() => ordem).toEqual(['stage', 'create'])
  expect(corpoCriacao.attachment_ids).toEqual(['an1'])
  expect(corpoCriacao.client_key).toMatch(/^[A-Za-z0-9_-]{8,64}$/)
  expect(corpoCriacao.type).toBe('melhoria')
})

test('hub fora do ar: a mensagem aparece e o texto fica', async ({ page }) => {
  const chaves = []
  await sessao(page)
  await rotasBase(page, [])
  await page.route(LISTA, r => {
    if (r.request().method() === 'POST') {
      chaves.push(r.request().postDataJSON().client_key)
      return json(r, 503, { error: 'não consegui falar com a Central de Clientes, tente de novo' })
    }
    return json(r, 200, { data: [] })
  })
  await page.goto('/admin/suggestions')

  await page.getByRole('button', { name: /nova sugestão/i }).first().click()
  await page.locator('#sug-title').fill('Filtro por cliente')
  await page.locator('#sug-desc').fill('Quero filtrar.')
  await page.getByRole('button', { name: 'Enviar sugestão' }).click()
  await expect(page.getByRole('alert')).toContainText('tente de novo')
  await expect(page.locator('#sug-title')).toHaveValue('Filtro por cliente')

  await page.getByRole('button', { name: 'Enviar sugestão' }).click()
  await expect.poll(() => chaves.length).toBe(2)
  // A chave tem de EXISTIR: undefined === undefined passava com o código antigo.
  expect(chaves[0]).toMatch(/^[A-Za-z0-9_-]{8,64}$/)
  expect(chaves[0]).toBe(chaves[1]) // tentar de novo é o MESMO envio
})

test('pessoa gestora vê que a fila vive no hub, sem chamar o summary', async ({ page }) => {
  const pedidos = []
  await sessao(page, 'tatico3@hubradios.com')
  await rotasBase(page, pedidos)
  await page.route(LISTA, r => json(r, 200, { data: [] }))
  await page.goto('/admin/suggestions')

  await expect(page.getByRole('heading', { name: 'A fila agora vive no hub' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Abrir a fila no hub' }))
    .toHaveAttribute('href', 'https://clientes.emidiastec.com.br/admin/sugestoes')
  expect(pedidos).toEqual([])
})
