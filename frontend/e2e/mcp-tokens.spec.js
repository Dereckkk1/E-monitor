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

async function abrir(page, conexoes) {
  await page.addInitScript((u) => {
    sessionStorage.setItem('rc_token', 'jwt.de.teste')
    sessionStorage.setItem('rc_user', JSON.stringify(u))
  }, ADMIN)
  await page.route(ME, r => json(r, 200, ADMIN))
  await page.route(CLIENTES, r => json(r, 200, { data: [] }))
  await page.route(TOKENS, r => r.request().method() === 'GET'
    ? json(r, 200, { items: conexoes })
    : json(r, 201, { token: 'emonitor_mcp_segredo123', item: { ...CONEXAO, id: '22222222-2222-2222-2222-222222222222', tipo: 'pessoal', nome: 'Claude Code', client_id: null } }))
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
})

test('revogar pede confirmação e chama DELETE', async ({ page }) => {
  await abrir(page, [CONEXAO])
  let apagou = false
  await page.route(TOKEN, r => { apagou = r.request().method() === 'DELETE'; return r.fulfill({ status: 204 }) })
  page.on('dialog', d => d.accept())
  await page.getByRole('region', { name: 'Integração com IA' }).getByRole('button', { name: 'Desconectar Claude' }).click()
  await expect.poll(() => apagou).toBe(true)
})
