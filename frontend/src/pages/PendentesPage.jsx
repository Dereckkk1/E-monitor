import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { usePendentesMcp, useAprovarPendenteMcp, useRecusarPendenteMcp } from '../api/hooks'
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
const MSG_SEM_REDE = 'Não foi possível falar com o servidor. Confira a conexão e tente de novo.'

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
  expirada: 'Expirou sem aprovação (24 h). Peça de novo à IA.',
}

// O que a recusa/erro da última ação significa para quem lê.
const ROTULO_FALHA = {
  preview_desatualizado: 'O pedido mudou: releia antes de aprovar',
}
const rotuloDaFalha = (f) => ROTULO_FALHA[f.codigo] ?? (f.status === 409 ? 'O estado do pedido mudou' : 'Não foi feito')

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
  x: <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" aria-hidden="true"><line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" /></svg>,
  vazio: <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M22 12h-6l-2 3h-4l-2-3H2" /><path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z" /></svg>,
}

function Esqueleto() {
  return (
    <div aria-busy="true">
      <span className="pen-sr" role="status">Carregando…</span>
      <ul className="pen-lista" aria-hidden="true">
        {[0, 1, 2].map(i => (
          <li key={i}>
            <div className="pen-card">
              <span className="pen-skel pen-skel--topo" />
              <span className="pen-skel pen-skel--titulo" />
              <span className="pen-skel pen-skel--linha" />
            </div>
          </li>
        ))}
      </ul>
    </div>
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
      {TEXTO_FECHADO[estado] && <p className="pen-fechado pen-fechado--ok">{TEXTO_FECHADO[estado]}</p>}
      {ressalvas.length > 0 && (
        <div className="pen-aviso pen-aviso--atencao">
          <p className="pen-rotulo">Ressalvas da execução</p>
          <ul className="pen-itens">{ressalvas.map((e, i) => <li key={i}>{e}</li>)}</ul>
        </div>
      )}
    </div>
  )
}

function Cartao({ p, estado, destaque, agindo, falha, onAgir }) {
  const idResumo = `pen-resumo-${p.id}`
  const pv = p.preview ?? {}
  const mudancas = pv.mudancas ?? []
  const efeitos = pv.efeitos ?? []
  const avisos = pv.ressalvas ?? []
  const meu = agindo?.id === p.id ? agindo.acao : null
  const classe = `pen-card pen-card--${estado}${destaque ? ' pen-card--destaque' : ''}`

  return (
    <article className={classe} aria-labelledby={idResumo} aria-current={destaque ? 'true' : undefined}>
      <div className="pen-topo">
        <code className="pen-tool">{p.tool}</code>
        <span className={`pen-estado pen-estado--${estado}`}>{ROTULO_ESTADO[estado] ?? estado}</span>
      </div>

      <h2 className="pen-resumo" id={idResumo}>{pv.resumo}</h2>

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
                <dt>{m.campo}</dt>
                <dd>
                  <span className="pen-sr">de</span>
                  <span className="pen-de">{valor(m.de)}</span>
                  <span className="pen-seta" aria-hidden="true">→</span>
                  <span className="pen-sr">para</span>
                  <span className="pen-para">{valor(m.para)}</span>
                </dd>
              </div>
            ))}
          </dl>
        </div>
      )}

      {efeitos.length > 0 && (
        <div className="pen-bloco">
          <p className="pen-rotulo">O que acontece</p>
          <ul className="pen-itens">{efeitos.map((e, i) => <li key={i}>{e}</li>)}</ul>
        </div>
      )}

      {avisos.length > 0 && (
        <div className="pen-aviso pen-aviso--atencao">
          <p className="pen-rotulo">Atenção</p>
          <ul className="pen-itens">{avisos.map((e, i) => <li key={i}>{e}</li>)}</ul>
        </div>
      )}

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
            <button className="btn btn-secondary" type="button" aria-describedby={idResumo}
              disabled={agindo !== null} onClick={() => onAgir(p, 'recusar')}>
              {meu === 'recusar' ? 'Recusando…' : 'Recusar'}
            </button>
            <button className="btn btn-primary" type="button" aria-describedby={idResumo}
              disabled={agindo !== null} onClick={() => onAgir(p, 'aprovar')}>
              {meu === 'aprovar' ? 'Aprovando…' : 'Aprovar e executar'}
            </button>
          </div>
        </>
      )}
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
  const [agindo, setAgindo] = useState(null) // { id, acao }
  const [falha, setFalha] = useState(null) // { id, mensagem }: a recusa/erro da última ação
  const [desfechos, setDesfechos] = useState([]) // pedidos que acabei de fechar, do mais novo ao mais antigo

  async function agir(p, acao) {
    setFalha(null)
    setAgindo({ id: p.id, acao })
    try {
      const data = await (acao === 'aprovar' ? aprovar : recusar).mutateAsync(p.id)
      // Fechado: sai da fila aberta, então o cartão fica aqui com o desfecho (e as ressalvas da execução).
      const ressalvas = (acao === 'aprovar' ? data?.ressalvas : null) ?? data?.pendente?.ressalvas ?? []
      setDesfechos(d => [{ ...p, ...data?.pendente, ressalvas }, ...d.filter(x => x.id !== p.id)])
      avisar('success', acao === 'aprovar' ? 'Ação executada' : 'Pedido recusado', ressalvas.length ? 'Com ressalvas: leia no cartão.' : null)
    } catch (err) {
      const status = err?.response?.status
      const corpo = err?.response?.data ?? {}
      const mensagem = corpo.mensagem ?? (err?.response ? MSG_PADRAO : MSG_SEM_REDE)
      if (status === 500 && corpo.error === 'interrompida') {
        // Virou `falhou`: final, e a ação pode ter acontecido. O texto do servidor diz o quê.
        setDesfechos(d => [{ ...p, estado: 'falhou', erro: mensagem, ressalvas: [] }, ...d.filter(x => x.id !== p.id)])
        avisar('error', 'Execução interrompida', 'Leia no cartão o que confirmar antes de pedir de novo.')
      } else {
        // 422: nada aconteceu e o pedido segue pendente (com o resumo novo, se era preview_desatualizado).
        // O texto do servidor fica no cartão.
        setFalha({ id: p.id, mensagem, codigo: corpo.error, status })
        // 409: o estado mudou por baixo (outra aba aprovou, o prazo venceu). O pedido já não está na fila
        // aberta: pedir o do link (?id=) traz o estado REAL dele, com as ressalvas, e o mantém na tela.
        if (status === 409) setParams({ id: p.id }, { replace: true })
      }
    } finally {
      setAgindo(null)
      // O botão clicado some da tela; sem isto o foco cai no body.
      area.current?.focus()
    }
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

  const linkNaoAchado = !!idUrl && !!dados && !dados.destaque
  const falhaSemCartao = falha && !vistos.has(falha.id)

  let corpo
  if (q.isLoading) {
    corpo = <Esqueleto />
  } else if (q.isError && !dados) {
    corpo = (
      <div className="pen-vazio" role="status">
        <span className="pen-vazio-icone pen-vazio-icone--erro">{Icone.alerta}</span>
        <h2 className="pen-vazio-titulo">Os pedidos não carregaram</h2>
        <p className="pen-vazio-texto">Tente de novo em instantes.</p>
        <button className="btn btn-primary" type="button" disabled={q.isFetching} onClick={() => q.refetch()}>Tentar de novo</button>
      </div>
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
        {falhaSemCartao && (
          <p className="pen-aviso pen-aviso--erro" role="alert">
            <span className="pen-rotulo">Não foi feito</span>
            <span>{falha.mensagem}</span>
          </p>
        )}
        {cartoes.length === 0 ? (
          <div className="pen-vazio" role="status">
            <span className="pen-vazio-icone">{Icone.vazio}</span>
            <h2 className="pen-vazio-titulo">Nenhum pedido esperando você</h2>
            <p className="pen-vazio-texto">
              Quando a IA pedir uma ação crítica pelo MCP, o pedido aparece aqui para você aprovar ou recusar.
            </p>
          </div>
        ) : (
          <ul className="pen-lista">
            {cartoes.map(p => (
              <li key={p.id}>
                <Cartao
                  p={p}
                  estado={estadoVisual(p, agora)}
                  destaque={p.id === idUrl || desfechos.some(d => d.id === p.id)}
                  agindo={agindo}
                  falha={falha?.id === p.id ? falha : null}
                  onAgir={agir}
                />
              </li>
            ))}
          </ul>
        )}
      </>
    )
  }

  return (
    <div className="pen-page">
      <header className="pen-header">
        <h1 className="pen-title">Pedidos da IA</h1>
        <p className="pen-subtitle">Ações que a IA pediu em seu nome e que só rodam quando você aprova.</p>
      </header>

      <div ref={area} tabIndex={-1} aria-label="Pedidos" className="pen-area">
        {corpo}
      </div>

      {toast && (
        <div className="pen-toast-region" aria-live="polite">
          <div className={`pen-toast pen-toast--${toast.kind}`} role="status">
            <span className="pen-toast-icone">{toast.kind === 'success' ? Icone.ok : Icone.alerta}</span>
            <div className="pen-toast-corpo">
              <p className="pen-toast-titulo">{toast.title}</p>
              {toast.msg && <p className="pen-toast-msg">{toast.msg}</p>}
            </div>
            <button className="pen-toast-fechar" type="button" onClick={fechar} aria-label="Fechar">{Icone.x}</button>
          </div>
        </div>
      )}
    </div>
  )
}
