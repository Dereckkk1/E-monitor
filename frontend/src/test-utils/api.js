// Infra de teste compartilhada (NÃO é código de produção; fica fora da cobertura).
//
// mockApi troca o ADAPTER do axios usado pelo app (api/client.js): nada de rede,
// e os interceptors de produção (Authorization, tratamento de 401) continuam
// rodando de verdade. As rotas são relativas ao baseURL (/v1/internal), do jeito
// que o código chama: api.get('/campaigns') → chave 'GET /campaigns'.
//
//   const chamadas = mockApi({
//     'GET /campaigns': [{ id: 'c1', name: 'Campanha Teste' }],
//     'POST /campaigns': (config) => ({ id: 'novo', ...JSON.parse(config.data) }),
//     'DELETE /campaigns/c1': erroApi(409, { error: 'tem veiculações' }),
//   })
//   ... depois: expect(chamadas.map(c => c.chave)).toContain('POST /campaigns')
//
// Rota não mapeada → 404 (ou `padrao` se passado), pra teste não passar
// "sem querer" com uma resposta vazia que ninguém escolheu.
import { AxiosError } from 'axios'
import api from '../api/client'

const MARCA_ERRO = Symbol('erroApi')

/** Resposta de erro HTTP pra usar como valor de uma rota em mockApi. */
export function erroApi(status, data = {}) {
  return { [MARCA_ERRO]: true, status, data }
}

function responder(config, status, data) {
  const response = { data, status, statusText: String(status), headers: {}, config, request: {} }
  if (status >= 400) {
    throw new AxiosError(`Request failed with status code ${status}`,
      status >= 500 ? AxiosError.ERR_BAD_RESPONSE : AxiosError.ERR_BAD_REQUEST,
      config, {}, response)
  }
  return response
}

/**
 * @param {Record<string, any>} rotas  'MÉTODO /caminho' → dado | (config) => dado | erroApi(...)
 * @param {{ padrao?: any }} [opts]   resposta pra rota não mapeada (padrão: 404)
 * @returns {Array<{chave:string, metodo:string, url:string, params:any, data:any}>} chamadas feitas
 */
export function mockApi(rotas = {}, { padrao } = {}) {
  const chamadas = []
  api.defaults.adapter = async (config) => {
    const metodo = (config.method || 'get').toUpperCase()
    const url = (config.url || '').split('?')[0]
    const chave = `${metodo} ${url}`
    let corpo = config.data
    try { corpo = typeof config.data === 'string' ? JSON.parse(config.data) : config.data } catch { /* não-JSON */ }
    chamadas.push({ chave, metodo, url, params: config.params, data: corpo })

    let h = chave in rotas ? rotas[chave] : (url in rotas ? rotas[url] : undefined)
    if (h === undefined) {
      if (padrao === undefined) return responder(config, 404, { error: `mockApi: rota não mapeada ${chave}` })
      h = padrao
    }
    const valor = typeof h === 'function' ? await h(config) : h
    if (valor && valor[MARCA_ERRO]) return responder(config, valor.status, valor.data)
    return responder(config, 200, valor)
  }
  return chamadas
}
