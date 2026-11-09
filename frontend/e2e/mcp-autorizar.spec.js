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
  await expect(page.getByRole('heading', { name: 'Um assistente quer acessar o E-monitor' })).toBeVisible()
  await expect(page.getByText('Claude', { exact: true })).toBeVisible()
  await expect(page.getByText('claude.ai', { exact: true })).toBeVisible()
  await expect(page.getByText(/Ele se apresenta como Claude e vai voltar para claude.ai./)).toBeVisible()
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

test('redirect com protocolo perigoso não navega e mostra erro', async ({ page }) => {
  await sessaoAdmin(page)
  await page.route(PEDIDO, r => json(r, 200, { cliente: 'Claude', destino: 'claude.ai', expira_em: '2030-01-01T00:00:00Z' }))
  await page.route(GRANT, r => json(r, 200, { redirect: 'javascript:window.__pwned=1' }))
  await page.goto('/autorizar-ia?pedido=P')
  await page.getByRole('button', { name: 'Autorizar' }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  expect(await page.evaluate(() => window.__pwned)).toBeUndefined()
  await expect(page).toHaveURL(/autorizar-ia/)
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

test('dentro de um iframe: pede para abrir numa aba própria e não chama a API nem mostra Autorizar/Recusar', async ({ page }) => {
  await sessaoAdmin(page)
  const chamadas = { pedido: 0, grant: 0 }
  await page.route(PEDIDO, r => { chamadas.pedido++; return json(r, 200, { cliente: 'Claude', destino: 'claude.ai', expira_em: '2030-01-01T00:00:00Z' }) })
  await page.route(GRANT, r => { chamadas.grant++; return json(r, 200, { redirect: 'https://claude.ai/cb?code=x' }) })
  await page.route('**/moldura-de-teste', r => r.fulfill({ status: 200, contentType: 'text/html', body: '<!doctype html><iframe src="/autorizar-ia?pedido=PEDIDO-ASSINADO" style="width:900px;height:600px"></iframe>' }))
  await page.goto('/moldura-de-teste')
  const quadro = page.frameLocator('iframe')
  await expect(quadro.getByText('Abra numa aba própria')).toBeVisible()
  // O resto do app (menu lateral) também está no quadro; o que não pode estar é a decisão.
  await expect(quadro.getByRole('button', { name: /Autorizar|Recusar/ })).toHaveCount(0)
  await expect(quadro.getByRole('heading', { name: 'Um assistente quer acessar o E-monitor' })).toHaveCount(0)
  expect(chamadas).toEqual({ pedido: 0, grant: 0 })
})
