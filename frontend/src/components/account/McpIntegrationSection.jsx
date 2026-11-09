import { useState, useRef, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { useMcpConexoes, useCriarTokenMcp, useRevogarMcp, usePendentesMcp } from '../../api/hooks'
import { useConfirm } from '../ConfirmModal'

// "Integração com IA" — credenciais do MCP do E-monitor (spec 2026-10-27 §4.1).
// Só admin: o MCP não aceita outro papel.
const URL_MCP = `${import.meta.env.VITE_API_URL ?? window.location.origin}/mcp`

function data(iso) {
  return iso ? new Date(iso).toLocaleDateString('pt-BR') : '—'
}

function erroDe(err) {
  return err?.response?.data?.error ?? 'Não foi possível concluir.'
}

export default function McpIntegrationSection({ numeral }) {
  const conexoesQ = useMcpConexoes()
  const criar = useCriarTokenMcp()
  const revogar = useRevogarMcp()
  const pendentesQ = usePendentesMcp()
  const [agora] = useState(() => Date.now())
  const [nome, setNome] = useState('')
  const [validade, setValidade] = useState(90)
  const [gerado, setGerado] = useState(null)
  const [copia, setCopia] = useState('idle') // idle | ok | erro
  const timerCopia = useRef(null)
  const confirm = useConfirm()

  useEffect(() => () => clearTimeout(timerCopia.current), [])

  const copiar = async () => {
    clearTimeout(timerCopia.current)
    try {
      await navigator.clipboard.writeText(gerado)
      setCopia('ok')
      timerCopia.current = setTimeout(() => setCopia('idle'), 2000)
    } catch {
      setCopia('erro')
    }
  }

  const gerar = (e) => {
    e.preventDefault()
    criar.mutate({ nome, validade_dias: Number(validade) }, {
      onSuccess: (d) => { setGerado(d.token); setCopia('idle'); criar.reset(); setNome('') },
    })
  }

  const desconectar = async (c) => {
    // useConfirm devolve a Promise do modal do projeto: sem o await ela é sempre
    // truthy e o token seria revogado mesmo em "Cancelar".
    if (!(await confirm(`Desconectar "${c.nome}"? O assistente perde o acesso na hora.`))) return
    revogar.mutate(c.id)
  }

  // Só os que ainda dá para aprovar: um `pendente` com o prazo vencido no relógio daqui não conta.
  const aguardando = Array.isArray(pendentesQ.data?.items)
    ? pendentesQ.data.items.filter(p => p.estado === 'pendente' && Date.parse(p.expira_em) > agora).length
    : 0

  return (
    <section className="account-section" aria-labelledby="acc-sec-mcp">
      <header className="account-section-head">
        <h2 className="account-section-title" id="acc-sec-mcp">
          <span className="account-section-numeral" aria-hidden="true">§ {numeral}</span>
          <span className="account-section-title-text">Integração com IA</span>
        </h2>
      </header>

      <p className="account-mcp-texto">
        Conecte o E-monitor ao Claude (claude.ai e Claude Code). Endereço do servidor MCP:
      </p>
      <code className="account-mcp-url">{URL_MCP}</code>
      <p className="account-mcp-texto">
        No claude.ai, adicione um conector personalizado com esse endereço e autorize. No Claude Code e em
        clientes que aceitam cabeçalho, gere um token abaixo. O Gemini Enterprise é cadastrado pelo
        administrador do sistema.
      </p>

      <p className="account-mcp-texto">
        <Link className="account-mcp-pendentes" to="/pendentes">
          Pedidos da IA
          {aguardando > 0 && <span className="account-mcp-contagem" aria-label={`${aguardando} aguardando`}>{aguardando}</span>}
        </Link>
        {' '}— as ações críticas que a IA pediu e esperam a sua aprovação.
      </p>

      <form className="account-mcp-form" onSubmit={gerar}>
        <label className="field">
          <span>Nome do token</span>
          <input value={nome} onChange={e => setNome(e.target.value)} placeholder="Claude Code no notebook" maxLength={80} required />
        </label>
        <label className="field">
          <span>Validade (dias)</span>
          <input type="number" min={1} max={365} value={validade} required onChange={e => setValidade(e.target.value)} />
        </label>
        <button className="btn btn-primary" type="submit" disabled={criar.isPending}>Gerar token</button>
      </form>
      {criar.isError && <p className="account-mcp-erro" role="alert">{erroDe(criar.error)}</p>}
      {gerado && (
        <div className="account-mcp-gerado" role="status">
          <code>{gerado}</code>
          <p>Copie agora: este token não será mostrado de novo. Use como <code>Authorization: Bearer …</code>.</p>
          <div className="account-mcp-gerado-acoes">
            <button className="btn btn-secondary btn-sm" type="button" onClick={copiar}>{copia === 'ok' ? 'Copiado' : 'Copiar'}</button>
            <button className="btn btn-secondary btn-sm" type="button" onClick={() => setGerado(null)}>Já copiei</button>
          </div>
          {copia === 'erro' && <p className="account-mcp-erro" role="alert">Não deu para copiar — selecione e copie à mão</p>}
        </div>
      )}

      {conexoesQ.isLoading && <p className="account-mcp-meta">Carregando…</p>}
      {conexoesQ.isError && <p className="account-mcp-erro" role="alert">{erroDe(conexoesQ.error)}</p>}
      <ul className="account-mcp-lista">
        {(conexoesQ.data ?? []).map(c => (
          <li key={c.id} className="account-mcp-item">
            <div>
              <strong>{c.nome}</strong>
              <span className="account-mcp-meta">
                {c.tipo === 'pessoal' ? 'Token pessoal' : 'Conector OAuth'} · criado {data(c.criado_em)} · último uso {data(c.ultimo_uso)} · expira {data(c.expira_em)}
              </span>
            </div>
            <button className="btn btn-secondary btn-sm" type="button" onClick={() => desconectar(c)} disabled={revogar.isPending && revogar.variables === c.id} aria-label={`Desconectar ${c.nome}`}>
              Desconectar
            </button>
          </li>
        ))}
        {conexoesQ.data?.length === 0 && <li className="account-mcp-meta">Nenhuma conexão ativa.</li>}
      </ul>
      {revogar.isError && <p className="account-mcp-erro" role="alert">{erroDe(revogar.error)}</p>}
    </section>
  )
}
