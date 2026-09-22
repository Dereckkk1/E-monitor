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

/* ────────── o que a revisão da Task 8 achou (2026-09-22) ────────── */

/**
 * ⚠️ O DEFEITO QUE APAGA TRABALHO DIGITADO.
 *
 * `setField` monta `{...value, [k]: v}` com o `value` do render em que o blur
 * aconteceu, e `conferir` é async: tudo o que a pessoa digitar entre o blur e a
 * resposta é DESFEITO quando o write-back do código canônico roda com a cópia
 * velha. Só dispara quando o canônico difere do digitado — ou seja, quando a
 * pessoa DIGITOU em vez de colar, que é exatamente a entrada que a §3.1 existe
 * para aceitar.
 */
test('digitar durante a conferência não é desfeito pelo write-back', async ({ page }) => {
  await abrirWizard(page)
  await page.route(ROTA_CODIGO, async (route) => {
    await new Promise(r => setTimeout(r, 900))
    await route.fulfill({ status: 200, contentType: 'application/json',
      body: JSON.stringify(campanhaDoHub(CLIENTE.id)) })
  })

  await escolherCliente(page, 'Rôgga')
  await colarCodigo(page, 'eh7k4m2x')            // dispara a conferência lenta
  const nome = page.getByPlaceholder('ex: Verão 2026 — Rôgga')
  await nome.fill('Nome digitado DEPOIS do blur') // digita enquanto ela vai e volta
  await page.locator('input[type="date"]').first().fill('2026-12-01')

  await expect(page.getByRole('status')).toContainText('Verão 2026', { timeout: 15000 })

  await expect(nome, 'o nome digitado foi apagado pelo write-back').toHaveValue('Nome digitado DEPOIS do blur')
  await expect(page.locator('input[type="date"]').first()).toHaveValue('2026-12-01')
  await expect(campoDoCodigo(page)).toHaveValue('EH-7K4M2X')
})

// ⚠️ Apagar o campo durante a ida em voo é a operação de CONGELAR a coleta
// (§6.5). O write-back não pode ressuscitar o código.
test('apagar o código durante a conferência não é desfeito', async ({ page }) => {
  await abrirWizard(page)
  await page.route(ROTA_CODIGO, async (route) => {
    await new Promise(r => setTimeout(r, 900))
    await route.fulfill({ status: 200, contentType: 'application/json',
      body: JSON.stringify(campanhaDoHub(CLIENTE.id)) })
  })

  await escolherCliente(page, 'Rôgga')
  await colarCodigo(page, 'eh7k4m2x')
  await campoDoCodigo(page).fill('')     // apaga sem sair do campo
  await page.waitForTimeout(1400)

  await expect(campoDoCodigo(page), 'o código apagado voltou sozinho').toHaveValue('')
})

/**
 * ⚠️ 200 com corpo inutilizável vira VERDE VAZIO — uma caixa verde com o ✓ e
 * nada escrito. O backend traduz "200 que não dá para usar" em 502 → âmbar; a
 * tela tem de fazer o mesmo quando o corpo ruim vem da PRÓPRIA rota (proxy,
 * envelope novo, rota renomeada).
 */
test('200 com corpo inutilizável é âmbar, nunca verde vazio', async ({ page }) => {
  await abrirWizard(page)
  for (const corpo of ['{}', '{"data":{"nome":"Verão"}}', '[]']) {
    await page.unroute(ROTA_CODIGO).catch(() => {})
    await page.route(ROTA_CODIGO, route => route.fulfill({
      status: 200, contentType: 'application/json', body: corpo }))
    await escolherCliente(page, 'Rôgga')
    await colarCodigo(page, 'EH-7K4M2X')
    await expect(page.getByRole('status'), `corpo ${corpo}`).toContainText(/não deu para conferir/i)
  }
})

/**
 * ⚠️ A ponte tem de ser comparada como UUID, igual ao `clienteDoHubConfere` do
 * Go. A tela comparava string crua, então um UUID sem hífen, com `urn:uuid:` ou
 * com espaço — todos que o `uuid.Parse` aceita — pintavam VERMELHO dizendo
 * "de outro cliente (Rôgga)", nomeando o cliente que a pessoa escolheu e
 * oferecendo uma saída que não existe.
 */
test('a ponte é comparada como UUID, como no servidor', async ({ page }) => {
  const iguais = [
    CLIENTE.id.replace(/-/g, ''),
    `urn:uuid:${CLIENTE.id}`,
    `  ${CLIENTE.id}  `,
    CLIENTE.id.toUpperCase(),
    'colar aqui o id',   // lixo = ponte AUSENTE (§6.1), não divergência
    '',                  // sem ponte
  ]
  for (const ponte of iguais) {
    await abrirWizard(page)   // página nova por caso: o RSelect não sobrevive ao laço
    await page.route(ROTA_CODIGO, route => route.fulfill({
      status: 200, contentType: 'application/json',
      body: JSON.stringify({ ...campanhaDoHub(CLIENTE.id),
        cliente: { nome: 'Rôgga', idNaPlataforma: ponte } }) }))

    await escolherCliente(page, 'Rôgga')
    /* ⚠️ Provar que o cliente FICOU escolhido. Sem isto o teste passa pelo
       motivo errado: com `client_id` vazio a comparação curto-circuita e tudo
       vira verde — foi exatamente o que aconteceu na primeira versão daqui. */
    await expect(page.getByText('Cliente não selecionado')).toHaveCount(0)

    await colarCodigo(page, 'EH-7K4M2X')
    const rotulo = `ponte ${JSON.stringify(ponte)}`
    /* ⚠️ A asserção NÃO pode ser `toContainText('Verão 2026')`: a mensagem
       VERMELHA também nomeia a campanha, então ela passaria nos dois estados.
       E `not.toContainText(/outro cliente/i)` também não serve — o texto
       vermelho diz "outro CÓDIGO", não "outro cliente". As duas versões
       anteriores deste teste não conseguiam falhar. O que separa os dois
       estados é a frase de saída do vermelho. */
    await expect(page.getByRole('status'), rotulo).not.toContainText('Escolha esse cliente')
    await expect(page.getByRole('status'), rotulo).toContainText('01/12/2026')
  }
})

// O controle negativo: cliente REALMENTE diferente continua vermelho. Sem ele,
// bastaria a comparação sumir para o teste de cima ficar permanentemente verde.
test('ponte de outro cliente continua vermelha', async ({ page }) => {
  await abrirWizard(page)
  await page.route(ROTA_CODIGO, route => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify(campanhaDoHub(OUTRO.id, 'Colheita 2026')) }))
  await escolherCliente(page, 'Rôgga')
  await expect(page.getByText('Cliente não selecionado')).toHaveCount(0)
  await colarCodigo(page, 'EH-7K4M2X')
  await expect(page.getByRole('status')).toContainText('Escolha esse cliente')
  await expect(page.getByRole('status')).toContainText('Pilecco')
})

// ⚠️ O 422 da barreira do §4.4 é a mensagem que diz DE QUEM é o código. Trocá-la
// por "Tente novamente" manda a pessoa repetir uma ação que vai falhar sempre —
// e é o caminho NORMAL quando o hub está mudo na hora de digitar (âmbar, "pode
// seguir") e volta na hora de salvar.
test('a recusa do servidor chega inteira à pessoa', async ({ page }) => {
  await abrirWizard(page)
  await page.route(ROTA_CODIGO, route => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify(campanhaDoHub(CLIENTE.id)) }))
  await page.route('**/v1/internal/campaigns**', route => route.fulfill({
    status: 422, contentType: 'text/plain',
    body: 'esse código é da campanha "Colheita 2026", de outro cliente (Pilecco)' }))

  await escolherCliente(page, 'Rôgga')
  await page.getByPlaceholder('ex: Verão 2026 — Rôgga').fill('Vai levar 422')
  await page.locator('input[type="date"]').first().fill('2026-12-01')
  await page.locator('input[type="date"]').nth(1).fill('2027-02-28')
  await colarCodigo(page, 'EH-7K4M2X')
  await expect(page.getByRole('status')).toContainText('Verão 2026')
  await page.getByRole('button', { name: /avançar/i }).click()

  /* ⚠️ A aviso NAO e um dialogo nativo: o `ConfirmModal` desta casa
     SUBSTITUI o `window.alert` por um modal proprio. Por isso `page.on('dialog')`
     nunca dispara e um `window.alert` trocado por `addInitScript` e sobrescrito
     — as duas tecnicas dao falso negativo aqui, medido em 2026-09-22. Quem ve a
     mensagem e o DOM. */
  await expect(page.getByText(/de outro cliente/i)).toBeVisible({ timeout: 15000 })
  await expect(page.getByText('Colheita 2026')).toBeVisible()
  await expect(page.getByText(/Tente novamente/i)).toHaveCount(0)
})

/**
 * ⚠️ A LINHA QUE O COMMIT MAIS DEFENDE, E QUE NÃO TINHA PROVA NENHUMA.
 *
 * O backend lê `hub_code` ausente como "não mexe" e vazio como "apaga", e
 * apagar CONGELA a coleta da proposta no hub (§6.5). Se a tela mandasse o campo
 * sempre, qualquer falha em carregar o código para o rascunho apagaria o código
 * de uma campanha boa na primeira edição de nome — em silêncio, do outro lado.
 *
 * A mutação "mandar `hub_code` sempre" passava pelos 9 testes originais.
 */
test('editando só o nome, o PUT não fala do código', async ({ page }) => {
  const ID = 'aaaaaaaa-1111-2222-3333-444444444444'
  const corpos = []

  await page.addInitScript(() => {
    sessionStorage.setItem('rc_token', 'jwt.de.teste')
    sessionStorage.setItem('rc_user', JSON.stringify({
      id: '0', name: 'QA', email: 'qa@teste.local', role: 'admin', client_id: null }))
  })
  await page.route(ROTA_CLIENTES, r => r.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify({ data: [CLIENTE, OUTRO] }) }))
  await page.route(ROTA_MATERIAIS, r => r.fulfill({ status: 200, contentType: 'application/json', body: '{"data":[]}' }))
  await page.route(ROTA_CODIGO, r => r.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify(campanhaDoHub(CLIENTE.id)) }))
  await page.route(`**/v1/internal/campaigns/${ID}`, async (route) => {
    if (route.request().method() === 'PUT') {
      corpos.push(route.request().postDataJSON())
      return route.fulfill({ status: 200, contentType: 'application/json', body: '{"id":"' + ID + '"}' })
    }
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({
      id: ID, client_id: CLIENTE.id, name: 'Nome antigo',
      start_date: '2026-12-01T00:00:00Z', end_date: '2027-02-28T00:00:00Z',
      hub_code: 'EH-7K4M2X', status: 'programada', target_stations: [] }) })
  })

  await page.goto(`/campaigns/${ID}/edit`)
  await expect(campoDoCodigo(page)).toHaveValue('EH-7K4M2X')

  await page.getByPlaceholder('ex: Verão 2026 — Rôgga').fill('Nome NOVO')
  await page.getByRole('button', { name: /avançar/i }).click()

  await expect.poll(() => corpos.length, { timeout: 15000 }).toBeGreaterThan(0)
  expect(corpos[0], 'o PUT falou do código numa edição que não o tocou').not.toHaveProperty('hub_code')
  expect(corpos[0].name).toBe('Nome NOVO')
})

// O controle positivo: quando o código MUDA, ele tem de ir. Sem este, bastaria
// a tela parar de mandar `hub_code` sempre para o teste de cima ficar verde.
test('mudando o código, o PUT leva o código novo', async ({ page }) => {
  const ID = 'bbbbbbbb-1111-2222-3333-444444444444'
  const corpos = []

  await page.addInitScript(() => {
    sessionStorage.setItem('rc_token', 'jwt.de.teste')
    sessionStorage.setItem('rc_user', JSON.stringify({
      id: '0', name: 'QA', email: 'qa@teste.local', role: 'admin', client_id: null }))
  })
  await page.route(ROTA_CLIENTES, r => r.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify({ data: [CLIENTE, OUTRO] }) }))
  await page.route(ROTA_MATERIAIS, r => r.fulfill({ status: 200, contentType: 'application/json', body: '{"data":[]}' }))
  await page.route(ROTA_CODIGO, r => r.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify({ ...campanhaDoHub(CLIENTE.id), codigo: 'EH-BBBBBB' }) }))
  await page.route(`**/v1/internal/campaigns/${ID}`, async (route) => {
    if (route.request().method() === 'PUT') {
      corpos.push(route.request().postDataJSON())
      return route.fulfill({ status: 200, contentType: 'application/json', body: '{"id":"' + ID + '"}' })
    }
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({
      id: ID, client_id: CLIENTE.id, name: 'Nome antigo',
      start_date: '2026-12-01T00:00:00Z', end_date: '2027-02-28T00:00:00Z',
      hub_code: 'EH-7K4M2X', status: 'programada', target_stations: [] }) })
  })

  await page.goto(`/campaigns/${ID}/edit`)
  await expect(campoDoCodigo(page)).toHaveValue('EH-7K4M2X')
  await colarCodigo(page, 'EH-BBBBBB')
  await page.getByRole('button', { name: /avançar/i }).click()

  await expect.poll(() => corpos.length, { timeout: 15000 }).toBeGreaterThan(0)
  expect(corpos[0].hub_code).toBe('EH-BBBBBB')
})

// ⚠️ O POST tem de LEVAR o código. A mutação que o tirava do corpo passava pelos
// 9 testes originais: o código nunca chegava ao banco e a feature estava morta,
// com a suíte verde.
test('o POST da criação leva o código', async ({ page }) => {
  await abrirWizard(page)
  const corpos = []
  await page.route(ROTA_CODIGO, r => r.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify(campanhaDoHub(CLIENTE.id)) }))
  await page.route('**/v1/internal/campaigns', async (route) => {
    if (route.request().method() === 'POST') corpos.push(route.request().postDataJSON())
    return route.fulfill({ status: 201, contentType: 'application/json',
      body: '{"id":"cccccccc-1111-2222-3333-444444444444"}' })
  })

  await escolherCliente(page, 'Rôgga')
  await page.getByPlaceholder('ex: Verão 2026 — Rôgga').fill('Com código')
  await page.locator('input[type="date"]').first().fill('2026-12-01')
  await page.locator('input[type="date"]').nth(1).fill('2027-02-28')
  await colarCodigo(page, 'EH-7K4M2X')
  await expect(page.getByRole('status')).toContainText('Verão 2026')
  await page.getByRole('button', { name: /avançar/i }).click()

  await expect.poll(() => corpos.length, { timeout: 15000 }).toBeGreaterThan(0)
  expect(corpos[0].hub_code, 'o POST não levou o código — a feature estaria morta').toBe('EH-7K4M2X')
})

/**
 * ⚠️ A DECISÃO 6, do lado que importa: campanha ANTIGA, sem código, continua
 * editável. Exigir o campo também na edição faria "corrigir uma data numa
 * campanha de junho" virar "vá ao hub criar uma campanha primeiro".
 *
 * ⚠️ E é o único jeito de pegar a mutação: num teste de edição cuja campanha JÁ
 * tem código, tornar o campo obrigatório não trava nada, e a mutação sobrevive.
 */
test('campanha antiga SEM código continua editável (decisão 6)', async ({ page }) => {
  const ID = 'dddddddd-1111-2222-3333-444444444444'
  const corpos = []

  await page.addInitScript(() => {
    sessionStorage.setItem('rc_token', 'jwt.de.teste')
    sessionStorage.setItem('rc_user', JSON.stringify({
      id: '0', name: 'QA', email: 'qa@teste.local', role: 'admin', client_id: null }))
  })
  await page.route(ROTA_CLIENTES, r => r.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify({ data: [CLIENTE, OUTRO] }) }))
  await page.route(ROTA_MATERIAIS, r => r.fulfill({ status: 200, contentType: 'application/json', body: '{"data":[]}' }))
  await page.route(`**/v1/internal/campaigns/${ID}`, async (route) => {
    if (route.request().method() === 'PUT') {
      corpos.push(route.request().postDataJSON())
      return route.fulfill({ status: 200, contentType: 'application/json', body: '{"id":"' + ID + '"}' })
    }
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({
      id: ID, client_id: CLIENTE.id, name: 'Campanha de junho',
      start_date: '2026-06-01T00:00:00Z', end_date: '2026-06-30T00:00:00Z',
      hub_code: null, status: 'concluida', target_stations: [] }) })
  })

  await page.goto(`/campaigns/${ID}/edit`)
  await expect(campoDoCodigo(page)).toHaveValue('')

  await page.getByPlaceholder('ex: Verão 2026 — Rôgga').fill('Campanha de junho, renomeada')
  const avancar = page.getByRole('button', { name: /avançar/i })
  await expect(avancar, 'campanha antiga sem código ficou travada').toBeEnabled()
  await avancar.click()

  await expect.poll(() => corpos.length, { timeout: 15000 }).toBeGreaterThan(0)
  expect(corpos[0]).not.toHaveProperty('hub_code')
})
