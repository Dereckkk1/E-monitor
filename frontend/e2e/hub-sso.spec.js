import { test, expect } from '@playwright/test'

/**
 * `/sso` — a chegada pela Central de Clientes (RFC-001 §8.1).
 *
 * Existe por um motivo concreto: esta tela foi entregue com 15 testes do lado
 * Go verdes, contra um Postgres real, e **não funcionava em navegador nenhum**.
 * Ninguém a tinha aberto. O defeito vivia exatamente onde aqueles testes não
 * chegam — no browser, depois da resposta do backend:
 *
 *   1ª passada do efeito marca `jaTrocou` e dispara a troca; o React desmonta
 *   (StrictMode) e a limpeza faz `cancelado = true`; a 2ª passada sai na guarda
 *   `jaTrocou`, ANTES de criar um `cancelado` novo. Quando a troca resolve,
 *   `if (cancelado) return` mata a única passada que fez trabalho: nem `login`,
 *   nem `navigate`. O backend respondia 200, a sessão de 8h era emitida, e a
 *   pessoa ficava olhando o spinner para sempre.
 *
 * A API é falseada com `page.route` de propósito: o que está sendo provado é a
 * TELA. Amarrar isto a Postgres + NATS + MinIO + Go faria um teste que ninguém
 * roda. A costura com o Go tem os testes de `hubsso_test.go`, e a costura com o
 * hub tem o `e2e_manual_test.go`.
 */

const ROTA = '**/v1/internal/auth/sso'

const USUARIO = {
  id: '00000000-0000-0000-0000-000000000001',
  name: 'QA SSO',
  email: 'qa.sso@teste.local',
  role: 'admin',
  client_id: null,
}

test.describe('/sso — entrada pela Central de Clientes', () => {
  test('código válido: grava a sessão nativa e sai da /sso', async ({ page }) => {
    let trocas = 0
    await page.route(ROTA, async (route) => {
      trocas += 1
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ token: 'jwt.de.teste', user: USUARIO }),
      })
    })

    await page.goto('/sso?code=hs_codigo_de_teste')

    // O defeito: a tela ficava aqui para sempre, com o backend em 200.
    await page.waitForURL((url) => !url.pathname.startsWith('/sso'), { timeout: 30_000 })

    const sessao = await page.evaluate(() => sessionStorage.getItem('rc_token'))
    expect(sessao, 'a sessão nativa tem de ficar gravada depois da troca').toBe('jwt.de.teste')

    // A guarda `jaTrocou` continua de pé: o código vale UMA vez, e em
    // desenvolvimento o efeito roda duas.
    expect(trocas, 'o código só pode ser trocado uma vez').toBe(1)
  })

  test('sem código: recado com saída, nunca spinner eterno', async ({ page }) => {
    await page.goto('/sso')
    await expect(page.locator('.hubsso-title')).toHaveText('Não deu para entrar')
    await expect(page.locator('.hubsso-message').first()).toContainText('Central de Clientes')
    await expect(page.locator('.hubsso-exit')).toHaveAttribute('href', '/login')
    await expect(page.locator('.hubsso-spinner')).toHaveCount(0)
  })

  test('410: link expirado ou já usado, com saída para o login local', async ({ page }) => {
    await page.route(ROTA, (route) =>
      route.fulfill({ status: 410, contentType: 'text/plain', body: 'code_invalid_or_expired\n' })
    )
    await page.goto('/sso?code=hs_queimado')
    await expect(page.locator('.hubsso-message').first()).toContainText(/expirou ou já foi usado/i)
    await expect(page.locator('.hubsso-exit')).toHaveAttribute('href', '/login')
    await expect(page.locator('.hubsso-spinner')).toHaveCount(0)
  })

  /**
   * `client_not_provisioned` não é erro de borda: é o caminho normal de todo
   * cliente até a Fase 3 do RFC existir. Se esta tradução sumir, o time vê o
   * código cru em inglês numa tela de entrada.
   */
  test('client_not_provisioned vira português, não código cru', async ({ page }) => {
    await page.route(ROTA, (route) =>
      route.fulfill({ status: 403, contentType: 'text/plain', body: 'client_not_provisioned\n' })
    )
    await page.goto('/sso?code=hs_sem_cliente')
    await expect(page.locator('.hubsso-message').first()).toContainText(
      /empresa ainda não está liberada/i
    )
    await expect(page.locator('.hubsso-exit')).toHaveAttribute('href', '/login')
  })
})
