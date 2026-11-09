import { useEffect, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { usePendentesMcp, useAprovarPendenteMcp, useRecusarPendenteMcp } from '../api/hooks'
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

function Cartao({ p, estado, destaque, agindo, falha, onAgir, agora }) {
  const idResumo = `pen-resumo-${p.id}`
  const pv = p.preview ?? {}
  const mudancas = pv.mudancas ?? []
  const efeitos = pv.efeitos ?? []
  const avisos = pv.ressalvas ?? []
  const meu = agindo?.id === p.id ? agindo.acao : null
  const prazo = estado === 'pendente' ? falta(p.expira_em, agora) : null
  const classe = `pen-card pen-card--${estado}${destaque ? ' pen-card--destaque' : ''}`

  return (
    <article className={classe} aria-labelledby={idResumo} aria-current={destaque ? 'true' : undefined}>
      <div className="pen-card-main">
        <div className="pen-topo">
          <span className={`pen-estado pen-estado--${estado}`}>{ROTULO_ESTADO[estado] ?? estado}</span>
          {prazo && (
            <span className={`pen-prazo${prazo.urgente ? ' pen-prazo--urgente' : ''}`}>
              {Icone.relogio}
              <span>faltam {prazo.texto}</span>
            </span>
          )}
        </div>

        <h2 className="pen-resumo" id={idResumo} dir="auto">{pv.resumo}</h2>

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
  const [agindo, setAgindo] = useState(null) // { id, acao }
  const [falha, setFalha] = useState(null) // { id, mensagem }: a recusa/erro da última ação
  const [desfechos, setDesfechos] = useState([]) // pedidos que acabei de fechar, do mais novo ao mais antigo

  async function agir(p, acao) {
    setFalha(null)
    setAgindo({ id: p.id, acao })
    try {
      // Aprovar manda a versão (tentativa) do pedido que ESTE cartão mostra: se o pedido mudou desde
      // então (outra aba recebeu preview_desatualizado), o servidor recusa com 409 pendente_mudou.
      const data = await (acao === 'aprovar'
        ? aprovar.mutateAsync({ id: p.id, tentativa: p.tentativa })
        : recusar.mutateAsync(p.id))
      // Fechado: sai da fila aberta, então o cartão fica aqui com o desfecho (e as ressalvas da execução).
      const ressalvas = (acao === 'aprovar' ? data?.ressalvas : null) ?? data?.pendente?.ressalvas ?? []
      setDesfechos(d => [{ ...p, ...data?.pendente, ressalvas }, ...d.filter(x => x.id !== p.id)])
      avisar('success', acao === 'aprovar' ? 'Ação executada' : 'Pedido recusado', ressalvas.length ? 'Com ressalvas: leia no cartão.' : null)
    } catch (err) {
      const status = err?.response?.status
      // Um 524 do Cloudflare (ou um 502) chega como HTML: o corpo não é objeto e não tem `error`.
      const corpo = err?.response?.data && typeof err.response.data === 'object' ? err.response.data : {}
      const semResposta = !err?.response
      const interrompida = status === 500 && corpo.error === 'interrompida'
      const incerto = !interrompida && (semResposta || (status >= 500 && !CODIGOS_SEM_EFEITO.has(corpo.error)))
      const mensagem = incerto ? MSG_INCERTO : (corpo.mensagem ?? (err?.response ? MSG_PADRAO : MSG_SEM_REDE))
      if (interrompida) {
        // Virou `falhou`: final, e a ação pode ter acontecido. O texto do servidor diz o quê.
        setDesfechos(d => [{ ...p, estado: 'falhou', erro: mensagem, ressalvas: [] }, ...d.filter(x => x.id !== p.id)])
        avisar('error', 'Execução interrompida', 'Leia no cartão o que confirmar antes de pedir de novo.')
      } else if (incerto) {
        // Sem saber se rodou: o aviso fica no cartão e a fila é relida (a mutação já invalida a lista
        // ao terminar, com erro ou não) — se a ação rodou, o cartão vira o desfecho dela.
        setFalha({ id: p.id, mensagem, codigo: 'incerto', status })
        avisar('error', 'Não sabemos se foi feito', 'Recarregue a página para ver o estado do pedido.')
      } else {
        // 422: nada aconteceu e o pedido segue pendente (com o resumo novo, se era preview_desatualizado).
        // O texto do servidor fica no cartão.
        setFalha({ id: p.id, mensagem, codigo: corpo.error, status })
        // 409: o estado mudou por baixo (outra aba aprovou, o prazo venceu, o resumo mudou). Pedir o do
        // link (?id=) traz o estado REAL dele, com as ressalvas, e o mantém na tela.
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

  const aguardando = cartoes.filter(p => estadoVisual(p, agora) === 'pendente').length
  const executando = cartoes.filter(p => estadoVisual(p, agora) === 'aprovada').length

  // Com a tela anterior mantida enquanto a nova carrega (placeholder), `dados` ainda não é a do link.
  const linkNaoAchado = !!idUrl && !!dados && !q.isPlaceholderData && !dados.destaque
  const falhaSemCartao = falha && !vistos.has(falha.id)

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
        {falhaSemCartao && (
          <p className="pen-aviso pen-aviso--erro" role="alert">
            <span className="pen-rotulo">{rotuloDaFalha(falha)}</span>
            <span>{falha.mensagem}</span>
          </p>
        )}
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
                  agora={agora}
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
