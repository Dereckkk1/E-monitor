import { test, expect } from '@playwright/test'

/**
 * O aviso de CNPJ repetido no cadastro de cliente (spec do hub 2026-10-22,
 * §3.2). O backend devolve 409 `cnpj_duplicado` com o cliente que já tem
 * aqueles 14 dígitos; a tela tem de dizer QUEM é e levar a pessoa até ele.
 *
 * ⚠️ Este app troca `window.alert` pelo AlertDialog do ConfirmModal, então não
 * existe diálogo nativo: `page.on('dialog')` ficaria calado para sempre e o
 * teste passaria a esperar um evento que nunca vem. O que se afirma é o que
 * está RENDERIZADO na página (role=dialog com a mensagem).
 *
 * A API é falseada com `page.route`: o que está em prova é a TELA.
 */

const EXISTENTE = { id: '33333333-3333-3333-3333-333333333333', nome: 'GIGA MAIS FIBRA' }
const MENSAGEM = `Já existe ${EXISTENTE.nome} com este CNPJ. Use esse cliente em vez de criar outro.`

const CLIENTE_EDITADO = {
  id: '44444444-4444-4444-4444-444444444444',
  name: 'Giga duplicada',
  cnpj: '11.222.333/0001-81',
  is_active: true,
}

/** Sessão de admin + a listagem (guarda os `q` pedidos) + a escrita falseada. */
async function preparar(page, { metodo, status, corpo, lista = [] }) {
  await page.addInitScript(() => {
    sessionStorage.setItem('rc_token', 'jwt.de.teste')
    sessionStorage.setItem('rc_user', JSON.stringify({
      id: '00000000-0000-0000-0000-000000000009',
      name: 'QA', email: 'qa@teste.local', role: 'admin', client_id: null,
    }))
  })

  const buscasFeitas = []
  const escritas = []
  // Uma rota só para /clients e /clients/:id: o glob com `?` não casa a
  // listagem com query string de forma confiável, regex casa as duas.
  await page.route(/\/v1\/internal\/clients(\/[^/?]+)?(\?.*)?$/, route => {
    const req = route.request()
    if (req.method() === 'GET') {
      const q = new URL(req.url()).searchParams.get('q') ?? ''
      buscasFeitas.push(q)
      const data = q ? [] : lista
      return route.fulfill({
        status: 200, contentType: 'application/json',
        body: JSON.stringify({ data, total: data.length, total_pages: 1 }),
      })
    }
    if (req.method() === metodo) {
      escritas.push(req.postDataJSON())
      return route.fulfill({
        status, contentType: 'application/json', body: JSON.stringify(corpo),
      })
    }
    return route.fallback()
  })
  return { buscasFeitas, escritas }
}

const CONFLITO = { error: 'cnpj_duplicado', existente: EXISTENTE }

test('CNPJ repetido ao CRIAR: avisa com o nome do existente, fecha o form e filtra a lista por ele', async ({ page }) => {
  const { buscasFeitas, escritas } = await preparar(page, { metodo: 'POST', status: 409, corpo: CONFLITO })

  await page.goto('/clients')
  await page.getByRole('button', { name: /novo cliente/i }).first().click()
  await page.getByPlaceholder('Nome do cliente').fill('Giga duplicada')
  await page.getByPlaceholder('00.000.000/0000-00').fill('07.714.104/0001-07')
  await page.getByRole('button', { name: 'Criar cliente' }).click()

  await expect(page.getByRole('dialog').filter({ hasText: MENSAGEM })).toBeVisible()
  expect(escritas).toHaveLength(1)

  // Fecha o aviso: o formulário de criação já foi dispensado.
  await page.getByRole('button', { name: /^ok$/i }).click()
  await expect(page.getByRole('heading', { name: 'Novo cliente' })).toHaveCount(0)

  await expect(page.getByPlaceholder(/buscar por nome/i)).toHaveValue(EXISTENTE.nome)
  await expect.poll(() => buscasFeitas).toContain(EXISTENTE.nome)
})

test('CNPJ repetido ao EDITAR: avisa e filtra pelo existente, mantendo o form aberto', async ({ page }) => {
  const { buscasFeitas, escritas } = await preparar(page, {
    metodo: 'PUT', status: 409, corpo: CONFLITO, lista: [CLIENTE_EDITADO],
  })

  await page.goto('/clients')
  await page.getByTitle('Editar').click()
  await page.getByPlaceholder('00.000.000/0000-00').fill('07.714.104/0001-07')
  await page.getByRole('button', { name: 'Salvar alterações' }).click()

  await expect(page.getByRole('dialog').filter({ hasText: MENSAGEM })).toBeVisible()
  expect(escritas).toHaveLength(1)
  await expect(page.getByPlaceholder(/buscar por nome/i)).toHaveValue(EXISTENTE.nome)
  await expect.poll(() => buscasFeitas).toContain(EXISTENTE.nome)
})

test('erro que não é 409: nenhum aviso de CNPJ e a lista não é filtrada', async ({ page }) => {
  const { buscasFeitas } = await preparar(page, {
    metodo: 'POST', status: 500, corpo: { error: 'boom' },
  })

  await page.goto('/clients')
  await page.getByRole('button', { name: /novo cliente/i }).first().click()
  await page.getByPlaceholder('Nome do cliente').fill('Qualquer')
  await page.getByRole('button', { name: 'Criar cliente' }).click()

  // O comportamento antigo se mantém: o form continua aberto.
  await expect(page.getByRole('heading', { name: 'Novo cliente' })).toBeVisible()
  await expect(page.getByRole('dialog').filter({ hasText: /Já existe/ })).toHaveCount(0)
  expect(buscasFeitas.every(q => q === '')).toBe(true)
})
