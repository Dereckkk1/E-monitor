import { test, expect } from '@playwright/test'

/**
 * /autorizar-ia — consentimento OAuth do MCP (spec 2026-10-27 §4.2).
 * A API é falseada com page.route: o que se prova é a TELA. O fluxo Go está
 * em workers/internal/mcp/oauth/*_test.go.
 */

const PEDIDO = '**/v1/internal/mcp/oauth/pedido*'
const GRANT = '**/v1/internal/mcp/oauth/grant'

function json(route, status, body) {
  return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function sessaoAdmin(page) {
  await page.addInitScript(() => {
    sessionStorage.setItem('rc_token', 'jwt.de.teste')
    sessionStorage.setItem('rc_user', JSON.stringify({
      id: '00000000-0000-0000-0000-000000000009', name: 'QA Admin',
      email: 'qa@teste.local', role: 'admin', client_id: null,
    }))
  })
}

test('autorizar manda o navegador de volta ao assistente com o código', async ({ page }) => {
  await sessaoAdmin(page)
  await page.route(PEDIDO, r => json(r, 200, { cliente: 'Claude', destino: 'claude.ai', expira_em: '2030-01-01T00:00:00Z' }))
  let corpo
  await page.route(GRANT, r => { corpo = r.request().postDataJSON(); return json(r, 200, { redirect: 'https://claude.ai/api/mcp/auth_callback?code=abc&state=s' }) })
  await page.route('https://claude.ai/**', r => r.fulfill({ status: 200, contentType: 'text/html', body: 'ok' }))

  await page.goto('/autorizar-ia?pedido=PEDIDO-ASSINADO')
  await expect(page.getByRole('heading', { name: /Claude quer acessar o E-monitor/ })).toBeVisible()
  await expect(page.getByText('claude.ai')).toBeVisible()
  await expect(page.getByText('Só autorize se foi você que acabou de clicar em "Conectar" no assistente. Se alguém te mandou este link, recuse.')).toBeVisible()
  await page.getByRole('button', { name: 'Autorizar' }).click()

  await expect(page).toHaveURL(/claude\.ai\/api\/mcp\/auth_callback\?code=abc/)
  expect(corpo).toEqual({ pedido: 'PEDIDO-ASSINADO', aprovar: true })
})

test('recusar devolve aprovar=false', async ({ page }) => {
  await sessaoAdmin(page)
  await page.route(PEDIDO, r => json(r, 200, { cliente: 'Claude', destino: 'claude.ai', expira_em: '2030-01-01T00:00:00Z' }))
  let corpo
  await page.route(GRANT, r => { corpo = r.request().postDataJSON(); return json(r, 200, { redirect: 'https://claude.ai/cb?error=access_denied' }) })
  await page.route('https://claude.ai/**', r => r.fulfill({ status: 200, contentType: 'text/html', body: 'ok' }))

  await page.goto('/autorizar-ia?pedido=P')
  await page.getByRole('button', { name: 'Recusar' }).click()
  await expect(page).toHaveURL(/error=access_denied/)
  expect(corpo.aprovar).toBe(false)
})

test('pedido vencido mostra o motivo e nenhum botão de autorizar', async ({ page }) => {
  await sessaoAdmin(page)
  await page.route(PEDIDO, r => json(r, 400, { error: 'Pedido de autorização inválido ou vencido (vale 10 minutos). Volte ao assistente e conecte de novo.' }))
  await page.goto('/autorizar-ia?pedido=VELHO')
  await expect(page.getByText(/inválido ou vencido/)).toBeVisible()
  await expect(page.getByRole('button', { name: 'Autorizar' })).toHaveCount(0)
})

test('sem pedido na URL', async ({ page }) => {
  await sessaoAdmin(page)
  await page.goto('/autorizar-ia')
  await expect(page.getByText(/Nenhum pedido de autorização/)).toBeVisible()
})
