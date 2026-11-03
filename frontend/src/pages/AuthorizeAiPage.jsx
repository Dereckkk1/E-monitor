import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { usePedidoAutorizacaoMcp, useConcederMcp } from '../api/hooks'
import { useAuth } from '../contexts/AuthContext'
import { emoldurada } from '../utils/emoldurada'
import './AuthorizeAiPage.css'

// Consentimento OAuth do MCP (spec 2026-10-27 §4.2). O /mcp/oauth/authorize
// da API valida o pedido e manda o navegador para cá com ?pedido=<assinado>;
// a decisão volta à API com o JWT do portal e a API devolve para onde ir.
// Um clique aqui concede acesso de administrador a um assistente: por isso o antiframe
// (`public/_headers` em /autorizar-ia* e `emoldurada()` abaixo, a segunda camada), igual a /pendentes.
function mensagemDe(err) {
  return err?.response?.data?.error ?? 'Não foi possível concluir. Volte ao assistente e conecte de novo.'
}

// O redirect vem da API (já validado lá contra o cadastro do cliente), mas a
// tela não navega para esquemas perigosos (javascript:, data:) se algo falhar.
function destinoSeguro(redirect) {
  try {
    const u = new URL(redirect)
    if (u.protocol === 'https:') return true
    return u.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(u.hostname)
  } catch {
    return false
  }
}

export default function AuthorizeAiPage() {
  const [params] = useSearchParams()
  const pedido = params.get('pedido') ?? ''
  const { user } = useAuth()
  const [moldura] = useState(emoldurada)
  // Emoldurada, nem o pedido é lido: a página não faz chamada nenhuma.
  const pedidoQ = usePedidoAutorizacaoMcp(pedido, { enabled: !moldura })
  const conceder = useConcederMcp()
  const [redirectInvalido, setRedirectInvalido] = useState(false)

  const decidir = (aprovar) =>
    conceder.mutate({ pedido, aprovar }, {
      onSuccess: (data) => {
        if (!destinoSeguro(data?.redirect)) { setRedirectInvalido(true); return }
        window.location.assign(data.redirect)
      },
    })

  if (moldura) {
    return (
      <div className="aia-page">
        <section className="aia-card">
          <h1 className="aia-titulo">Abra numa aba própria</h1>
          <p className="aia-texto">Esta página não abre dentro de outra página.</p>
        </section>
      </div>
    )
  }

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
        <h1 className="aia-titulo">Um assistente quer acessar o E-monitor</h1>
        <p className="aia-texto aia-destino">Ele se apresenta como <strong>{cliente}</strong> e vai voltar para <strong>{destino}</strong>.</p>
        <p className="aia-texto">
          Vai agir como <strong>{user?.name ?? 'você'}</strong>, com acesso de administrador: ler campanhas,
          veiculações, materiais e emissoras, gerar relatórios e, quando você confirmar, editar.
        </p>
        <p className="aia-texto aia-alerta">Só autorize se foi você que acabou de clicar em "Conectar" no assistente. Se alguém te mandou este link, recuse.</p>
        {conceder.isError && <p className="aia-msg aia-msg--erro" role="alert">{mensagemDe(conceder.error)}</p>}
        {redirectInvalido && <p className="aia-msg aia-msg--erro" role="alert">{mensagemDe(null)}</p>}
        <div className="aia-acoes">
          <button className="btn btn-secondary" disabled={conceder.isPending || conceder.isSuccess} onClick={() => decidir(false)}>Recusar</button>
          <button className="btn btn-primary" disabled={conceder.isPending || conceder.isSuccess} onClick={() => decidir(true)}>Autorizar</button>
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
