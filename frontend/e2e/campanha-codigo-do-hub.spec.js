import { test, expect } from '@playwright/test'

/**
 * O campo "Código do hub" no Step 1 do wizard de campanha (spec do hub
 * 2026-09-18, §4.2 e §4.3).
 *
 * ⚠️ Por que Playwright e não vitest: este `frontend/` NÃO tem runner de teste
 * unitário — não há vitest nem @testing-library instalados, e a regra 5 do
 * CLAUDE.md proíbe `npm install` nesta máquina (poda o lockfile e quebra o
 * build do Cloudflare Pages). O runner que existe é este, e ele entrou no
 * projeto exatamente por este motivo: a `/sso` foi entregue com 15 testes Go
 * verdes e não funcionava em navegador nenhum. Uma tela cuja lógica é "o que
 * fazer com CADA status da rede" é a que menos pode ser provada sem browser.
 *
 * A API é falseada com `page.route`: o que está sendo provado é a TELA. A
 * costura com o Go está em `hubcodes_test.go` e `campaigns_hubcode_test.go`.
 */

const ROTA_CLIENTES = '**/v1/internal/clients'
const ROTA_CODIGO = '**/v1/internal/hub-codes/*'
const ROTA_MATERIAIS = '**/v1/internal/clients/*/materials*'

const CLIENTE = { id: '11111111-1111-1111-1111-111111111111', name: 'Rôgga' }
const OUTRO = { id: '22222222-2222-2222-2222-222222222222', name: 'Pilecco' }

function campanhaDoHub(idNaPlataforma, nome = 'Verão 2026') {
  return {
    codigo: 'EH-7K4M2X',
    nome,
    inicio: '2026-12-01',
    fim: '2027-02-28',
    cliente: { nome: idNaPlataforma === CLIENTE.id ? 'Rôgga' : 'Pilecco', idNaPlataforma },
  }
}

/** Sessão de admin + os GETs que o Step 1 dispara ao abrir. */
async function abrirWizard(page) {
  await page.addInitScript(([cliente, outro]) => {
    sessionStorage.setItem('rc_token', 'jwt.de.teste')
    sessionStorage.setItem('rc_user', JSON.stringify({
      id: '00000000-0000-0000-0000-000000000009',
      name: 'QA Wizard', email: 'qa.wizard@teste.local', role: 'admin', client_id: null,
    }))
    void cliente; void outro
  }, [CLIENTE, OUTRO])

  await page.route(ROTA_CLIENTES, route => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({ data: [CLIENTE, OUTRO] }),
  }))
  await page.route(ROTA_MATERIAIS, route => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({ data: [] }),
  }))

  await page.goto('/campaigns/new')
  await expect(campoDoCodigo(page)).toBeVisible()
}

/**
 * O RSelect é um react-select SEM `classNamePrefix` — não existe `.rs__control`
 * para mirar. O que ele expõe de estável é o `role="combobox"` do input, e é
 * essa a única `combobox` do Step 1.
 *
 * `pressSequentially` e não `fill`: o react-select filtra no `onChange` de cada
 * tecla, e um `fill` que injeta o valor de uma vez não abre o menu.
 */
async function escolherCliente(page, nome) {
  const combo = page.getByRole('combobox').first()
  await combo.click()
  await combo.pressSequentially(nome)
  await page.keyboard.press('Enter')
}

/** O rótulo carrega o asterisco de obrigatório, então o nome acessível não é
 *  exatamente "Código do hub" — daí a regex. */
function campoDoCodigo(page) {
  return page.getByLabel(/código do hub/i)
}

async function colarCodigo(page, code) {
  const campo = campoDoCodigo(page)
  await campo.fill(code)
  await campo.blur()
}

test.describe('Step 1 — o código do hub', () => {
  test('código do cliente escolhido: verde, com o nome e o período da campanha', async ({ page }) => {
    await abrirWizard(page)
    await page.route(ROTA_CODIGO, route => route.fulfill({
      status: 200, contentType: 'application/json',
      body: JSON.stringify(campanhaDoHub(CLIENTE.id)),
    }))

    await escolherCliente(page, 'Rôgga')
    await colarCodigo(page, 'EH-7K4M2X')

    const status = page.getByRole('status')
    await expect(status).toContainText('Verão 2026')
    // O período é o que prova que a pessoa está amarrando na campanha CERTA —
    // dois códigos parecidos com nomes parecidos se distinguem pela data.
    await expect(status).toContainText('01/12/2026')
    await expect(status).toContainText('28/02/2027')
  })

  test('código de outro cliente: vermelho, e diz DE QUEM é, com a saída', async ({ page }) => {
    await abrirWizard(page)
    await page.route(ROTA_CODIGO, route => route.fulfill({
      status: 200, contentType: 'application/json',
      body: JSON.stringify(campanhaDoHub(OUTRO.id, 'Colheita 2026')),
    }))

    await escolherCliente(page, 'Rôgga')
    await colarCodigo(page, 'EH-7K4M2X')

    const status = page.getByRole('status')
    await expect(status).toContainText('Colheita 2026')
    await expect(status).toContainText('Pilecco')
    // A mensagem tem de dizer o que fazer a seguir, senão a pessoa trava.
    await expect(status).toContainText(/escolha esse cliente|cole outro código/i)
  })

  /**
   * ⚠️ O caso que só o navegador pega: o veredito depende do CLIENTE, e o
   * cliente pode ser trocado DEPOIS de conferir. Sem derivar o veredito a cada
   * render, a tela ficaria verde afirmando um cliente que não é mais o
   * escolhido — e a pessoa salvaria confiando no verde, para levar 422.
   */
  test('trocar o cliente depois de conferir muda o veredito, sem nova ida à rede', async ({ page }) => {
    await abrirWizard(page)
    let idas = 0
    await page.route(ROTA_CODIGO, route => {
      idas += 1
      return route.fulfill({
        status: 200, contentType: 'application/json',
        body: JSON.stringify(campanhaDoHub(CLIENTE.id)),
      })
    })

    await escolherCliente(page, 'Rôgga')
    await colarCodigo(page, 'EH-7K4M2X')
    await expect(page.getByRole('status')).toContainText('Verão 2026')

    await escolherCliente(page, 'Pilecco')

    await expect(page.getByRole('status')).toContainText(/de outro cliente|do cliente/i)
    expect(idas, 'trocar o cliente não pode gastar outra ida ao servidor').toBe(1)
  })

  /**
   * ⚠️ A RESPOSTA LENTA DO CÓDIGO ANTIGO NÃO PODE PINTAR A TELA.
   *
   * O campo confere ao sair do foco. Colar um código, perceber o erro, corrigir
   * e sair de novo deixa DUAS idas em voo — e a primeira pode voltar por
   * último. Sem o guarda de "só a resposta do pedido mais recente vale", a tela
   * acaba falando de um código que não está mais no campo: verde afirmando uma
   * campanha que a pessoa já trocou, e ela salva confiando nisso.
   *
   * A mutação provou que nenhum outro teste daqui pegava isto.
   */
  test('resposta lenta do código ANTIGO não sobrescreve a do novo', async ({ page }) => {
    await abrirWizard(page)
    await page.route(ROTA_CODIGO, async (route) => {
      const ehOAntigo = route.request().url().includes('EH-AAAAAA')
      if (ehOAntigo) await new Promise(r => setTimeout(r, 1200))
      await route.fulfill({
        status: 200, contentType: 'application/json',
        body: JSON.stringify({
          ...campanhaDoHub(CLIENTE.id, ehOAntigo ? 'Campanha ANTIGA' : 'Campanha NOVA'),
          codigo: ehOAntigo ? 'EH-AAAAAA' : 'EH-BBBBBB',
        }),
      })
    })

    await escolherCliente(page, 'Rôgga')
    await colarCodigo(page, 'EH-AAAAAA')   // dispara a lenta
    await colarCodigo(page, 'EH-BBBBBB')   // dispara a rápida

    await expect(page.getByRole('status')).toContainText('Campanha NOVA')
    // Tempo de sobra para a lenta voltar e tentar sobrescrever.
    await page.waitForTimeout(1600)
    await expect(page.getByRole('status')).toContainText('Campanha NOVA')
    await expect(page.getByRole('status')).not.toContainText('ANTIGA')
  })

  test('código inexistente (404): vermelho', async ({ page }) => {
    await abrirWizard(page)
    await page.route(ROTA_CODIGO, route =>
      route.fulfill({ status: 404, contentType: 'text/plain', body: 'código não encontrado\n' }))

    await escolherCliente(page, 'Rôgga')
    await colarCodigo(page, 'EH-ZZZZZZ')

    await expect(page.getByRole('status')).toContainText(/não encontrado/i)
  })

  test('hub fora do ar (503): âmbar, e NÃO impede de avançar', async ({ page }) => {
    await abrirWizard(page)
    await page.route(ROTA_CODIGO, route =>
      route.fulfill({ status: 503, contentType: 'text/plain', body: 'a Central não respondeu\n' }))

    await escolherCliente(page, 'Rôgga')
    await page.getByPlaceholder('ex: Verão 2026 — Rôgga').fill('Verão 2026 — Rôgga')
    await page.locator('input[type="date"]').first().fill('2026-12-01')
    await page.locator('input[type="date"]').nth(1).fill('2027-02-28')
    await colarCodigo(page, 'EH-7K4M2X')

    await expect(page.getByRole('status')).toContainText(/não deu para conferir/i)
    // ⚠️ A decisão 2 da spec em uma linha: queda do hub NÃO trava o cadastro.
    // Se este botão ficar cinza, a operação para de trabalhar quando o hub cai.
    await expect(page.getByRole('button', { name: /avançar/i })).toBeEnabled()
  })

  /**
   * ⚠️ O 502 é o "200 mentiroso": o hub respondeu 200 com um corpo que não dá
   * para usar (envelope novo, rota mudada, proxy respondendo por ele). É a
   * falha mais provável do dia a dia, e o backend a traduz para 502 — NÃO 503.
   * Se a tela tratasse só o 503 como âmbar, ela pintaria de vermelho um código
   * perfeito. Este teste é o que impede isso.
   */
  test('200 ilegível (502): âmbar, nunca vermelho', async ({ page }) => {
    await abrirWizard(page)
    await page.route(ROTA_CODIGO, route =>
      route.fulfill({ status: 502, contentType: 'text/plain', body: 'resposta ilegível\n' }))

    await escolherCliente(page, 'Rôgga')
    await colarCodigo(page, 'EH-7K4M2X')

    const status = page.getByRole('status')
    await expect(status).toContainText(/não deu para conferir/i)
    await expect(status).not.toContainText(/não encontrado/i)
  })

  test('o campo passa a mostrar a forma canônica que o servidor devolveu', async ({ page }) => {
    await abrirWizard(page)
    await page.route(ROTA_CODIGO, route => route.fulfill({
      status: 200, contentType: 'application/json',
      body: JSON.stringify(campanhaDoHub(CLIENTE.id)),
    }))

    await escolherCliente(page, 'Rôgga')
    await colarCodigo(page, 'eh7k4m2x')

    // Quem digita escreve minúsculo e sem hífen; a coluna guarda EH-7K4M2X.
    // Sem isto a tela mostra uma string e o banco guarda outra.
    await expect(campoDoCodigo(page)).toHaveValue('EH-7K4M2X')
  })

  test('sem código não dá para avançar na CRIAÇÃO (decisão 6)', async ({ page }) => {
    await abrirWizard(page)
    await escolherCliente(page, 'Rôgga')
    await page.getByPlaceholder('ex: Verão 2026 — Rôgga').fill('Verão 2026 — Rôgga')
    await page.locator('input[type="date"]').first().fill('2026-12-01')
    await page.locator('input[type="date"]').nth(1).fill('2027-02-28')

    const avancar = page.getByRole('button', { name: /avançar/i })
    await expect(avancar).toBeDisabled()

    await campoDoCodigo(page).fill('EH-7K4M2X')
    await expect(avancar).toBeEnabled()
  })
})
