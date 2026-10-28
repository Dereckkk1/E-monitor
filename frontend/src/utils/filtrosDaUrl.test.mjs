// Filtros que as tools do MCP põem na URL das telas (links.tela).
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { lerId, lerIds, lerPeriodo, lerOpcao, lerInteiro, temFiltro } from './filtrosDaUrl.js'

const A = '11111111-1111-4111-8111-111111111111'
const B = '22222222-2222-4222-8222-222222222222'
const sp = (q) => new URLSearchParams(q)

test('ids: só uuid válido passa; lista em csv', () => {
  assert.equal(lerId(sp(`client_id=${A}`), 'client_id'), A)
  assert.equal(lerId(sp('client_id=lixo'), 'client_id'), null)
  assert.equal(lerId(sp(''), 'client_id'), null)
  assert.equal(lerId(sp('client_id='), 'client_id'), null, 'vazio = ausente')
  assert.deepEqual(lerIds(sp(`campaigns=${A},lixo, ${B}`), 'campaigns'), [A, B])
  assert.deepEqual(lerIds(sp(`campaigns=${A},${A}`), 'campaigns'), [A], 'sem repetição')
  assert.deepEqual(lerIds(sp(''), 'campaigns'), [])
  assert.deepEqual(lerIds(sp('campaigns='), 'campaigns'), [])
})

test('período: dia válido e ordem; senão null', () => {
  assert.deepEqual(lerPeriodo(sp('from=2026-10-01&to=2026-10-31')), { from: '2026-10-01', to: '2026-10-31' })
  assert.deepEqual(lerPeriodo(sp('from=2026-10-05&to=2026-10-05')), { from: '2026-10-05', to: '2026-10-05' }, 'um dia só vale')
  assert.equal(lerPeriodo(sp('from=2026-10-31&to=2026-10-01')), null, 'to antes de from seria 400 na API')
  assert.equal(lerPeriodo(sp('from=2026-13-01&to=2026-10-31')), null)
  assert.equal(lerPeriodo(sp('from=2026-02-30&to=2026-03-01')), null, 'dia que não existe')
  assert.equal(lerPeriodo(sp('from=2026-10-01')), null, 'meio período não vale')
  assert.equal(lerPeriodo(sp('from=&to=')), null)
})

test('opção e inteiro só dentro do conjunto aceito pela tela', () => {
  assert.equal(lerOpcao(sp('status=ativa'), 'status', ['ativa', 'concluida']), 'ativa')
  assert.equal(lerOpcao(sp('status=xpto'), 'status', ['ativa']), null)
  assert.equal(lerOpcao(sp(''), 'status', ['ativa']), null)
  assert.equal(lerInteiro(sp('min_down=300'), 'min_down', [0, 60, 300, 1800]), 300)
  assert.equal(lerInteiro(sp('min_down=0'), 'min_down', [0, 60]), 0)
  assert.equal(lerInteiro(sp('min_down=120'), 'min_down', [0, 60]), null)
  assert.equal(lerInteiro(sp('min_down=60.0'), 'min_down', [0, 60]), null, 'só inteiro escrito como inteiro')
  assert.equal(lerInteiro(sp('min_down='), 'min_down', [0, 60]), null, 'vazio = ausente, não zero')
  assert.equal(lerInteiro(sp(''), 'min_down', [0, 60]), null)
})

test('temFiltro: chave presente, mesmo vazia (a URL tem de ser limpa)', () => {
  assert.equal(temFiltro(sp('from=2026-10-01'), ['client_id', 'from']), true)
  assert.equal(temFiltro(sp('client_id='), ['client_id', 'from']), true)
  assert.equal(temFiltro(sp('x=1'), ['client_id', 'from']), false)
})
