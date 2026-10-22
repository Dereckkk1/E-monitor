import { test, expect } from '@playwright/test'

/**
 * Os estados da ligação no passo 1 do assistente (spec do hub 2026-10-22 §3.1).
 * A API é falseada com `page.route` — o que se prova é a TELA; a regra está em
 * `ligacoes_estado_test.go` e o botão em `hubcodes_ligacao_test.go`.
 *
 * Os seletores de nome, cliente e datas são os de `campanha-codigo-do-hub.spec.js`,
 * que já foram medidos contra esta tela (o react-select sem `classNamePrefix`, o
 * `pressSequentially` que abre o menu, o placeholder do nome).
 */

const ROTA_CLIENTES = '**/v1/internal/clients'
const ROTA_CODIGO = '**/v1/internal/hub-codes/*'
const ROTA_LIGAR = '**/v1/internal/hub-codes/*/ligar'
const ROTA_MATERIAIS = '**/v1/internal/clients/*/materials*'

const CLIENTE = { id: '11111111-1111-1111-1111-111111111111', name: 'LOJAS RENNER' }
const OUTRO = { id: '22222222-2222-2222-2222-222222222222', name: 'Pilecco' }

const FALTA_LIGAR = { estado: 'faltaLigar', mensagem: 'LOJAS RENNER ainda não está ligado a Lojas Renner do hub.' }

function conferencia(ligacao) {
  return {
    codigo: 'EH-7K4M2X', nome: 'Verão 2026', inicio: '2026-12-01', fim: '2027-02-28',
    cliente: { nome: 'Lojas Renner', idNaPlataforma: '' },
    ...(ligacao ? { ligacao } : {}),
  }
}

function sessao(page) {
  return page.addInitScript(() => {
    sessionStorage.setItem('rc_token', 'jwt.de.teste')
    sessionStorage.setItem('rc_user', JSON.stringify({
      id: '00000000-0000-0000-0000-000000000009', name: 'QA', email: 'qa@teste.local', role: 'admin', client_id: null,
    }))
  })
}

async function rotasBase(page) {
  await page.route(ROTA_CLIENTES, r => r.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify({ data: [CLIENTE, OUTRO] }) }))
  await page.route(ROTA_MATERIAIS, r => r.fulfill({ status: 200, contentType: 'application/json', body: '{"data":[]}' }))
}

function rotaLigar(page, ligar) {
  return page.route(ROTA_LIGAR, r => r.fulfill({
    status: ligar.status,
    contentType: ligar.texto ? 'text/plain' : 'application/json',
    body: ligar.texto ?? JSON.stringify(ligar.body),
  }))
}

const campoDoCodigo = page => page.getByLabel(/código do hub/i)

async function escolherCliente(page, nome) {
  const combo = page.getByRole('combobox').first()
  await combo.click()
  await combo.pressSequentially(nome)
  await page.keyboard.press('Enter')
}

/** Abre a criação com tudo preenchido e o código colado: o único motivo para o
 *  Avançar travar, daí em diante, é a ligação. */
async function abrir(page, { ligacao, ligar } = {}) {
  await sessao(page)
  await rotasBase(page)
  await page.route(ROTA_CODIGO, r => r.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify(conferencia(ligacao)) }))
  if (ligar) await rotaLigar(page, ligar)

  await page.goto('/campaigns/new')
  await expect(campoDoCodigo(page)).toBeVisible()
  await page.getByPlaceholder('ex: Verão 2026 — Rôgga').fill('Verão 2026')
  await escolherCliente(page, 'LOJAS')
  await expect(page.getByText('Cliente não selecionado')).toHaveCount(0)
  await page.locator('input[type="date"]').first().fill('2026-12-01')
  await page.locator('input[type="date"]').nth(1).fill('2027-02-28')
  const campo = campoDoCodigo(page)
  await campo.fill('EH-7K4M2X')
  await campo.blur()
}

const avancar = page => page.getByRole('button', { name: /avançar/i })
const botaoLigar = page => page.getByRole('button', { name: /ligar os dois clientes/i })

test('faltaLigar: âmbar, botão, Avançar travado', async ({ page }) => {
  await abrir(page, { ligacao: FALTA_LIGAR })
  await expect(page.getByText('LOJAS RENNER ainda não está ligado')).toBeVisible()
  await expect(botaoLigar(page)).toBeVisible()
  await expect(avancar(page)).toBeDisabled()
})

test('o botão leva de âmbar a verde e destrava o Avançar', async ({ page }) => {
  const corpos = []
  await abrir(page, {
    ligacao: FALTA_LIGAR,
    ligar: { status: 200, body: { ligacao: { estado: 'ligado', mensagem: '' } } },
  })
  page.on('request', r => { if (r.url().includes('/ligar')) corpos.push(r.postDataJSON()) })
  await botaoLigar(page).click()
  await expect(page.getByText(/^Confere:/)).toBeVisible()
  await expect(botaoLigar(page)).toHaveCount(0)
  await expect(avancar(page)).toBeEnabled()
  // O botão liga o cliente ESCOLHIDO, não outro qualquer.
  expect(corpos).toEqual([{ client_id: CLIENTE.id }])
})

test('ligacaoNoDuplicado também tem botão', async ({ page }) => {
  await abrir(page, { ligacao: { estado: 'ligacaoNoDuplicado', mensagem: 'A ligação está em "Giga + Internet", que não tem nada cadastrado.' } })
  await expect(page.getByText('Giga + Internet')).toBeVisible()
  await expect(botaoLigar(page)).toBeVisible()
  await expect(avancar(page)).toBeDisabled()
})

test('bloqueado: vermelho, sem botão, travado', async ({ page }) => {
  await abrir(page, { ligacao: { estado: 'bloqueado', mensagem: 'Fale com um admin antes de mudar.' } })
  await expect(page.getByText('Fale com um admin')).toBeVisible()
  await expect(botaoLigar(page)).toHaveCount(0)
  await expect(avancar(page)).toBeDisabled()
})

test('o botão que volta 409 mostra o bloqueio', async ({ page }) => {
  await abrir(page, {
    ligacao: FALTA_LIGAR,
    ligar: { status: 409, body: { ligacao: { estado: 'bloqueado', mensagem: 'O cliente "X" do hub já usa este cliente.' } } },
  })
  await botaoLigar(page).click()
  await expect(page.getByText('O cliente "X" do hub')).toBeVisible()
  await expect(botaoLigar(page)).toHaveCount(0)
  await expect(avancar(page)).toBeDisabled()
})

// O hub não confirmou: a mensagem do servidor (que pode mandar avisar o suporte)
// chega inteira, o botão continua para tentar de novo e o Avançar segue travado.
test('o botão que volta 503 mostra a mensagem e deixa tentar de novo', async ({ page }) => {
  await abrir(page, {
    ligacao: FALTA_LIGAR,
    ligar: { status: 503, body: { ligacao: { estado: 'faltaLigar', mensagem: 'O hub não confirmou a ligação. Se repetir, avise o suporte.' } } },
  })
  await botaoLigar(page).click()
  await expect(page.getByText('avise o suporte')).toBeVisible()
  await expect(botaoLigar(page)).toBeEnabled()
  await expect(avancar(page)).toBeDisabled()
})

// Erro sem `ligacao` (400/404/500 em texto puro): o aviso genérico, e a caixa
// âmbar continua como estava — nada de verde inventado.
test('o botão que volta 500 em texto puro mostra o aviso genérico', async ({ page }) => {
  await abrir(page, { ligacao: FALTA_LIGAR, ligar: { status: 500, texto: 'erro interno\n' } })
  await botaoLigar(page).click()
  /* O `ConfirmModal` desta casa substitui o `window.alert` por um modal próprio:
     quem vê a mensagem é o DOM, não `page.on('dialog')`. */
  await expect(page.getByText(/não deu para ligar agora/i)).toBeVisible({ timeout: 15000 })
  await expect(page.getByText('LOJAS RENNER ainda não está ligado')).toBeVisible()
  await expect(page.getByText(/^Confere:/)).toHaveCount(0)
})

test('ligado: verde como antes, Avançar livre', async ({ page }) => {
  await abrir(page, { ligacao: { estado: 'ligado', mensagem: '' } })
  await expect(page.getByText(/^Confere:/)).toBeVisible()
  await expect(avancar(page)).toBeEnabled()
})

test('a conferência leva o client_id', async ({ page }) => {
  const pedidos = []
  page.on('request', r => { if (r.url().includes('/hub-codes/')) pedidos.push(r.url()) })
  await abrir(page, { ligacao: { estado: 'ligado', mensagem: '' } })
  await expect(page.getByText(/^Confere:/)).toBeVisible()
  expect(pedidos.some(u => u.includes(`client_id=${CLIENTE.id}`))).toBe(true)
})

/* A ligação depende do cliente: trocá-lo depois de conferir tem de perguntar de
   novo ao servidor, com o client_id NOVO — senão a tela mostra a ligação do
   cliente anterior. */
test('trocar o cliente reconfere com o client_id novo', async ({ page }) => {
  const pedidos = []
  page.on('request', r => { if (r.url().includes('/hub-codes/')) pedidos.push(r.url()) })
  await abrir(page, { ligacao: { estado: 'ligado', mensagem: '' } })
  await expect(page.getByText(/^Confere:/)).toBeVisible()
  await escolherCliente(page, 'Pilecco')
  await expect.poll(() => pedidos.some(u => u.includes(`client_id=${OUTRO.id}`)), { timeout: 15000 }).toBe(true)
})

/* Em modo edição a conferência roda AO ABRIR: uma campanha antiga com a ligação
   quebrada mostra o problema e o botão sem a pessoa precisar entrar e sair do
   campo. */
test('na edição, a ligação quebrada aparece ao abrir', async ({ page }) => {
  const ID = 'eeeeeeee-1111-2222-3333-444444444444'
  await sessao(page)
  await rotasBase(page)
  await page.route(ROTA_CODIGO, r => r.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify(conferencia(FALTA_LIGAR)) }))
  await page.route(`**/v1/internal/campaigns/${ID}`, r => r.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify({
      id: ID, client_id: CLIENTE.id, name: 'Verão 2026',
      start_date: '2026-12-01T00:00:00Z', end_date: '2027-02-28T00:00:00Z',
      hub_code: 'EH-7K4M2X', status: 'programada', target_stations: [] }) }))

  await page.goto(`/campaigns/${ID}/edit`)
  await expect(page.getByText('LOJAS RENNER ainda não está ligado')).toBeVisible()
  await expect(botaoLigar(page)).toBeVisible()
  await expect(avancar(page)).toBeDisabled()
})
