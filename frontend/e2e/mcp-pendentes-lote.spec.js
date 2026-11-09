import { test, expect } from '@playwright/test'

/**
 * /pendentes — decidir em lote. A tela agrupa os pedidos pela campanha (a lista traz `campanha`),
 * deixa marcar vários e chama a MESMA rota do cartão, um pedido por vez, na ordem em que a IA pediu.
 * As conferências de cada aprovação são do servidor (workers/internal/mcp/*_test.go); aqui se prova a TELA.
 */

const LISTA = '**/v1/internal/mcp/pendentes*'
const APROVAR = '**/v1/internal/mcp/pendentes/*/aprovar'
const RECUSAR = '**/v1/internal/mcp/pendentes/*/recusar'
const ADMIN = { id: '00000000-0000-0000-0000-000000000009', name: 'QA Admin', email: 'qa@teste.local', role: 'admin', client_id: null, client_ids: [], is_active: true, created_at: '2026-01-01T00:00:00Z' }
const VERAO = { id: 'c0000000-0000-4000-8000-000000000001', nome: 'Verão 2026' }

const json = (r, status, body) => r.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })

function pedido(id, resumo, over = {}) {
  return {
    id, tool: 'editar_regra_distribuicao', motivo: 'Mexe em dia já veiculado.',
    preview: { resumo, mudancas: [], efeitos: [], ressalvas: [] },
    estado: 'pendente', tentativa: 0, criada_em: '2026-11-03T15:00:00Z', expira_em: '2099-03-10T15:30:00Z',
    aprovada_em: null, executada_em: null, recusada_em: null, erro: null, ressalvas: [], campanha: null, ...over,
  }
}

// A IA pediu na ordem 1, 2, 3 (criada_em); a lista vem do mais novo para o mais antigo, como a API devolve.
const P1 = pedido('11111111-1111-4111-8111-111111111111', 'Meta do dia 10/11 de 3 para 5', { campanha: VERAO, criada_em: '2026-11-03T15:00:00Z' })
const P2 = pedido('22222222-2222-4222-8222-222222222222', 'Meta do dia 11/11 de 3 para 5', { campanha: VERAO, criada_em: '2026-11-03T15:01:00Z', tentativa: 2 })
const P3 = pedido('33333333-3333-4333-8333-333333333333', 'Meta do dia 12/11 de 3 para 5', { campanha: VERAO, criada_em: '2026-11-03T15:02:00Z' })
const SOLTO = pedido('44444444-4444-4444-8444-444444444444', 'Desativar o usuário "Fulano"', { tool: 'desativar_usuario', criada_em: '2026-11-03T15:03:00Z' })

const idDa = (url) => url.split('/').slice(-2)[0]

/** Fila falsa que lembra o que foi decidido. `aprovar(id, n)` devolve [status, corpo] ou 'abortar'. */
async function servidor(page, itens, { aprovar = () => [200, {}], recusar = () => [200, {}] } = {}) {
  const fechados = new Set()
  const ordem = []
  const corpos = []
  await page.addInitScript((u) => {
    sessionStorage.setItem('rc_token', 'jwt.de.teste')
    sessionStorage.setItem('rc_user', JSON.stringify(u))
  }, ADMIN)
  await page.route('**/v1/internal/auth/me', r => json(r, 200, ADMIN))
  await page.route('**/v1/internal/suggestions/**', r => json(r, 200, { count: 0 }))
  await page.route(LISTA, r => json(r, 200, { items: itens.filter(p => !fechados.has(p.id)), destaque: null }))
  await page.route(APROVAR, r => {
    const id = idDa(r.request().url())
    ordem.push(id)
    corpos.push(r.request().postDataJSON())
    const out = aprovar(id, ordem.length)
    if (out === 'abortar') return r.abort('connectionreset')
    const [status, corpo] = out
    if (status === 200) fechados.add(id)
    const p = itens.find(x => x.id === id)
    return json(r, status, status === 200 ? { pendente: { ...p, estado: 'executada', campanha: null }, ressalvas: [] } : corpo)
  })
  await page.route(RECUSAR, r => {
    const id = idDa(r.request().url())
    ordem.push(id)
    const [status, corpo] = recusar(id)
    if (status === 200) fechados.add(id)
    const p = itens.find(x => x.id === id)
    return json(r, status, status === 200 ? { pendente: { ...p, estado: 'recusada', campanha: null } } : corpo)
  })
  return { ordem, corpos }
}

test('agrupa pela campanha, seleciona o grupo e aprova em lote na ordem em que a IA pediu', async ({ page }) => {
  const s = await servidor(page, [SOLTO, P3, P2, P1])
  await page.goto('/pendentes')

  const verao = page.getByRole('region', { name: 'Campanha Verão 2026' })
  await expect(verao.getByRole('heading', { name: /Verão 2026/ })).toBeVisible()
  await expect(verao.getByRole('article')).toHaveCount(3)
  // O pedido sem campanha vai para "Outros pedidos", por último.
  await expect(page.getByRole('region', { name: 'Outros pedidos' }).getByRole('article')).toHaveCount(1)

  await page.getByRole('checkbox', { name: 'Selecionar todos os pedidos de Verão 2026' }).check()
  const lote = page.getByRole('region', { name: 'Decidir em lote' })
  await expect(lote.locator('.pen-lote-texto')).toHaveText('3 selecionados')
  await lote.getByRole('button', { name: 'Aprovar selecionados (3)' }).click()

  // A confirmação mostra o resumo de cada um, antes de rodar qualquer coisa.
  await expect(lote.getByText('Aprovar e executar 3 pedidos?')).toBeVisible()
  for (const p of [P1, P2, P3]) await expect(lote.getByText(p.preview.resumo)).toBeVisible()
  expect(s.ordem).toEqual([])

  await lote.getByRole('button', { name: 'Confirmar e executar 3' }).click()
  await expect(page.getByText('3 pedidos executados', { exact: true })).toBeVisible()
  // Um por vez, do mais antigo para o mais novo, cada um com a versão que a pessoa leu.
  expect(s.ordem).toEqual([P1.id, P2.id, P3.id])
  expect(s.corpos).toEqual([{ tentativa: 0 }, { tentativa: 2 }, { tentativa: 0 }])
  // Os cartões viram o desfecho e continuam no grupo da campanha (a resposta não traz a campanha).
  await expect(verao.getByText('Já foi executada.')).toHaveCount(3)
  await expect(page.getByRole('region', { name: 'Decidir em lote' })).toHaveCount(0)
})

test('o lote não é tudo-ou-nada: o que o servidor recusou fica pendente com o motivo e o resto segue', async ({ page }) => {
  const s = await servidor(page, [P3, P2, P1], {
    aprovar: (id) => id === P2.id
      ? [422, { error: 'preview_desatualizado', mensagem: 'O pedido mudou desde que você leu.', pendente: { ...P2, tentativa: 3, preview: { ...P2.preview, resumo: 'Resumo NOVO' } } }]
      : [200, {}],
  })
  await page.goto('/pendentes')
  await page.getByRole('checkbox', { name: 'Selecionar todos os pedidos de Verão 2026' }).check()
  const lote = page.getByRole('region', { name: 'Decidir em lote' })
  await lote.getByRole('button', { name: /Aprovar selecionados/ }).click()
  await lote.getByRole('button', { name: /Confirmar e executar/ }).click()

  await expect(page.getByText('2 pedidos executados', { exact: true })).toBeVisible()
  await expect(page.getByText('1 não passou: leia no cartão.')).toBeVisible()
  expect(s.ordem).toEqual([P1.id, P2.id, P3.id])
  const recusado = page.getByRole('article').filter({ hasText: 'O pedido mudou desde que você leu.' })
  await expect(recusado.getByRole('alert')).toBeVisible()
  await expect(recusado.getByRole('button', { name: /Aprovar/ })).toBeEnabled()
})

test('para no primeiro sem resposta: não dispara os seguintes às cegas', async ({ page }) => {
  const s = await servidor(page, [P3, P2, P1], { aprovar: (id) => (id === P1.id ? 'abortar' : [200, {}]) })
  await page.goto('/pendentes')
  await page.getByRole('checkbox', { name: 'Selecionar todos os pedidos de Verão 2026' }).check()
  const lote = page.getByRole('region', { name: 'Decidir em lote' })
  await lote.getByRole('button', { name: /Aprovar selecionados/ }).click()
  await lote.getByRole('button', { name: /Confirmar e executar/ }).click()

  await expect(page.getByText(/2 não foram tentados/)).toBeVisible()
  expect(s.ordem).toEqual([P1.id])
})

test('recusar em lote pede confirmação e recusa só os marcados', async ({ page }) => {
  const s = await servidor(page, [P3, P2, P1])
  await page.goto('/pendentes')
  await page.getByRole('checkbox', { name: `Selecionar: ${P1.preview.resumo}` }).check()
  await page.getByRole('checkbox', { name: `Selecionar: ${P3.preview.resumo}` }).check()
  // Marcar parte do grupo deixa a caixa do grupo em "alguns".
  await expect(page.getByRole('checkbox', { name: 'Selecionar todos os pedidos de Verão 2026' })).toHaveJSProperty('indeterminate', true)

  const lote = page.getByRole('region', { name: 'Decidir em lote' })
  await lote.getByRole('button', { name: 'Recusar selecionados' }).click()
  await expect(lote.getByText('Recusar 2 pedidos?')).toBeVisible()
  await lote.getByRole('button', { name: 'Confirmar recusa de 2' }).click()

  await expect(page.getByText('2 pedidos recusados', { exact: true })).toBeVisible()
  expect(s.ordem).toEqual([P1.id, P3.id])
})

test('com um pedido só não há seleção nem barra de lote', async ({ page }) => {
  await servidor(page, [P1])
  await page.goto('/pendentes')
  await expect(page.getByRole('article')).toHaveCount(1)
  await expect(page.getByRole('checkbox')).toHaveCount(0)
})
