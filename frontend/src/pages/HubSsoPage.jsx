import { useEffect, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import api from '../api/client'
import { useAuth } from '../contexts/AuthContext'
import './HubSsoPage.css'

/**
 * /sso — a chegada de quem clicou no card do E-monitor na Central de Clientes.
 *
 * A pessoa cai aqui com `?code=` na URL. O backend troca esse código com o hub
 * (RFC-001 §8.1) e devolve o MESMO envelope do /auth/login — token de 8h, user
 * com role e client_id. Daqui em diante nada muda: mesma sessão, mesmas telas.
 *
 * Tela de passagem, não de destino: quando dá certo, ninguém a lê. O que ela
 * precisa fazer bem é o caso ruim — dizer o que aconteceu e oferecer saída.
 */
export default function HubSsoPage() {
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const { login } = useAuth()
  const [erro, setErro] = useState(null)

  const code = searchParams.get('code')

  /**
   * O código vale UMA vez. Em desenvolvimento o React roda o efeito duas vezes
   * (StrictMode): sem esta guarda, a segunda troca recebe 410 e sobrescreve a
   * tela com "link expirado" num acesso perfeitamente válido.
   */
  const jaTrocou = useRef(false)

  useEffect(() => {
    // Sem código não há nada a fazer aqui — o recado é derivado no render, e
    // não setado de dentro do efeito: `setState` síncrono no corpo do efeito
    // provoca render em cascata (react-hooks/set-state-in-effect).
    if (!code) return
    if (jaTrocou.current) return
    jaTrocou.current = true

    let cancelado = false
    ;(async () => {
      try {
        const { data } = await api.post('/auth/sso', { code })
        if (cancelado) return
        login(data.token, data.user)
        // Mesmo destino do login local: a raiz resolve a home pelo role.
        navigate('/', { replace: true })
      } catch (err) {
        if (cancelado) return
        const status = err?.response?.status
        // `data` do backend Go vem como texto puro (http.Error), não JSON.
        const corpo = typeof err?.response?.data === 'string' ? err.response.data.trim() : ''
        setErro(mensagem(status, corpo))
      }
    })()

    return () => { cancelado = true }
  }, [code, navigate, login])

  // Derivado no render, não setado no efeito: sem o código na URL a página já
  // nasce sabendo o que dizer, e não há estado novo a criar para isso.
  const recado = erro ?? (code ? null : 'Abra o E-monitor pela Central de Clientes para entrar por aqui.')

  return (
    <div className="hubsso-shell">
      <div className="hubsso-card">
        {recado ? (
          <>
            <h1 className="hubsso-title">Não deu para entrar</h1>
            <p className="hubsso-message">{recado}</p>
            {/* Erro sem saída é beco sem saída: sem isto a pessoa fecha a aba e
                liga para alguém. O login local continua valendo (decisão D5). */}
            <a className="hubsso-exit" href="/login">Entrar pelo E-monitor</a>
          </>
        ) : (
          <>
            <span className="hubsso-spinner" aria-hidden="true" />
            <p className="hubsso-message" role="status">
              Entrando pela Central de Clientes…
            </p>
          </>
        )}
      </div>
    </div>
  )
}

/**
 * Traduz o erro do backend para uma frase que diz o que fazer.
 *
 * Os códigos vêm como texto puro porque é assim que o `http.Error` do Go
 * responde — comparar contra JSON aqui daria sempre a mensagem genérica.
 */
function mensagem(status, corpo) {
  if (corpo === 'client_not_provisioned') {
    return 'Sua empresa ainda não está liberada no E-monitor. Avise o nosso time — é um passo que depende de nós, não de você.'
  }
  if (corpo === 'account_disabled') {
    return 'Sua conta no E-monitor está desativada. Fale com o suporte.'
  }
  if (corpo === 'client_disabled') {
    return 'O acesso da sua empresa ao E-monitor está suspenso. Fale com o suporte.'
  }
  if (status === 410) {
    return 'Este link de acesso expirou ou já foi usado. Volte à Central de Clientes e clique de novo.'
  }
  if (corpo) return corpo.charAt(0).toUpperCase() + corpo.slice(1)
  return 'Não foi possível entrar agora. Tente novamente.'
}
