import { test, expect } from '@playwright/test'

/**
 * /pendentes — aprovação, pela pessoa, dos pedidos `critico` que a IA criou
 * pelo MCP (spec 2026-10-27 §4.3). A API é falseada com page.route: o que se
 * prova é a TELA. A fila, a aprovação e as recusas estão em
 * workers/internal/mcp/*_test.go.
 *
 * Capturas de tela: com E2E_SHOTS_DIR definido, alguns testes salvam um PNG lá.
 */

const LISTA = '**/v1/internal/mcp/pendentes*' // GET /pendentes e /pendentes?id=
const APROVAR = '**/v1/internal/mcp/pendentes/*/aprovar'
const RECUSAR = '**/v1/internal/mcp/pendentes/*/recusar'
const ME = '**/v1/internal/auth/me'
const CLIENTES = '**/v1/internal/clients*'
const TOKENS = '**/v1/internal/mcp/tokens'

const ID_A = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
const ID_B = 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb'
const SHOTS = process.env.E2E_SHOTS_DIR

const ADMIN = { id: '00000000-0000-0000-0000-000000000009', name: 'QA Admin', email: 'qa@teste.local', role: 'admin', client_id: null, client_ids: [], is_active: true, created_at: '2026-01-01T00:00:00Z' }
const CLIENTE = { id: '00000000-0000-0000-0000-000000000010', name: 'QA Cliente', email: 'cliente@teste.local', role: 'viewer', client_id: '00000000-0000-0000-0000-0000000000c1', client_ids: [], is_active: true, created_at: '2026-01-01T00:00:00Z' }

function json(route, status, body) {
  return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

/** Um pedido como a API o devolve. expira_em fica longe: o relógio do navegador não o vence. */
function pedido(over = {}) {
  return {
    id: ID_A,
    tool: 'cancelar_campanha',
    motivo: 'Cancelar uma campanha em andamento desliga os workers e muda a cobrança.',
    preview: {
      resumo: 'Cancelar a campanha "FSJ - Pedido Jack"',
      mudancas: [{ campo: 'estado', de: 'ativa', para: 'cancelada' }],
      efeitos: ['Os workers das emissoras são desligados.'],
      ressalvas: ['A campanha já tem veiculações registradas.'],
    },
    estado: 'pendente',
    // a versão do pedido: quem aprova a devolve, e o servidor só aprova se ainda for a vigente
    tentativa: 0,
    criada_em: '2026-11-03T15:00:00Z',
    expira_em: '2099-03-10T15:30:00Z',
    aprovada_em: null,
    executada_em: null,
    recusada_em: null,
    erro: null,
    ressalvas: [],
    ...over,
  }
}

const OUTRO = pedido({
  id: ID_B,
  tool: 'editar_regra_distribuicao',
  motivo: 'Mexe na grade que o cliente paga.',
  preview: {
    resumo: 'Trocar a meta do dia 12/11 da campanha "Verão"',
    mudancas: [
      { campo: 'plays_expected', de: 3, para: 5 },
      { campo: 'observacao', de: null, para: 'Reforço de fim de semana' },
      { campo: 'bonificada', de: false, para: true },
    ],
    efeitos: [],
    ressalvas: [],
  },
})

async function sessao(page, usuario = ADMIN) {
  await page.addInitScript((u) => {
    sessionStorage.setItem('rc_token', 'jwt.de.teste')
    sessionStorage.setItem('rc_user', JSON.stringify(u))
  }, usuario)
  await page.route(ME, r => json(r, 200, usuario))
}

/**
 * Falseia a fila. `lista(url)` devolve o corpo do GET; `aprovar`/`recusar`
 * devolvem [status, corpo]. Conta as chamadas para provar que a tela recarrega.
 */
async function servidor(page, { lista, aprovar, recusar }) {
  const chamadas = { lista: [], aprovar: 0, recusar: 0, corpos: [] }
  await page.route(LISTA, r => {
    const url = new URL(r.request().url())
    chamadas.lista.push(url.searchParams.get('id'))
    return json(r, 200, lista(url))
  })
  await page.route(APROVAR, r => {
    chamadas.aprovar++
    chamadas.corpos.push(r.request().postDataJSON())
    const [status, corpo] = aprovar()
    return json(r, status, corpo)
  })
  await page.route(RECUSAR, r => {
    chamadas.recusar++
    const [status, corpo] = recusar()
    return json(r, status, corpo)
  })
  return chamadas
}

const semAcao = () => { throw new Error('esta rota não deveria ser chamada') }

async function foto(page, nome) {
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/${nome}.png`, fullPage: true })
}

test('lista os pedidos com resumo, motivo, mudanças, efeitos e prazo em horário de SP', async ({ page }) => {
  await sessao(page)
  await servidor(page, { lista: () => ({ items: [pedido(), OUTRO], destaque: null }), aprovar: semAcao, recusar: semAcao })
  await page.goto('/pendentes')

  await expect(page.getByRole('heading', { name: 'Pedidos da IA' })).toBeVisible()
  const cartoes = page.getByRole('article')
  await expect(cartoes).toHaveCount(2)

  const a = cartoes.nth(0)
  await expect(a.getByText('Cancelar a campanha "FSJ - Pedido Jack"')).toBeVisible()
  await expect(a.getByText('Cancelar uma campanha em andamento desliga os workers e muda a cobrança.')).toBeVisible()
  await expect(a.getByText('estado', { exact: true })).toBeVisible()
  await expect(a.getByText('ativa', { exact: true })).toBeVisible()
  await expect(a.getByText('cancelada', { exact: true })).toBeVisible()
  await expect(a.getByText('Os workers das emissoras são desligados.')).toBeVisible()
  await expect(a.getByText('A campanha já tem veiculações registradas.')).toBeVisible()
  // 15:30 UTC = 12:30 em SP (UTC-3): o prazo não pode sair no fuso do navegador.
  await expect(a.getByText(/Expira em 10\/03\/2099.*12:30/)).toBeVisible()
  await expect(a.getByRole('button', { name: /Aprovar/ })).toBeEnabled()
  await expect(a.getByRole('button', { name: 'Recusar' })).toBeEnabled()

  const b = cartoes.nth(1)
  await expect(b.getByText('Trocar a meta do dia 12/11 da campanha "Verão"')).toBeVisible()
  await expect(b.getByText('plays_expected', { exact: true })).toBeVisible()
  await expect(b.getByText('3', { exact: true })).toBeVisible()
  await expect(b.getByText('5', { exact: true })).toBeVisible()
  await expect(b.getByText('—', { exact: true })).toBeVisible() // de: null
  await expect(b.getByText('Reforço de fim de semana', { exact: true })).toBeVisible()
  await expect(b.getByText('não', { exact: true })).toBeVisible() // de: false
  await expect(b.getByText('sim', { exact: true })).toBeVisible() // para: true
  await foto(page, 'pendentes-lista')
})

test('o texto do preview é texto, não HTML', async ({ page }) => {
  await sessao(page)
  const hostil = pedido({ preview: { resumo: '<img src=x onerror="window.__pwned=1"> Cancelar', mudancas: [], efeitos: ['<b>negrito</b>'], ressalvas: [] } })
  await servidor(page, { lista: () => ({ items: [hostil], destaque: null }), aprovar: semAcao, recusar: semAcao })
  await page.goto('/pendentes')
  await expect(page.getByText('<img src=x onerror="window.__pwned=1"> Cancelar')).toBeVisible()
  await expect(page.getByText('<b>negrito</b>')).toBeVisible()
  expect(await page.evaluate(() => window.__pwned)).toBeUndefined()
})

test('?id= de um pedido já executado destaca o cartão, sem botões, com as ressalvas da execução', async ({ page }) => {
  await sessao(page)
  const executado = pedido({
    estado: 'executada',
    aprovada_em: '2026-11-03T15:10:00Z',
    executada_em: '2026-11-03T15:10:02Z',
    ressalvas: ['O email não saiu: a pessoa não tem a senha. Redefina pela tela de usuários.'],
  })
  const chamadas = await servidor(page, { lista: () => ({ items: [], destaque: executado }), aprovar: semAcao, recusar: semAcao })
  await page.goto(`/pendentes?id=${ID_A}`)

  const cartao = page.getByRole('article')
  await expect(cartao).toHaveCount(1)
  await expect(cartao).toHaveAttribute('aria-current', 'true')
  await expect(cartao.getByText('Já foi executada.')).toBeVisible()
  await expect(cartao.getByText('O email não saiu: a pessoa não tem a senha. Redefina pela tela de usuários.')).toBeVisible()
  await expect(cartao.getByRole('button')).toHaveCount(0)
  expect(chamadas.lista[0]).toBe(ID_A)
  await foto(page, 'pendentes-executada-com-ressalvas')
})

test('aprovar chama a API, avisa "Ação executada", recarrega e mostra o desfecho com as ressalvas', async ({ page }) => {
  await sessao(page)
  const ressalva = 'O email não saiu: a pessoa não tem a senha. Redefina pela tela de usuários.'
  let executou = false
  const chamadas = await servidor(page, {
    lista: (url) => {
      if (!executou) return { items: [pedido(), OUTRO], destaque: null }
      const fechado = pedido({ estado: 'executada', executada_em: '2026-11-03T15:10:02Z', ressalvas: [ressalva] })
      return { items: [OUTRO], destaque: url.searchParams.get('id') === ID_A ? fechado : null }
    },
    aprovar: () => { executou = true; return [200, { pendente: pedido({ estado: 'executada', ressalvas: [ressalva] }), resultado: { id: 'x' }, ressalvas: [ressalva] }] },
    recusar: semAcao,
  })
  await page.goto('/pendentes')
  const a = page.getByRole('article').filter({ hasText: 'FSJ - Pedido Jack' })
  await a.getByRole('button', { name: /Aprovar/ }).click()

  await expect(page.getByText('Ação executada', { exact: true })).toBeVisible()
  expect(chamadas.aprovar).toBe(1)
  expect(chamadas.recusar).toBe(0)
  // O POST carrega a versão (tentativa) do pedido que o cartão mostrava
  expect(chamadas.corpos).toEqual([{ tentativa: 0 }])
  // A lista recarregou: o cartão virou o desfecho (fechado, sem botões) e as ressalvas seguem na tela.
  const fechado = page.getByRole('article').filter({ hasText: 'FSJ - Pedido Jack' })
  await expect(fechado.getByText('Já foi executada.')).toBeVisible()
  await expect(fechado.getByText(ressalva)).toBeVisible()
  await expect(fechado.getByRole('button')).toHaveCount(0)
  await expect(page.getByRole('article')).toHaveCount(2)
  expect(chamadas.lista.length).toBeGreaterThanOrEqual(2)
  await foto(page, 'pendentes-depois-de-aprovar')
})

test('enquanto aprova, os botões do pedido ficam desabilitados', async ({ page }) => {
  await sessao(page)
  let solta
  const segura = new Promise(r => { solta = r })
  await page.route(LISTA, r => json(r, 200, { items: [pedido(), OUTRO], destaque: null }))
  await page.route(APROVAR, async r => { await segura; return json(r, 200, { pendente: pedido({ estado: 'executada' }), resultado: null, ressalvas: [] }) })
  await page.goto('/pendentes')
  const cartoes = page.getByRole('article')
  await cartoes.nth(0).getByRole('button', { name: /Aprovar/ }).click()
  await expect(cartoes.nth(0).getByRole('button', { name: 'Recusar' })).toBeDisabled()
  await expect(cartoes.nth(1).getByRole('button', { name: /Aprovar/ })).toBeDisabled()
  await expect(cartoes.nth(1).getByRole('button', { name: 'Recusar' })).toBeDisabled()
  solta()
})

test('recusa de negócio (422): a mensagem do servidor aparece e o pedido segue com os botões', async ({ page }) => {
  await sessao(page)
  const msg = 'A campanha já foi cancelada por outra pessoa.'
  const chamadas = await servidor(page, {
    lista: () => ({ items: [pedido({ erro: msg })], destaque: null }),
    aprovar: () => [422, { error: 'campanha_cancelada', mensagem: msg, pendente: pedido({ tentativa: 1, erro: msg }) }],
    recusar: semAcao,
  })
  await page.goto('/pendentes')
  const a = page.getByRole('article')
  await a.getByRole('button', { name: /Aprovar/ }).click()

  await expect(a.getByRole('alert')).toContainText(msg)
  await expect(a.getByRole('button', { name: /Aprovar/ })).toBeEnabled()
  await expect(a.getByRole('button', { name: 'Recusar' })).toBeEnabled()
  expect(chamadas.aprovar).toBe(1)
})

test('preview_desatualizado (422): mostra a mensagem, recarrega e o cartão traz o resumo NOVO', async ({ page }) => {
  await sessao(page)
  const msg = 'O estado mudou desde o pedido; releia o resumo e aprove de novo.'
  const velho = pedido({ preview: { resumo: 'Cancelar a campanha "FSJ" (12 dias de veiculação)', mudancas: [], efeitos: [], ressalvas: [] } })
  const novo = pedido({ tentativa: 1, erro: msg, preview: { resumo: 'Cancelar a campanha "FSJ" (13 dias de veiculação)', mudancas: [{ campo: 'dias_veiculados', de: 12, para: 13 }], efeitos: [], ressalvas: [] } })
  let desatualizou = false
  const chamadas = await servidor(page, {
    lista: () => ({ items: [desatualizou ? novo : velho], destaque: null }),
    aprovar: () => { desatualizou = true; return [422, { error: 'preview_desatualizado', mensagem: msg, pendente: novo }] },
    recusar: semAcao,
  })
  await page.goto('/pendentes')
  const a = page.getByRole('article')
  await expect(a.getByText('12 dias de veiculação')).toBeVisible()
  await a.getByRole('button', { name: /Aprovar/ }).click()

  await expect(a.getByRole('alert')).toContainText(msg)
  await expect(a.getByText('13 dias de veiculação')).toBeVisible()
  await expect(a.getByText('12 dias de veiculação')).toHaveCount(0)
  await expect(a.getByText('dias_veiculados', { exact: true })).toBeVisible()
  await expect(a.getByRole('button', { name: /Aprovar/ })).toBeEnabled()
  expect(chamadas.lista.length).toBeGreaterThanOrEqual(2)
  await foto(page, 'pendentes-preview-desatualizado')
})

test('execução interrompida (500): mostra a mensagem do servidor e o cartão fica "falhou", sem botões', async ({ page }) => {
  await sessao(page)
  const msg = 'A campanha foi cancelada mas NÃO foi apagada: confira o estado dela antes de pedir de novo.'
  let falhou = false
  await servidor(page, {
    lista: (url) => falhou && url.searchParams.get('id') === ID_A
      ? { items: [], destaque: pedido({ estado: 'falhou', erro: msg }) }
      : { items: falhou ? [] : [pedido()], destaque: null },
    aprovar: () => { falhou = true; return [500, { error: 'interrompida', mensagem: msg }] },
    recusar: semAcao,
  })
  await page.goto('/pendentes')
  const a = page.getByRole('article')
  await a.getByRole('button', { name: /Aprovar/ }).click()

  await expect(a.getByText(msg)).toBeVisible()
  await expect(a.getByText('A execução foi interrompida', { exact: false })).toBeVisible()
  await expect(a.getByRole('button')).toHaveCount(0)
  await foto(page, 'pendentes-falhou')
})

test('pedido que mudou de estado por baixo (409): explica e mostra o estado real', async ({ page }) => {
  await sessao(page)
  let fechou = false
  await servidor(page, {
    lista: (url) => fechou
      ? { items: [], destaque: url.searchParams.get('id') === ID_A ? pedido({ estado: 'executada' }) : null }
      : { items: [pedido()], destaque: null },
    aprovar: () => { fechou = true; return [409, { error: 'pendente_executada', mensagem: 'Este pedido já foi executado.' }] },
    recusar: semAcao,
  })
  await page.goto('/pendentes')
  const a = page.getByRole('article')
  await a.getByRole('button', { name: /Aprovar/ }).click()
  await expect(a.getByText('Este pedido já foi executado.')).toBeVisible()
  await expect(a.getByText('Já foi executada.')).toBeVisible()
  await expect(a.getByRole('button')).toHaveCount(0)
})

test('recusar chama a API de recusa, avisa "Pedido recusado" e recarrega', async ({ page }) => {
  await sessao(page)
  let recusou = false
  const chamadas = await servidor(page, {
    lista: (url) => recusou
      ? { items: [OUTRO], destaque: url.searchParams.get('id') === ID_A ? pedido({ estado: 'recusada', recusada_em: '2026-11-03T15:11:00Z' }) : null }
      : { items: [pedido(), OUTRO], destaque: null },
    aprovar: semAcao,
    recusar: () => { recusou = true; return [200, { pendente: pedido({ estado: 'recusada' }) }] },
  })
  await page.goto('/pendentes')
  await page.getByRole('article').filter({ hasText: 'FSJ - Pedido Jack' }).getByRole('button', { name: 'Recusar' }).click()

  await expect(page.getByText('Pedido recusado', { exact: true })).toBeVisible()
  expect(chamadas.recusar).toBe(1)
  expect(chamadas.aprovar).toBe(0)
  await expect(page.getByRole('article').filter({ hasText: 'FSJ - Pedido Jack' }).getByText('Você recusou este pedido.')).toBeVisible()
  expect(chamadas.lista.length).toBeGreaterThanOrEqual(2)
})

test('lista vazia mostra o estado vazio', async ({ page }) => {
  await sessao(page)
  await servidor(page, { lista: () => ({ items: [], destaque: null }), aprovar: semAcao, recusar: semAcao })
  await page.goto('/pendentes')
  await expect(page.getByText('Nenhum pedido esperando você')).toBeVisible()
  await expect(page.getByRole('article')).toHaveCount(0)
  await foto(page, 'pendentes-vazio')
})

test('pedido pendente com o prazo vencido no relógio do navegador vira "expirada", sem botões', async ({ page }) => {
  await sessao(page)
  await servidor(page, { lista: () => ({ items: [pedido({ expira_em: '2020-01-01T12:00:00Z' })], destaque: null }), aprovar: semAcao, recusar: semAcao })
  await page.goto('/pendentes')
  const a = page.getByRole('article')
  await expect(a.getByText('Expirou sem aprovação. Peça de novo à IA.')).toBeVisible()
  await expect(a.getByRole('button')).toHaveCount(0)
})

test('pedido aprovado e ainda rodando mostra "Executando…", sem botões', async ({ page }) => {
  await sessao(page)
  await servidor(page, { lista: () => ({ items: [pedido({ estado: 'aprovada', aprovada_em: '2026-11-03T15:10:00Z' })], destaque: null }), aprovar: semAcao, recusar: semAcao })
  await page.goto('/pendentes')
  const a = page.getByRole('article')
  await expect(a.getByText(/Executando…/)).toBeVisible()
  await expect(a.getByRole('button')).toHaveCount(0)
})

test('falha ao carregar a lista oferece tentar de novo', async ({ page }) => {
  await sessao(page)
  let falha = true
  await page.route(LISTA, r => falha ? json(r, 500, { error: 'interno', mensagem: 'Erro interno.' }) : json(r, 200, { items: [pedido()], destaque: null }))
  await page.goto('/pendentes')
  await expect(page.getByText('Os pedidos não carregaram')).toBeVisible()
  falha = false
  await page.getByRole('button', { name: 'Tentar de novo' }).click()
  await expect(page.getByRole('article')).toHaveCount(1)
})

test('dentro de um iframe: pede para abrir numa aba própria e não mostra botões nem chama a API', async ({ page }) => {
  await sessao(page)
  const chamadas = await servidor(page, { lista: () => ({ items: [pedido()], destaque: null }), aprovar: semAcao, recusar: semAcao })
  await page.route('**/moldura-de-teste', r => r.fulfill({ status: 200, contentType: 'text/html', body: '<!doctype html><iframe src="/pendentes" style="width:900px;height:600px"></iframe>' }))
  await page.goto('/moldura-de-teste')
  const quadro = page.frameLocator('iframe')
  await expect(quadro.getByText('Abra numa aba própria')).toBeVisible()
  // O resto do app (menu lateral) também está no quadro; o que não pode estar é a decisão.
  await expect(quadro.getByRole('button', { name: /Aprovar|Recusar/ })).toHaveCount(0)
  await expect(quadro.getByRole('article')).toHaveCount(0)
  expect(chamadas.lista).toHaveLength(0)
})

test('usuário cliente não acessa a rota (volta para /campaigns) e nada é pedido à API', async ({ page }) => {
  await sessao(page, CLIENTE)
  const chamadas = await servidor(page, { lista: () => ({ items: [], destaque: null }), aprovar: semAcao, recusar: semAcao })
  await page.goto('/pendentes')
  await expect(page).toHaveURL(/\/campaigns/)
  await expect(page.getByRole('heading', { name: 'Pedidos da IA' })).toHaveCount(0)
  expect(chamadas.lista).toHaveLength(0)
})

test('em Minha conta, "Pedidos da IA" mostra quantos esperam e leva à fila', async ({ page }) => {
  await sessao(page)
  await page.route(CLIENTES, r => json(r, 200, { data: [] }))
  await page.route(TOKENS, r => json(r, 200, { items: [] }))
  await servidor(page, {
    // um vencido pelo relógio não conta: não dá para aprovar
    lista: () => ({ items: [pedido(), OUTRO, pedido({ id: 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', expira_em: '2020-01-01T00:00:00Z' })], destaque: null }),
    aprovar: semAcao, recusar: semAcao,
  })
  await page.goto('/account')
  const secao = page.getByRole('region', { name: 'Integração com IA' })
  const link = secao.getByRole('link', { name: /Pedidos da IA/ })
  await expect(link).toContainText('2')
  await link.click()
  await expect(page).toHaveURL(/\/pendentes$/)
  await expect(page.getByRole('heading', { name: 'Pedidos da IA' })).toBeVisible()
})

test('aprovar manda a tentativa do cartão que está na tela (a do pedido que a pessoa leu)', async ({ page }) => {
  await sessao(page)
  const chamadas = await servidor(page, {
    lista: () => ({ items: [pedido({ tentativa: 3 }), pedido({ id: ID_B, tentativa: 5, tool: 'editar_regra_distribuicao' })], destaque: null }),
    aprovar: () => [200, { pendente: pedido({ estado: 'executada' }), resultado: null, ressalvas: [] }],
    recusar: semAcao,
  })
  await page.goto('/pendentes')
  await page.getByRole('article').nth(1).getByRole('button', { name: /Aprovar/ }).click()
  await expect(page.getByText('Ação executada', { exact: true })).toBeVisible()
  expect(chamadas.corpos).toEqual([{ tentativa: 5 }])
})

test('422 com `pendente`: o cartão troca na hora, mesmo que a releitura da lista falhe, e o próximo clique manda a versão nova', async ({ page }) => {
  await sessao(page)
  const msg = 'O estado mudou desde o pedido; releia o resumo e aprove de novo.'
  const velho = pedido({ tentativa: 0, preview: { resumo: 'Cancelar a campanha "FSJ" (12 dias de veiculação)', mudancas: [], efeitos: [], ressalvas: [] } })
  const novo = pedido({ tentativa: 1, erro: msg, preview: { resumo: 'Cancelar a campanha "FSJ" (13 dias de veiculação)', mudancas: [{ campo: 'dias_veiculados', de: 12, para: 13 }], efeitos: [], ressalvas: [] } })
  let aprovou = false
  await page.route(LISTA, r => aprovou ? json(r, 500, { error: 'interno', mensagem: 'Erro interno.' }) : json(r, 200, { items: [velho], destaque: null }))
  const corpos = []
  let resposta = [422, { error: 'preview_desatualizado', mensagem: msg, pendente: novo }]
  await page.route(APROVAR, r => { aprovou = true; corpos.push(r.request().postDataJSON()); return json(r, resposta[0], resposta[1]) })

  await page.goto('/pendentes')
  const a = page.getByRole('article')
  await expect(a.getByText('12 dias de veiculação')).toBeVisible()
  await a.getByRole('button', { name: /Aprovar/ }).click()

  // O 422 já trouxe o pedido novo: a tela o mostra sem depender da releitura (que está falhando)
  await expect(a.getByText('13 dias de veiculação')).toBeVisible()
  await expect(a.getByText('12 dias de veiculação')).toHaveCount(0)
  await expect(a.getByRole('alert')).toContainText(msg)
  await expect(page.getByText('Não foi possível atualizar a lista')).toBeVisible()

  // Quem aprova agora aprova o resumo novo: o corpo leva a tentativa 1, não a 0
  resposta = [200, { pendente: pedido({ estado: 'executada', tentativa: 2 }), resultado: null, ressalvas: [] }]
  await a.getByRole('button', { name: /Aprovar/ }).click()
  await expect(page.getByText('Ação executada', { exact: true })).toBeVisible()
  expect(corpos).toEqual([{ tentativa: 0 }, { tentativa: 1 }])
})

test('409 pendente_mudou: a aba com o resumo velho não aprova; avisa, relê e mostra o resumo novo', async ({ page }) => {
  await sessao(page)
  const msg = 'O pedido mudou desde que você abriu; releia antes de aprovar.'
  const velho = pedido({ tentativa: 0, preview: { resumo: 'Cancelar a campanha "FSJ" (12 dias de veiculação)', mudancas: [], efeitos: [], ressalvas: [] } })
  const novo = pedido({ tentativa: 1, erro: 'O estado mudou desde o pedido; releia o resumo e aprove de novo.', preview: { resumo: 'Cancelar a campanha "FSJ" (13 dias de veiculação)', mudancas: [], efeitos: [], ressalvas: [] } })
  let mudou = false
  const chamadas = await servidor(page, {
    lista: (url) => {
      const pedidoAgora = mudou ? novo : velho
      return { items: [pedidoAgora], destaque: url.searchParams.get('id') === ID_A ? pedidoAgora : null }
    },
    // a outra aba aprovou e gravou o resumo novo (tentativa 1) antes deste clique
    aprovar: () => { mudou = true; return [409, { error: 'pendente_mudou', mensagem: msg }] },
    recusar: semAcao,
  })
  await page.goto('/pendentes')
  const a = page.getByRole('article')
  await expect(a.getByText('12 dias de veiculação')).toBeVisible()
  await a.getByRole('button', { name: /Aprovar/ }).click()

  await expect(a.getByRole('alert')).toContainText('O pedido mudou: releia antes de aprovar')
  await expect(a.getByRole('alert')).toContainText(msg)
  await expect(a.getByText('13 dias de veiculação')).toBeVisible()
  await expect(a.getByText('12 dias de veiculação')).toHaveCount(0)
  await expect(a.getByRole('button', { name: /Aprovar/ })).toBeEnabled()
  expect(chamadas.corpos).toEqual([{ tentativa: 0 }])
})

test('409 troca ?id= sem piscar o esqueleto: a tela anterior fica até a nova chegar', async ({ page }) => {
  await sessao(page)
  let fechou = false
  await page.route(LISTA, async r => {
    const url = new URL(r.request().url())
    if (url.searchParams.get('id')) await new Promise(res => setTimeout(res, 700)) // a releitura do link demora
    return json(r, 200, fechou
      ? { items: [], destaque: url.searchParams.get('id') === ID_A ? pedido({ estado: 'executada' }) : null }
      : { items: [pedido()], destaque: null })
  })
  await page.route(APROVAR, r => { fechou = true; return json(r, 409, { error: 'pendente_executada', mensagem: 'Este pedido já foi executado.' }) })
  await page.goto('/pendentes')
  const a = page.getByRole('article')
  await expect(a).toHaveCount(1)
  await a.getByRole('button', { name: /Aprovar/ }).click()

  // Enquanto a releitura do link não chega: nada de "Carregando…" nem de "Não encontramos o pedido deste link"
  await expect(page.getByText('Este pedido já foi executado.')).toBeVisible()
  await expect(page.getByText('Carregando…')).toHaveCount(0)
  await expect(page.getByText('Não encontramos o pedido deste link')).toHaveCount(0)
  await expect(a).toHaveCount(1)
  await expect(a.getByText('Já foi executada.')).toBeVisible()
  await expect(page.getByText('Não encontramos o pedido deste link')).toHaveCount(0)
})

test('aprovar sem resposta (a rede caiu): não diz "Não foi feito"; diz que não sabe e relê a fila', async ({ page }) => {
  await sessao(page)
  const chamadas = { lista: 0 }
  await page.route(LISTA, r => { chamadas.lista++; return json(r, 200, { items: [pedido()], destaque: null }) })
  await page.route(APROVAR, r => r.abort('failed'))
  await page.goto('/pendentes')
  const a = page.getByRole('article')
  await expect(a).toHaveCount(1)
  const antes = chamadas.lista
  await a.getByRole('button', { name: /Aprovar/ }).click()

  await expect(a.getByRole('alert')).toContainText('Não sabemos se foi feito')
  await expect(a.getByRole('alert')).toContainText('Recarregue a página')
  await expect(a.getByRole('alert')).not.toContainText('Não foi feito')
  // a fila foi relida para mostrar o estado real
  await expect.poll(() => chamadas.lista).toBeGreaterThan(antes)
})

test('aprovar e o Cloudflare responde 524 em HTML: também é "não sabemos se foi feito"', async ({ page }) => {
  await sessao(page)
  await page.route(LISTA, r => json(r, 200, { items: [pedido()], destaque: null }))
  await page.route(APROVAR, r => r.fulfill({ status: 524, contentType: 'text/html', body: '<html><body>A timeout occurred</body></html>' }))
  await page.goto('/pendentes')
  const a = page.getByRole('article')
  await a.getByRole('button', { name: /Aprovar/ }).click()
  await expect(a.getByRole('alert')).toContainText('Não sabemos se foi feito')
  await expect(a.getByRole('alert')).not.toContainText('Não foi feito')
})

test('503 conferencia_falhou é "nada foi feito": o rótulo segue "Não foi feito"', async ({ page }) => {
  await sessao(page)
  await servidor(page, {
    lista: () => ({ items: [pedido()], destaque: null }),
    aprovar: () => [503, { error: 'conferencia_falhou', mensagem: 'Não consegui conferir o pedido agora; nada foi feito. Tente de novo em instantes.' }],
    recusar: semAcao,
  })
  await page.goto('/pendentes')
  const a = page.getByRole('article')
  await a.getByRole('button', { name: /Aprovar/ }).click()
  await expect(a.getByRole('alert')).toContainText('Não foi feito')
  await expect(a.getByRole('alert')).toContainText('nada foi feito')
  await expect(a.getByRole('alert')).not.toContainText('Não sabemos')
})

test('pedido expirado porque a conexão de IA foi revogada mostra o motivo', async ({ page }) => {
  await sessao(page)
  const motivo = 'A conexão de IA que fez este pedido foi revogada; peça de novo por uma conexão válida.'
  await servidor(page, {
    lista: () => ({ items: [], destaque: pedido({ estado: 'expirada', erro: motivo }) }),
    aprovar: semAcao,
    recusar: semAcao,
  })
  await page.goto(`/pendentes?id=${ID_A}`)
  const a = page.getByRole('article')
  await expect(a.getByText(motivo)).toBeVisible()
  await expect(a.getByRole('button')).toHaveCount(0)
})

test('voltar para a aba relê a fila (só esta tela relê no foco): o resumo mudou enquanto ela ficou aberta', async ({ page }) => {
  await sessao(page)
  let mudou = false
  const chamadas = await servidor(page, {
    lista: () => ({ items: [mudou ? pedido({ tentativa: 1, preview: { resumo: 'Resumo NOVO', mudancas: [], efeitos: [], ressalvas: [] } }) : pedido()], destaque: null }),
    aprovar: semAcao,
    recusar: semAcao,
  })
  await page.goto('/pendentes')
  await expect(page.getByText('Cancelar a campanha "FSJ - Pedido Jack"')).toBeVisible()
  const antes = chamadas.lista.length
  mudou = true
  // a aba volta a ficar visível: o TanStack escuta o visibilitychange na window (o evento sobe do document)
  await page.evaluate(() => {
    const def = (v) => Object.defineProperty(document, 'visibilityState', { value: v, configurable: true })
    def('hidden'); document.dispatchEvent(new Event('visibilitychange', { bubbles: true }))
    def('visible'); document.dispatchEvent(new Event('visibilitychange', { bubbles: true }))
  })
  await expect(page.getByText('Resumo NOVO')).toBeVisible()
  expect(chamadas.lista.length).toBeGreaterThan(antes)
})

test('texto do modelo com controle de direção: cada valor é isolado (dir="auto" + unicode-bidi: isolate)', async ({ page }) => {
  await sessao(page)
  const rlo = '‮'
  const hostil = pedido({
    preview: {
      resumo: `Trocar ${rlo}001 R$`,
      mudancas: [{ campo: 'valor', de: `10${rlo}`, para: `${rlo}100` }],
      efeitos: [`Afeta ${rlo}2 usuários`],
      ressalvas: [],
    },
  })
  await servidor(page, { lista: () => ({ items: [hostil], destaque: null }), aprovar: semAcao, recusar: semAcao })
  await page.goto('/pendentes')
  const a = page.getByRole('article')
  for (const sel of ['.pen-resumo', '.pen-de', '.pen-para', '.pen-itens li']) {
    const el = a.locator(sel).first()
    await expect(el).toHaveAttribute('dir', 'auto')
    expect(await el.evaluate(n => getComputedStyle(n).unicodeBidi)).toMatch(/isolate/)
  }
})

test('o aviso (toast) usa uma região viva que já existia antes do primeiro aviso', async ({ page }) => {
  await sessao(page)
  await servidor(page, {
    lista: () => ({ items: [pedido()], destaque: null }),
    aprovar: () => [200, { pendente: pedido({ estado: 'executada' }), resultado: null, ressalvas: [] }],
    recusar: semAcao,
  })
  await page.goto('/pendentes')
  const regiao = page.locator('.pen-toast-region')
  await expect(page.getByRole('article')).toHaveCount(1)
  // montada e vazia ANTES de qualquer aviso
  await expect(regiao).toHaveAttribute('aria-live', 'polite')
  await expect(regiao).toBeEmpty()
  const handle = await regiao.elementHandle()
  await page.getByRole('button', { name: /Aprovar/ }).click()
  await expect(regiao.getByText('Ação executada')).toBeVisible()
  // o MESMO elemento recebeu o conteúdo (não foi remontado)
  expect(await handle.evaluate(n => n.isConnected)).toBe(true)
  expect(await page.locator('.pen-toast-region').count()).toBe(1)
})
