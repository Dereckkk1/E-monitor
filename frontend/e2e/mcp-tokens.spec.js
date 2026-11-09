import { test, expect } from '@playwright/test'

const ME = '**/v1/internal/auth/me'
const CLIENTES = '**/v1/internal/clients*'
const TOKENS = '**/v1/internal/mcp/tokens'
const TOKEN = '**/v1/internal/mcp/tokens/*'

function json(route, status, body) {
  return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

const ADMIN = { id: '00000000-0000-0000-0000-000000000009', name: 'QA Admin', email: 'qa@teste.local', role: 'admin', client_id: null, client_ids: [], is_active: true, created_at: '2026-01-01T00:00:00Z' }
const CONEXAO = { id: '11111111-1111-1111-1111-111111111111', tipo: 'oauth_refresh', nome: 'Claude', client_id: 'emc_x', expira_em: '2027-01-25T00:00:00Z', ultimo_uso: null, criado_em: '2026-10-27T12:00:00Z' }

let postBody
async function abrir(page, conexoes, { falhaLista = false } = {}) {
  postBody = undefined
  await page.addInitScript((u) => {
    sessionStorage.setItem('rc_token', 'jwt.de.teste')
    sessionStorage.setItem('rc_user', JSON.stringify(u))
  }, ADMIN)
  await page.route(ME, r => json(r, 200, ADMIN))
  await page.route(CLIENTES, r => json(r, 200, { data: [] }))
  await page.route(TOKENS, r => r.request().method() === 'GET'
    ? (falhaLista ? json(r, 500, { error: 'falhou' }) : json(r, 200, { items: conexoes }))
    : (postBody = r.request().postDataJSON(), json(r, 201, { token: 'emonitor_mcp_segredo123', item: { ...CONEXAO, id: '22222222-2222-2222-2222-222222222222', tipo: 'pessoal', nome: 'Claude Code', client_id: null } })))
  await page.goto('/account')
}

test('lista conexões e mostra o endereço do MCP', async ({ page }) => {
  await abrir(page, [CONEXAO])
  const secao = page.getByRole('region', { name: 'Integração com IA' })
  await expect(secao).toBeVisible()
  await expect(secao.getByText('/mcp')).toBeVisible()
  await expect(secao.getByText('Claude', { exact: true })).toBeVisible()
  await expect(secao.getByText('Conector OAuth')).toBeVisible()
})

test('gerar token mostra o segredo uma vez', async ({ page }) => {
  await abrir(page, [])
  const secao = page.getByRole('region', { name: 'Integração com IA' })
  await secao.getByLabel('Nome do token').fill('Claude Code')
  await secao.getByRole('button', { name: 'Gerar token' }).click()
  await expect(secao.getByText('emonitor_mcp_segredo123')).toBeVisible()
  await expect(secao.getByText(/não será mostrado de novo/)).toBeVisible()
  expect(postBody).toEqual({ nome: 'Claude Code', validade_dias: 90 })
  await secao.getByRole('button', { name: 'Já copiei' }).click()
  await expect(secao.getByText('emonitor_mcp_segredo123')).toHaveCount(0)
})

test('copiar mostra Copiado e, se falhar, orienta copiar à mão', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await abrir(page, [])
  const secao = page.getByRole('region', { name: 'Integração com IA' })
  await secao.getByLabel('Nome do token').fill('X')
  await secao.getByRole('button', { name: 'Gerar token' }).click()
  await secao.getByRole('button', { name: 'Copiar' }).click()
  await expect(secao.getByRole('button', { name: 'Copiado' })).toBeVisible()
  await page.evaluate(() => { navigator.clipboard.writeText = () => Promise.reject(new Error('negado')) })
  await expect(secao.getByRole('button', { name: 'Copiar' })).toBeVisible({ timeout: 5000 })
  await secao.getByRole('button', { name: 'Copiar' }).click()
  await expect(secao.getByText('Não deu para copiar — selecione e copie à mão')).toBeVisible()
})

test('falha ao listar mostra erro', async ({ page }) => {
  await abrir(page, [], { falhaLista: true })
  const secao = page.getByRole('region', { name: 'Integração com IA' })
  await expect(secao.getByRole('alert')).toBeVisible()
})

test('revogar espera a confirmação: cancelar não apaga, confirmar apaga', async ({ page }) => {
  await abrir(page, [CONEXAO])
  let apagou = 0
  await page.route(TOKEN, r => { if (r.request().method() === 'DELETE') apagou++; return r.fulfill({ status: 204 }) })
  const botao = page.getByRole('region', { name: 'Integração com IA' }).getByRole('button', { name: 'Desconectar Claude' })

  await botao.click()
  const modal = page.getByRole('dialog')
  await expect(modal).toBeVisible()
  await expect(modal.getByText(/Desconectar "Claude"/)).toBeVisible()
  await modal.getByRole('button', { name: 'Cancelar' }).click()
  await expect(modal).toBeHidden()
  await page.waitForTimeout(500)
  expect(apagou).toBe(0)

  await botao.click()
  await page.getByRole('dialog').getByRole('button', { name: 'Confirmar' }).click()
  await expect.poll(() => apagou).toBe(1)
})
