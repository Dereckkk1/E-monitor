import { useSearchParams } from 'react-router-dom'
import { usePedidoAutorizacaoMcp, useConcederMcp } from '../api/hooks'
import { useAuth } from '../contexts/AuthContext'
import './AuthorizeAiPage.css'

// Consentimento OAuth do MCP (spec 2026-10-27 §4.2). O /mcp/oauth/authorize
// da API valida o pedido e manda o navegador para cá com ?pedido=<assinado>;
// a decisão volta à API com o JWT do portal e a API devolve para onde ir.
function mensagemDe(err) {
  return err?.response?.data?.error ?? 'Não foi possível concluir. Volte ao assistente e conecte de novo.'
}

export default function AuthorizeAiPage() {
  const [params] = useSearchParams()
  const pedido = params.get('pedido') ?? ''
  const { user } = useAuth()
  const pedidoQ = usePedidoAutorizacaoMcp(pedido)
  const conceder = useConcederMcp()

  const decidir = (aprovar) =>
    conceder.mutate({ pedido, aprovar }, {
      onSuccess: (data) => window.location.assign(data.redirect),
    })

  let corpo
  if (!pedido) {
    corpo = <p className="aia-msg">Nenhum pedido de autorização nesta página. Ela é aberta pelo assistente de IA quando você conecta o E-monitor.</p>
  } else if (pedidoQ.isLoading) {
    corpo = <p className="aia-msg">Carregando o pedido…</p>
  } else if (pedidoQ.isError) {
    corpo = <p className="aia-msg aia-msg--erro" role="alert">{mensagemDe(pedidoQ.error)}</p>
  } else {
    const { cliente, destino } = pedidoQ.data
    corpo = (
      <>
        <h1 className="aia-titulo">{cliente} quer acessar o E-monitor</h1>
        <p className="aia-texto">
          Vai agir como <strong>{user?.name ?? 'você'}</strong>, com acesso de administrador: ler campanhas,
          veiculações, materiais e emissoras, gerar relatórios e, quando você confirmar, editar.
        </p>
        <p className="aia-texto aia-destino">Depois de decidir, você volta para <strong>{destino}</strong>.</p>
        <p className="aia-texto aia-alerta">Só autorize se foi você que acabou de clicar em "Conectar" no assistente. Se alguém te mandou este link, recuse.</p>
        {conceder.isError && <p className="aia-msg aia-msg--erro" role="alert">{mensagemDe(conceder.error)}</p>}
        <div className="aia-acoes">
          <button className="btn btn-secondary" disabled={conceder.isPending} onClick={() => decidir(false)}>Recusar</button>
          <button className="btn btn-primary" disabled={conceder.isPending} onClick={() => decidir(true)}>Autorizar</button>
        </div>
        <p className="aia-rodape">Você pode desconectar quando quiser em Minha conta → Integração com IA.</p>
      </>
    )
  }

  return (
    <div className="aia-page">
      <section className="aia-card" aria-live="polite">{corpo}</section>
    </div>
  )
}
