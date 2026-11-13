import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import {
  usePendentesMcp, useAprovarPendenteMcp, useRecusarPendenteMcp,
  aprovarPendenteMcp, recusarPendenteMcp, trocarPedidoNoCache,
} from '../api/hooks'
import FlowEmptyState from '../components/FlowEmptyState'
import { emoldurada } from '../utils/emoldurada'
import './PendentesPage.css'

// /pendentes — as escritas `critico` que um assistente de IA pediu pelo MCP e
// que só rodam quando uma PESSOA as aprova aqui. É esta tela que dá a garantia
// que o protocolo não dá: o modelo pede, um admin autenticado executa. Por isso
// o antiframe (`public/_headers` em /pendentes* e `emoldurada()` abaixo): um
// clique aqui grava dinheiro. O texto do preview vem do servidor e é só TEXTO
// (React escapa; nada de dangerouslySetInnerHTML): o que estiver escrito ali
// descreve a ação, não manda em nada.
//
// Estados: `pendente` tem botões; `aprovada` está executando; `falhou` é FINAL
// (a execução foi interrompida e pode ter acontecido); os demais só explicam.

const MSG_PADRAO = 'Não foi possível concluir. Tente de novo.'
// Sem resposta (a rede caiu, o Cloudflare deu 524 no meio) ou 5xx sem corpo de erro nosso: a aprovação
// pode ter chegado e executado. "Não foi feito" seria mentira; a tela diz que não sabe e relê o estado.
const MSG_INCERTO = 'Recarregue a página para ver o estado do pedido antes de tentar de novo.'
// 5xx em que o servidor DIZ que nada foi feito (a conferência caiu / aprovação indisponível).
const CODIGOS_SEM_EFEITO = new Set(['conferencia_falhou', 'indisponivel'])

const ROTULO_ESTADO = {
  pendente: 'Aguardando você',
  aprovada: 'Executando',
  executada: 'Executada',
  falhou: 'Interrompida',
  recusada: 'Recusada',
  expirada: 'Expirada',
}

const TEXTO_FECHADO = {
  executada: 'Já foi executada.',
  recusada: 'Você recusou este pedido.',
  expirada: 'Expirou sem aprovação. Peça de novo à IA.',
}

// O que a recusa/erro da última ação significa para quem lê.
const ROTULO_FALHA = {
  preview_desatualizado: 'O pedido mudou: releia antes de aprovar',
  pendente_mudou: 'O pedido mudou: releia antes de aprovar',
  incerto: 'Não sabemos se foi feito',
}
const rotuloDaFalha = (f) => ROTULO_FALHA[f.codigo] ?? (f.status === 409 ? 'O estado do pedido mudou' : 'Não foi feito')

/** O que um erro de aprovar/recusar significa. É a MESMA leitura para o botão do cartão e para o lote. */
function lerErro(err) {
  const status = err?.response?.status
  // Um 524 do Cloudflare (ou um 502) chega como HTML: o corpo não é objeto e não tem `error`.
  const corpo = err?.response?.data && typeof err.response.data === 'object' ? err.response.data : {}
  const semResposta = !err?.response
  const interrompida = status === 500 && corpo.error === 'interrompida'
  const incerto = !interrompida && (semResposta || (status >= 500 && !CODIGOS_SEM_EFEITO.has(corpo.error)))
  const mensagem = incerto ? MSG_INCERTO : (corpo.mensagem ?? MSG_PADRAO)
  return { status, corpo, interrompida, incerto, mensagem }
}

/** Agrupa os cartões pela campanha do pedido, na ordem em que aparecem. Sem campanha = "Outros pedidos". */
function agrupar(cartoes) {
  const grupos = []
  const porChave = new Map()
  for (const p of cartoes) {
    const chave = p.campanha?.id ?? '_sem'
    let g = porChave.get(chave)
    if (!g) {
      g = { chave, nome: p.campanha?.nome ?? null, itens: [] }
      porChave.set(chave, g)
      grupos.push(g)
    }
    g.itens.push(p)
  }
  // "Outros pedidos" vai por último: o que tem campanha é o caso do lote.
  return [...grupos.filter(g => g.chave !== '_sem'), ...grupos.filter(g => g.chave === '_sem')]
}

/** Caixa de marcar com o estado "alguns" (indeterminate), que só existe pela propriedade do DOM. */
function CaixaTodos({ marcados, total, disabled, onChange, rotulo }) {
  const ref = useRef(null)
  useLayoutEffect(() => {
    if (ref.current) ref.current.indeterminate = marcados > 0 && marcados < total
  }, [marcados, total])
  return (
    <input ref={ref} type="checkbox" className="pen-check-input" checked={total > 0 && marcados === total}
      disabled={disabled} onChange={onChange} aria-label={rotulo} />
  )
}

const quando = (iso) =>
  iso ? new Date(iso).toLocaleString('pt-BR', { timeZone: 'America/Sao_Paulo', dateStyle: 'short', timeStyle: 'short' }) : ''

/** O que a pessoa vê: um `pendente` com o prazo vencido no relógio dela já é `expirada`
 *  (o varredor só grava depois, e aprovar daria 409). */
function estadoVisual(p, agora) {
  return p.estado === 'pendente' && Date.parse(p.expira_em) <= agora ? 'expirada' : p.estado
}

/** Um valor de mudança "de → para", em texto: nulo é "—", booleano é sim/não. */
function valor(v) {
  if (v === null || v === undefined || v === '') return '—'
  if (typeof v === 'boolean') return v ? 'sim' : 'não'
  if (typeof v === 'number') return v.toLocaleString('pt-BR')
  if (typeof v === 'string') return v
  if (Array.isArray(v)) return v.length ? v.map(valor).join(', ') : '—'
  try { return JSON.stringify(v) } catch { return String(v) }
}

/** Quanto falta até o prazo, no relógio da pessoa: é o que decide a urgência do cartão. */
function falta(iso, agora) {
  const ms = Date.parse(iso) - agora
  if (!(ms > 0)) return null
  const min = Math.floor(ms / 60_000)
  if (min < 60) return { texto: `${Math.max(min, 1)} min`, urgente: true }
  const h = Math.floor(min / 60)
  if (h < 48) return { texto: `${h} h`, urgente: h < 2 }
  return { texto: `${Math.floor(h / 24)} dias`, urgente: false }
}

function useAgora(ms = 30_000) {
  const [agora, setAgora] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setAgora(Date.now()), ms)
    return () => clearInterval(t)
  }, [ms])
  return agora
}

function useToast() {
  const [toast, setToast] = useState(null)
  const timer = useRef(null)
  useEffect(() => () => clearTimeout(timer.current), [])
  const avisar = (kind, title, msg = null) => {
    clearTimeout(timer.current)
    setToast({ kind, title, msg })
    timer.current = setTimeout(() => setToast(null), 6000)
  }
  const fechar = () => { clearTimeout(timer.current); setToast(null) }
  return { toast, avisar, fechar }
}

const Icone = {
  ok: <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><polyline points="20 6 9 17 4 12" /></svg>,
  alerta: <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><line x1="12" y1="8" x2="12" y2="13" /><line x1="12" y1="17" x2="12.01" y2="17" /></svg>,
  relogio: <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="9" /><polyline points="12 7 12 12 15 14" /></svg>,
  x: <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" aria-hidden="true"><line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" /></svg>,
  vazio: <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M22 12h-6l-2 3h-4l-2-3H2" /><path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z" /></svg>,
}

/** A forma de um cartão, sem dados: o esqueleto do carregamento e a silhueta do estado vazio. */
function CartaoFantasma({ animado = true }) {
  const k = animado ? 'pen-skel' : 'pen-skel pen-skel--parado'
  return (
    <div className="pen-card pen-card--fantasma">
      <div className="pen-card-main">
        <span className={`${k} pen-skel--topo`} />
        <span className={`${k} pen-skel--titulo`} />
        <span className={`${k} pen-skel--linha`} />
        <span className={`${k} pen-skel--bloco`} />
      </div>
      <div className="pen-card-side">
        <span className={`${k} pen-skel--linha`} />
        <span className={`${k} pen-skel--linha-curta`} />
        <span className={`${k} pen-skel--botao`} />
      </div>
    </div>
  )
}

function Esqueleto() {
  return (
    <div aria-busy="true">
      <span className="pen-sr" role="status">Carregando…</span>
      <ul className="pen-lista" aria-hidden="true">
        {[0, 1].map(i => <li key={i}><CartaoFantasma /></li>)}
      </ul>
    </div>
  )
}

function Silhueta() {
  return (
    <ul className="pen-lista pen-lista--silhueta">
      {[0, 1, 2].map(i => <li key={i}><CartaoFantasma animado={false} /></li>)}
    </ul>
  )
}

/** O desfecho de um pedido que já não pede nada de você: vem logo abaixo do título,
 *  porque é a primeira coisa que a pessoa quer saber ("deu certo? o que eu confiro?"). */
function Desfecho({ p, estado, falha }) {
  const ressalvas = p.ressalvas ?? []
  return (
    <div className="pen-desfecho">
      {falha && (
        <p className="pen-aviso pen-aviso--erro" role="alert">
          <span className="pen-rotulo">{rotuloDaFalha(falha)}</span>
          <span>{falha.mensagem}</span>
        </p>
      )}
      {estado === 'falhou' && (
        <>
          <p className="pen-fechado pen-fechado--forte">A execução foi interrompida.</p>
          {p.erro
            ? <p className="pen-aviso pen-aviso--erro">{p.erro}</p>
            : <p className="pen-fechado">Confira se a ação aconteceu e, se não, peça de novo à IA.</p>}
        </>
      )}
      {estado === 'aprovada' && (
        <p className="pen-fechado">
          Executando… Se ficar assim por mais de 10 minutos, o pedido é marcado como interrompido e você
          confere se a ação aconteceu.
        </p>
      )}
      {/* Expirada de verdade (não só pelo relógio) pode trazer o motivo: a conexão de IA foi revogada. */}
      {estado === 'expirada' && p.estado === 'expirada' && p.erro
        ? <p className="pen-aviso pen-aviso--erro">{p.erro}</p>
        : TEXTO_FECHADO[estado] && <p className="pen-fechado pen-fechado--ok">{TEXTO_FECHADO[estado]}</p>}
      {ressalvas.length > 0 && (
        <div className="pen-aviso pen-aviso--atencao">
          <p className="pen-rotulo">Ressalvas da execução</p>
          <ul className="pen-itens">{ressalvas.map((e, i) => <li key={i} dir="auto">{e}</li>)}</ul>
        </div>
      )}
    </div>
  )
}

function Cartao({ p, estado, destaque, agindo, falha, onAgir, agora, selecao, Titulo = 'h2' }) {
  const idResumo = `pen-resumo-${p.id}`
  const pv = p.preview ?? {}
  const mudancas = pv.mudancas ?? []
  const efeitos = pv.efeitos ?? []
  const avisos = pv.ressalvas ?? []
  const meu = agindo?.id === p.id ? agindo.acao : null
  const prazo = estado === 'pendente' ? falta(p.expira_em, agora) : null
  const classe = `pen-card pen-card--${estado}${destaque ? ' pen-card--destaque' : ''}${selecao?.marcado ? ' pen-card--marcado' : ''}`

  return (
    <article className={classe} aria-labelledby={idResumo} aria-current={destaque ? 'true' : undefined}>
      <div className="pen-card-main">
        <div className="pen-topo">
          {selecao && (
            <input type="checkbox" className="pen-check-input" checked={selecao.marcado} disabled={selecao.disabled}
              onChange={selecao.alternar} aria-label={`Selecionar: ${pv.resumo ?? p.tool}`} />
          )}
          <span className={`pen-estado pen-estado--${estado}`}>{ROTULO_ESTADO[estado] ?? estado}</span>
          {prazo && (
            <span className={`pen-prazo${prazo.urgente ? ' pen-prazo--urgente' : ''}`}>
              {Icone.relogio}
              <span>faltam {prazo.texto}</span>
            </span>
          )}
        </div>

        <Titulo className="pen-resumo" id={idResumo} dir="auto">{pv.resumo}</Titulo>

        {estado !== 'pendente' && <Desfecho p={p} estado={estado} falha={falha} />}

        {p.motivo && (
          <p className="pen-motivo">
            <span className="pen-rotulo">Por que precisa da sua aprovação</span>
            {p.motivo}
          </p>
        )}

        {mudancas.length > 0 && (
          <div className="pen-bloco">
            <p className="pen-rotulo">O que muda</p>
            <dl className="pen-mudancas">
              {mudancas.map((m, i) => (
                <div className="pen-mudanca" key={`${m.campo}-${i}`}>
                  <dt dir="auto">{m.campo}</dt>
                  <dd>
                    <span className="pen-sr">de</span>
                    <span className="pen-de" dir="auto">{valor(m.de)}</span>
                    <span className="pen-seta" aria-hidden="true">→</span>
                    <span className="pen-sr">para</span>
                    <span className="pen-para" dir="auto">{valor(m.para)}</span>
                  </dd>
                </div>
              ))}
            </dl>
          </div>
        )}

        {efeitos.length > 0 && (
          <div className="pen-bloco">
            <p className="pen-rotulo">O que acontece</p>
            <ul className="pen-itens">{efeitos.map((e, i) => <li key={i} dir="auto">{e}</li>)}</ul>
          </div>
        )}

        {avisos.length > 0 && (
          <div className="pen-aviso pen-aviso--atencao">
            <p className="pen-rotulo">Atenção</p>
            <ul className="pen-itens">{avisos.map((e, i) => <li key={i} dir="auto">{e}</li>)}</ul>
          </div>
        )}
      </div>

      {/* O painel da decisão: de onde veio, até quando vale e os dois botões, sempre no mesmo lugar. */}
      <div className="pen-card-side">
        <div className="pen-ficha">
          <span className="pen-ficha-rotulo">Ferramenta</span>
          <code className="pen-tool">{p.tool}</code>
        </div>
        <p className="pen-meta">
          <span>Pedido em {quando(p.criada_em)}</span>
          {estado === 'pendente' && <span>Expira em {quando(p.expira_em)}</span>}
        </p>

        {estado === 'pendente' && (
          <>
            {falha ? (
              <p className="pen-aviso pen-aviso--erro" role="alert">
                <span className="pen-rotulo">{rotuloDaFalha(falha)}</span>
                <span>{falha.mensagem}</span>
              </p>
            ) : p.erro ? (
              <p className="pen-aviso pen-aviso--erro" role="status">
                <span className="pen-rotulo">A última tentativa não passou</span>
                <span>{p.erro}</span>
              </p>
            ) : null}
            <div className="pen-acoes">
              <button className="btn btn-primary" type="button" aria-describedby={idResumo}
                disabled={agindo !== null} onClick={() => onAgir(p, 'aprovar')}>
                {meu === 'aprovar' ? 'Aprovando…' : 'Aprovar e executar'}
              </button>
              <button className="btn btn-secondary" type="button" aria-describedby={idResumo}
                disabled={agindo !== null} onClick={() => onAgir(p, 'recusar')}>
                {meu === 'recusar' ? 'Recusando…' : 'Recusar'}
              </button>
            </div>
          </>
        )}
      </div>
    </article>
  )
}

export default function PendentesPage() {
  const [params, setParams] = useSearchParams()
  const idUrl = params.get('id')
  const [moldura] = useState(emoldurada)
  const q = usePendentesMcp(idUrl, { enabled: !moldura })
  const aprovar = useAprovarPendenteMcp()
  const recusar = useRecusarPendenteMcp()
  const agora = useAgora()
  const { toast, avisar, fechar } = useToast()
  const area = useRef(null)
  const qc = useQueryClient()
  const [agindo, setAgindo] = useState(null) // { id, acao }
  const [falhas, setFalhas] = useState({}) // id -> { mensagem, codigo, status }: a recusa/erro da última ação nele
  const [desfechos, setDesfechos] = useState([]) // pedidos que acabei de fechar, do mais novo ao mais antigo
  const [selecionados, setSelecionados] = useState(() => new Set())
  const [confirmar, setConfirmar] = useState(null) // { acao }: o lote esperando o "confirmar"
  const [progresso, setProgresso] = useState(null) // { acao, feitos, total }: o lote rodando

  const tirarFalha = (id) => setFalhas(f => { if (!(id in f)) return f; const n = { ...f }; delete n[id]; return n })
  const porFalha = (id, falha) => setFalhas(f => ({ ...f, [id]: falha }))

  // Fechado: sai da fila aberta, então o cartão fica aqui com o desfecho (e as ressalvas da execução).
  // A resposta não traz a campanha: o cartão mantém a sua, para não pular de grupo.
  function fecharCartao(p, acao, data) {
    const ressalvas = (acao === 'aprovar' ? data?.ressalvas : null) ?? data?.pendente?.ressalvas ?? []
    setDesfechos(d => [{ ...p, ...data?.pendente, campanha: p.campanha, ressalvas }, ...d.filter(x => x.id !== p.id)])
    return ressalvas
  }

  // Registra no cartão o que o erro significa e devolve o tipo: 'interrompida' | 'incerto' | 'recusa'.
  function registrarErro(p, err) {
    const e = lerErro(err)
    if (e.interrompida) {
      // Virou `falhou`: final, e a ação pode ter acontecido. O texto do servidor diz o quê.
      setDesfechos(d => [{ ...p, estado: 'falhou', erro: e.mensagem, ressalvas: [] }, ...d.filter(x => x.id !== p.id)])
      return 'interrompida'
    }
    if (e.incerto) {
      // Sem saber se rodou: o aviso fica no cartão e a fila é relida — se a ação rodou, o cartão vira o desfecho dela.
      porFalha(p.id, { mensagem: e.mensagem, codigo: 'incerto', status: e.status })
      return 'incerto'
    }
    // 422: nada aconteceu e o pedido segue pendente (com o resumo novo, se era preview_desatualizado).
    // 409: o estado mudou por baixo (outra aba aprovou, o prazo venceu, o resumo mudou).
    porFalha(p.id, { mensagem: e.mensagem, codigo: e.corpo.error, status: e.status })
    return 'recusa'
  }

  async function agir(p, acao) {
    tirarFalha(p.id)
    setAgindo({ id: p.id, acao })
    try {
      // Aprovar manda a versão (tentativa) do pedido que ESTE cartão mostra: se o pedido mudou desde
      // então (outra aba recebeu preview_desatualizado), o servidor recusa com 409 pendente_mudou.
      const data = await (acao === 'aprovar'
        ? aprovar.mutateAsync({ id: p.id, tentativa: p.tentativa })
        : recusar.mutateAsync(p.id))
      const ressalvas = fecharCartao(p, acao, data)
      avisar('success', acao === 'aprovar' ? 'Ação executada' : 'Pedido recusado', ressalvas.length ? 'Com ressalvas: leia no cartão.' : null)
    } catch (err) {
      const tipo = registrarErro(p, err)
      if (tipo === 'interrompida') {
        avisar('error', 'Execução interrompida', 'Leia no cartão o que confirmar antes de pedir de novo.')
      } else if (tipo === 'incerto') {
        avisar('error', 'Não sabemos se foi feito', 'Recarregue a página para ver o estado do pedido.')
      } else if (err?.response?.status === 409) {
        // Pedir o do link (?id=) traz o estado REAL dele, com as ressalvas, e o mantém na tela.
        setParams({ id: p.id }, { replace: true })
      }
    } finally {
      setAgindo(null)
      // O botão clicado some da tela; sem isto o foco cai no body.
      area.current?.focus()
    }
  }

  // Lote: a MESMA rota do botão do cartão, um pedido por vez, na ordem em que a IA pediu. Cada um tem
  // as conferências de sempre no servidor (a versão lida, o resumo recalculado). Não é tudo-ou-nada:
  // o que passou fica feito. Para no primeiro sem resposta (não sabemos se rodou: seguir disparando
  // seria às cegas) e quando a sessão cai (401/403).
  async function executarLote(acao, alvos) {
    setConfirmar(null)
    setProgresso({ acao, feitos: 0, total: alvos.length })
    let ok = 0
    let nao = 0
    let parou = false
    for (const p of alvos) {
      tirarFalha(p.id)
      try {
        const data = acao === 'aprovar' ? await aprovarPendenteMcp(p.id, p.tentativa) : await recusarPendenteMcp(p.id)
        fecharCartao(p, acao, data)
        ok++
      } catch (err) {
        trocarPedidoNoCache(qc, err?.response?.data?.pendente)
        const tipo = registrarErro(p, err)
        nao++
        const st = err?.response?.status
        if (tipo === 'incerto' || st === 401 || st === 403) { parou = true }
      }
      setProgresso(pr => pr && { ...pr, feitos: pr.feitos + 1 })
      if (parou) break
    }
    const naoTentados = alvos.length - ok - nao
    setProgresso(null)
    setSelecionados(new Set())
    qc.invalidateQueries({ queryKey: ['mcp', 'pendentes'] })
    const feitoTxt = acao === 'aprovar'
      ? (ok === 1 ? '1 pedido executado' : `${ok} pedidos executados`)
      : (ok === 1 ? '1 pedido recusado' : `${ok} pedidos recusados`)
    const partes = []
    if (nao > 0) partes.push(nao === 1 ? '1 não passou: leia no cartão.' : `${nao} não passaram: leia nos cartões.`)
    if (parou && naoTentados > 0) partes.push(`Parei sem saber se o último rodou: ${naoTentados} não foram tentados.`)
    avisar(nao > 0 ? 'error' : 'success', feitoTxt, partes.join(' ') || null)
    area.current?.focus()
  }

  if (moldura) {
    return (
      <div className="pen-page">
        <header className="pen-header">
          <h1 className="pen-title">Abra numa aba própria</h1>
          <p className="pen-subtitle">Esta página não abre dentro de outra página.</p>
        </header>
      </div>
    )
  }

  const dados = q.data
  // Ordem: o que acabei de decidir, o do link (?id=, mesmo já fechado) e a fila aberta. Um id aparece uma vez.
  const cartoes = []
  const vistos = new Set()
  const entrar = (p) => { if (p && !vistos.has(p.id)) { vistos.add(p.id); cartoes.push(p) } }
  desfechos.forEach(entrar)
  entrar(dados?.destaque)
  dados?.items?.forEach(entrar)

  const aguardando = cartoes.filter(p => estadoVisual(p, agora) === 'pendente').length
  const executando = cartoes.filter(p => estadoVisual(p, agora) === 'aprovada').length

  // Lote: só faz sentido com 2+ pedidos que ainda podem ser decididos.
  const selecionaveis = cartoes.filter(p => estadoVisual(p, agora) === 'pendente')
  const modoLote = selecionaveis.length >= 2
  // Na ordem em que a IA pediu (o mais antigo primeiro): é a ordem em que ela pensou as ações.
  const alvosLote = selecionaveis.filter(p => selecionados.has(p.id))
    .sort((a, b) => Date.parse(a.criada_em) - Date.parse(b.criada_em))
  const ocupado = agindo !== null || progresso !== null
  const alternar = (ids, marcar) => setSelecionados(sel => {
    const n = new Set(sel)
    ids.forEach(id => (marcar ? n.add(id) : n.delete(id)))
    return n
  })
  const grupos = agrupar(cartoes)

  // Com a tela anterior mantida enquanto a nova carrega (placeholder), `dados` ainda não é a do link.
  const linkNaoAchado = !!idUrl && !!dados && !q.isPlaceholderData && !dados.destaque
  const falhasSemCartao = Object.entries(falhas).filter(([id]) => !vistos.has(id))

  let corpo
  if (q.isLoading) {
    corpo = <Esqueleto />
  } else if (q.isError && !dados) {
    corpo = (
      <FlowEmptyState
        className="pen-vazio"
        tone="warn"
        icon={Icone.alerta}
        title="Os pedidos não carregaram"
        description="Tente de novo em instantes."
        actions={<button className="btn btn-primary" type="button" disabled={q.isFetching} onClick={() => q.refetch()}>Tentar de novo</button>}
        ghost={<Silhueta />}
      />
    )
  } else {
    corpo = (
      <>
        {q.isError && (
          <p className="pen-aviso pen-aviso--erro" role="status">
            <span>Não foi possível atualizar a lista. Os pedidos abaixo podem estar desatualizados.</span>
            <button className="btn btn-secondary btn-sm" type="button" disabled={q.isFetching} onClick={() => q.refetch()}>Tentar de novo</button>
          </p>
        )}
        {linkNaoAchado && (
          <p className="pen-aviso" role="status">
            <span>Não encontramos o pedido deste link. Ele pode ser de outra pessoa ou ter sido apagado.</span>
          </p>
        )}
        {falhasSemCartao.map(([id, f]) => (
          <p key={id} className="pen-aviso pen-aviso--erro" role="alert">
            <span className="pen-rotulo">{rotuloDaFalha(f)}</span>
            <span>{f.mensagem}</span>
          </p>
        ))}
        {cartoes.length === 0 ? (
          <FlowEmptyState
            className="pen-vazio"
            tone="mute"
            icon={Icone.vazio}
            title="Nenhum pedido esperando você"
            description="Quando a IA pedir uma ação crítica (cancelar campanha, mexer em preço, desativar usuário), o pedido chega aqui mostrando o que muda. Nada roda até você aprovar."
            actions={<Link className="btn btn-secondary" to="/account">Conectar uma IA</Link>}
            ghost={<Silhueta />}
          />
        ) : (
          <div className="pen-grupos">
            {grupos.map(g => {
              const abertos = g.itens.filter(p => estadoVisual(p, agora) === 'pendente')
              const marcados = abertos.filter(p => selecionados.has(p.id)).length
              const nome = g.nome ?? 'Outros pedidos'
              const comCabecalho = grupos.length > 1 || modoLote
              return (
                <section key={g.chave} className="pen-grupo" aria-label={g.nome ? `Campanha ${g.nome}` : nome}>
                  {comCabecalho && (
                    <div className="pen-grupo-head">
                      {modoLote && abertos.length > 0 && (
                        <CaixaTodos marcados={marcados} total={abertos.length} disabled={ocupado}
                          rotulo={`Selecionar todos os pedidos de ${nome}`}
                          onChange={() => alternar(abertos.map(p => p.id), marcados < abertos.length)} />
                      )}
                      <h2 className="pen-grupo-nome">
                        {g.nome && <span className="pen-grupo-tipo">Campanha</span>}
                        <span dir="auto">{nome}</span>
                      </h2>
                      <span className="pen-grupo-conta">
                        {g.itens.length === 1 ? '1 pedido' : `${g.itens.length} pedidos`}
                        {abertos.length > 0 && abertos.length < g.itens.length && ` · ${abertos.length} aguardando`}
                      </span>
                    </div>
                  )}
                  <ul className="pen-lista">
                    {g.itens.map(p => {
                      const estado = estadoVisual(p, agora)
                      return (
                        <li key={p.id}>
                          <Cartao
                            p={p}
                            estado={estado}
                            destaque={p.id === idUrl || desfechos.some(d => d.id === p.id)}
                            agindo={progresso ? { id: null, acao: 'lote' } : agindo}
                            falha={falhas[p.id] ?? null}
                            onAgir={agir}
                            agora={agora}
                            Titulo={comCabecalho ? 'h3' : 'h2'}
                            selecao={modoLote && estado === 'pendente' ? {
                              marcado: selecionados.has(p.id),
                              disabled: ocupado,
                              alternar: () => alternar([p.id], !selecionados.has(p.id)),
                            } : null}
                          />
                        </li>
                      )
                    })}
                  </ul>
                </section>
              )
            })}
          </div>
        )}
      </>
    )
  }

  return (
    <div className="pen-page">
      <header className="pen-header">
        <div className="pen-header-main">
          <h1 className="pen-title">Pedidos da IA</h1>
          <p className="pen-subtitle">Ações críticas que a IA pediu em seu nome pelo MCP. Nenhuma roda até você aprovar.</p>
        </div>
        <Link className="btn btn-secondary" to="/account">Conexões de IA</Link>
      </header>

      {(aguardando > 0 || executando > 0) && (
        <div className="pen-fila" role="group" aria-label="Situação da fila">
          {aguardando > 0 && (
            <span className="pen-fila-item pen-fila-item--pendente"><strong>{aguardando}</strong> aguardando você</span>
          )}
          {executando > 0 && (
            <span className="pen-fila-item pen-fila-item--aprovada"><strong>{executando}</strong> executando</span>
          )}
        </div>
      )}

      <div ref={area} tabIndex={-1} aria-label="Pedidos" className="pen-area">
        {corpo}
      </div>

      {modoLote && (alvosLote.length > 0 || progresso) && (
        <div className="pen-lote" role="region" aria-label="Decidir em lote">
          {progresso ? (
            <div className="pen-lote-barra" role="status">
              <span className="pen-lote-texto">
                {progresso.acao === 'aprovar' ? 'Executando' : 'Recusando'} {Math.min(progresso.feitos + 1, progresso.total)} de {progresso.total}…
              </span>
              <span className="pen-lote-trilho" aria-hidden="true">
                <span className="pen-lote-avanco" style={{ transform: `scaleX(${progresso.total ? progresso.feitos / progresso.total : 0})` }} />
              </span>
            </div>
          ) : confirmar ? (
            <div className="pen-lote-confirma">
              <p className="pen-lote-titulo">
                {confirmar.acao === 'aprovar'
                  ? `Aprovar e executar ${alvosLote.length} pedidos?`
                  : `Recusar ${alvosLote.length} pedidos?`}
              </p>
              <p className="pen-lote-apoio">
                {confirmar.acao === 'aprovar'
                  ? 'Rodam um por vez, na ordem em que a IA pediu, cada um conferido de novo. O que mudou desde que você leu fica pendente. Não é tudo-ou-nada: o que passar fica feito.'
                  : 'Nada é executado. A IA precisa pedir de novo se você mudar de ideia.'}
              </p>
              <ol className="pen-lote-lista">
                {alvosLote.map(p => <li key={p.id} dir="auto">{p.preview?.resumo ?? p.tool}</li>)}
              </ol>
              <div className="pen-lote-botoes">
                <button className="btn btn-secondary" type="button" onClick={() => setConfirmar(null)}>Voltar</button>
                <button className="btn btn-primary" type="button" onClick={() => executarLote(confirmar.acao, alvosLote)}>
                  {confirmar.acao === 'aprovar' ? `Confirmar e executar ${alvosLote.length}` : `Confirmar recusa de ${alvosLote.length}`}
                </button>
              </div>
            </div>
          ) : (
            <div className="pen-lote-barra">
              <span className="pen-lote-texto"><strong>{alvosLote.length}</strong> {alvosLote.length === 1 ? 'selecionado' : 'selecionados'}</span>
              <button className="pen-lote-limpar" type="button" disabled={ocupado} onClick={() => setSelecionados(new Set())}>Limpar</button>
              <span className="pen-lote-acoes">
                <button className="btn btn-secondary" type="button" disabled={ocupado} onClick={() => setConfirmar({ acao: 'recusar' })}>
                  Recusar selecionados
                </button>
                <button className="btn btn-primary" type="button" disabled={ocupado} onClick={() => setConfirmar({ acao: 'aprovar' })}>
                  Aprovar selecionados ({alvosLote.length})
                </button>
              </span>
            </div>
          )}
        </div>
      )}

      {/* A região viva fica SEMPRE montada e só o conteúdo muda: um leitor de tela só anuncia o que entra
          numa região que já existia. */}
      <div className="pen-toast-region" aria-live="polite" aria-atomic="true">
        {toast && (
          <div className={`pen-toast pen-toast--${toast.kind}`}>
            <span className="pen-toast-icone">{toast.kind === 'success' ? Icone.ok : Icone.alerta}</span>
            <div className="pen-toast-corpo">
              <p className="pen-toast-titulo">{toast.title}</p>
              {toast.msg && <p className="pen-toast-msg">{toast.msg}</p>}
            </div>
            <button className="pen-toast-fechar" type="button" onClick={fechar} aria-label="Fechar">{Icone.x}</button>
          </div>
        )}
      </div>
    </div>
  )
}
