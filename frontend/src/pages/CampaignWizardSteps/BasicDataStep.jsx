import { useEffect, useMemo, useRef, useState } from 'react'
import RSelect from '../../components/RSelect'
import api from '../../api/client'

function fmtDate(iso) {
  if (!iso) return '—'
  const [y, m, d] = iso.slice(0, 10).split('-')
  return `${d}/${m}/${y}`
}

/*
A ponte de identidade, do jeito que o servidor a lê.

⚠️ Espelha o `clienteDoHubConfere` do Go (`handlers/campaigns.go`) e o
`mesmaPonte` do hub. Os três precisam concordar: o `emonitorClientId` do hub é
texto livre digitado à mão, e o `uuid.Parse` do Go aceita as formas sem hífen,
entre chaves e com `urn:uuid:`. Uma tela mais ESTREITA que o servidor recusa o
que ele aceita — e a mensagem que ela mostra nomeia o próprio cliente escolhido.

⚠️ FILTRA antes de minusculizar, pela mesma razão medida no código da campanha:
`toLowerCase()` do JS aplica o case mapping completo do Unicode e o
`strings.ToLower` do Go só o simples. Reduzindo a hexadecimal primeiro sobra só
ASCII, onde as duas linguagens concordam por construção.

Devolve `null` para o que NÃO é UUID — ausência de ponte, não divergência.
*/
function soHex(v) {
  const semUrn = (v ?? '').trim().replace(/^urn:uuid:/i, '')
  return semUrn.replace(/[^0-9a-fA-F]/g, '').toLowerCase()
}

function ponteComoUUID(v) {
  const hex = soHex(v)
  return hex.length === 32 ? hex : null
}

function diffInDays(a, b) {
  if (!a || !b) return null
  const da = new Date(a + 'T00:00:00')
  const db = new Date(b + 'T00:00:00')
  const ms = db.getTime() - da.getTime()
  if (Number.isNaN(ms) || ms < 0) return null
  return Math.round(ms / 86400000) + 1
}

/**
 * Step 1 of the wizard: campaign basic data.
 *
 * Split layout: form on the left, live preview card on the right so the user
 * sees the campaign shape forming as they type. On narrow screens the preview
 * collapses below the form.
 *
 * Props:
 *  - value: { name, client_id, start_date, end_date, hub_code }
 *  - onChange: (newValue) => void
 *  - clients: Array<{id, name}>
 *  - isEditMode: bool — disables client field after creation
 */
export default function BasicDataStep({ value, onChange, clients, isEditMode }) {
  /* ⚠️ O valor CORRENTE, não o do render que fechou sobre ele.
     `onChange({...value})` com o `value` capturado parece inofensivo até alguém
     chamar `setField` de dentro de uma função ASSÍNCRONA: a conferência do
     código demora o tempo da rede, e tudo o que a pessoa digitar nesse intervalo
     é DESFEITO quando a resposta chega e espalha a cópia velha.

     Medido em 2026-09-22, com resposta em 1,2s: o nome voltava ao valor
     anterior, as datas voltavam a vazias, e em modo edição o wizard AVANÇAVA
     sem mandar PUT nenhum — a pessoa saía convencida de que tinha salvado. */
  const valorAtual = useRef(value)
  // Escrito no EFEITO, não no render: o compilador do React proíbe tocar ref
  // durante o render, e para o que isto serve — ser lido por um callback
  // assíncrono, muito depois do commit — dá no mesmo.
  useEffect(() => { valorAtual.current = value }, [value])

  function setField(k, v) {
    onChange({ ...valorAtual.current, [k]: v })
  }

  const selectedClient = useMemo(
    () => clients.find(c => c.id === value.client_id) ?? null,
    [clients, value.client_id]
  )

  const days = diffInDays(value.start_date, value.end_date)

  /* ─────────────── A conferência do código do hub (§4.3 da spec) ───────────────

     ⚠️ Isto é CONVENIÊNCIA, não barreira. A barreira de verdade é o 422 do
     servidor no POST e no PUT (§4.4): quem chamar a API direto pula esta tela
     inteira. O que esta conferência compra é a pessoa ver EM QUE está
     amarrando a campanha antes de salvar, em vez de descobrir no erro. */
  const [conferencia, setConferencia] = useState({ estado: 'vazio' })
  /* Cada ida ao servidor leva um número, e só a resposta do pedido MAIS RECENTE
     pinta a tela. Sem isto, colar um código, corrigir e sair do campo de novo
     deixa a resposta do primeiro — que pode chegar depois — sobrescrever a do
     segundo, e a tela passa a falar de um código que não está mais no campo. */
  const pedidoAtual = useRef(0)
  const codigoDigitado = (value.hub_code ?? '').trim()

  async function conferir(bruto) {
    const code = (bruto ?? '').trim()
    if (!code) {
      pedidoAtual.current += 1
      setConferencia({ estado: 'vazio' })
      return
    }
    // ⚠️ Não reconfere o que já está conferido: o `onBlur` dispara toda vez que
    // o campo perde o foco, mesmo sem edição — medido, 4 idas à rede para o
    // mesmo código só entrando e saindo. Cada uma divide com o job de 15 em 15
    // minutos o balde por IP do hub.
    if (code === conferencia.codigo && conferencia.estado === 'achou') return

    const meuPedido = (pedidoAtual.current += 1)
    setConferencia({ estado: 'conferindo', codigo: code })
    try {
      /* `timeout` explícito: o axios desta casa não tem nenhum (padrão 0 =
         nunca). O teto de 2s de que a feature fala é do SERVIDOR; sem este, uma
         conexão pendurada deixa "Conferindo com a Central…" para sempre, sem
         saída a não ser reeditar o campo. */
      const { data } = await api.get(`/hub-codes/${encodeURIComponent(code)}`, { timeout: 8000 })
      if (meuPedido !== pedidoAtual.current) return

      /* ⚠️ 200 com corpo inutilizável NÃO é sucesso. Sem esta guarda, um
         `{}` — proxy no meio, envelope novo, rota renomeada — vira uma caixa
         VERDE com o ✓ e nada escrito dentro. O backend traduz esse mesmo caso
         em 502 → âmbar quando ele vem do hub; quando vem da nossa própria rota,
         quem tem de traduzir é a tela. */
      if (!data || typeof data !== 'object' || !data.nome) {
        setConferencia({ estado: 'naoConferido', codigo: code })
        return
      }

      /* A forma CANÔNICA volta do servidor e substitui o que foi digitado: quem
         digita escreve `eh7k4m2x` e a coluna guarda `EH-7K4M2X` (§10). Sem
         isto a tela mostra uma string e o banco guarda outra.

         ⚠️ Mas só se o campo AINDA falar deste código. Entre o blur e a resposta
         a pessoa pode ter apagado o campo (que é a operação de congelar a
         coleta, §6.5) ou digitado outro — repor aqui desfaria o gesto dela em
         silêncio. O `pedidoAtual` não cobre isso: ele só anda no `onBlur`. */
      const canonico = data.codigo || code
      if ((valorAtual.current.hub_code ?? '').trim() !== code) {
        setConferencia({ estado: 'vazio' })
        return
      }
      if (canonico !== code) setField('hub_code', canonico)
      setConferencia({ estado: 'achou', campanha: data, codigo: canonico })
    } catch (err) {
      if (meuPedido !== pedidoAtual.current) return
      /* ⚠️ 404 é o ÚNICO vermelho — é o caso em que o problema é do que a
         pessoa digitou. Todo o resto é problema NOSSO e vira âmbar: 503 (hub
         mudo, 401 por chave errada ou produto desmarcado, 429 pelo balde por
         IP), 502 (respondeu 200 com um corpo que não dá para usar) e 500.

         Escrever `status === 503 ? âmbar : erro` pintaria de vermelho um código
         perfeito no dia em que o hub trocasse o envelope da resposta — que é a
         falha mais provável do dia a dia. */
      const status = err?.response?.status
      setConferencia({
        estado: status === 404 ? 'inexistente' : 'naoConferido',
        codigo: code,
      })
    }
  }

  /* O veredito é DERIVADO do que voltou, e não guardado pronto: trocar o
     CLIENTE depois de conferir muda a resposta sem precisar de outra ida ao
     servidor — e sem isso a tela ficaria verde afirmando um cliente que não é
     mais o escolhido. */
  const veredito = useMemo(() => {
    const c = conferencia
    // ⚠️ `codigoDigitado` primeiro: sem isso, um campo JÁ PREENCHIDO (modo
    // edição, ou antes do primeiro blur) mostra "cole o código aqui" embaixo de
    // um código, e o estado "pendente" fica inalcançável.
    if (c.estado === 'vazio') return { tipo: codigoDigitado ? 'pendente' : 'idle' }
    // O código mudou depois da última conferência: o resultado antigo não vale
    // mais, e mostrar o verde de OUTRO código seria mentira.
    if ((c.codigo ?? '') !== codigoDigitado) {
      return { tipo: codigoDigitado ? 'pendente' : 'idle' }
    }
    if (c.estado !== 'achou') return { tipo: c.estado }

    const dono = ponteComoUUID(c.campanha?.cliente?.idNaPlataforma)
    /* ⚠️ Ponte AUSENTE ou ilegível NÃO é divergência: o cliente do hub ainda não
       foi ligado a este E-monitor, e não há o que comparar (§6.1). Tratar
       ausência como divergência barraria todo cliente ainda não ligado
       justamente na primeira campanha dele.

       ⚠️ E a comparação é por UUID, espelhando o `clienteDoHubConfere` do Go.
       Comparando string crua, um UUID sem hífen, com `urn:uuid:` ou com espaço
       em volta — todos que o `uuid.Parse` do servidor ACEITA — pintavam
       vermelho dizendo "do cliente Rôgga" com Rôgga sendo o cliente escolhido,
       e oferecendo uma saída que não existe. Medido em 2026-09-22. */
    if (dono && value.client_id && dono !== soHex(value.client_id)) {
      return { tipo: 'outroCliente', campanha: c.campanha }
    }
    return { tipo: 'achou', campanha: c.campanha }
  }, [conferencia, codigoDigitado, value.client_id])

  return (
    <div style={{
      display: 'grid',
      gridTemplateColumns: 'minmax(0,1fr) 360px',
      gap: 40,
      alignItems: 'start',
    }} className="wizard-basic-grid">

      {/* ───── Form column ───── */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 28 }}>

        {/* Section title */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <h2 style={{
            margin: 0, fontFamily: 'var(--font-heading)', fontWeight: 700,
            fontSize: 24, color: 'var(--c-text)', letterSpacing: '-0.01em',
          }}>
            Vamos começar pela base
          </h2>
          <p style={{ margin: 0, color: 'var(--c-text-2)', fontSize: 13, lineHeight: 1.55 }}>
            Dê um nome interno pra essa campanha e diga pra qual cliente ela é.
            O período define a janela em que as veiculações vão contar.
          </p>
        </div>

        {/* Name */}
        <FieldBlock label="Nome da campanha" required hint="Como aparecerá nos relatórios e nos webhooks.">
          <input
            className="input"
            placeholder="ex: Verão 2026 — Rôgga"
            value={value.name}
            onChange={e => setField('name', e.target.value)}
            autoFocus
            style={{ fontSize: 14, fontWeight: 500 }}
          />
        </FieldBlock>

        {/* Client */}
        <FieldBlock
          label="Cliente"
          required
          hint={isEditMode
            ? 'Cliente trava após criar a campanha — os materiais já carregados pertencem a ele.'
            : 'Quem é o anunciante. Define qual biblioteca de materiais estará disponível.'}
        >
          <RSelect
            options={clients.map(c => ({ value: c.id, label: c.name }))}
            value={selectedClient ? { value: selectedClient.id, label: selectedClient.name } : null}
            onChange={opt => setField('client_id', opt?.value ?? '')}
            placeholder="Selecione o cliente…"
            isClearable
            isDisabled={isEditMode}
          />
        </FieldBlock>

        {/* Código do hub */}
        <FieldBlock
          label="Código do hub"
          htmlFor="hub-code"
          required={!isEditMode}
        >
          <input
            id="hub-code"
            className="input"
            placeholder="EH-7K4M2X"
            value={value.hub_code ?? ''}
            onChange={e => setField('hub_code', e.target.value)}
            onBlur={e => conferir(e.target.value)}
            /* O corretor do navegador "conserta" códigos em maiúscula, e a
               capitalização automática do celular estraga o que foi colado. */
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            autoComplete="off"
            /* O asterisco do rótulo é só tinta: quem usa leitor de tela precisa
               do `required`. E o `aria-describedby` é o que faz o resultado da
               conferência ser lido ao voltar ao campo com Tab — sem ele, quem
               tomou o vermelho não ouve nada ao tentar de novo. */
            required={!isEditMode}
            aria-describedby="hub-code-estado"
            aria-invalid={veredito.tipo === 'inexistente' || veredito.tipo === 'outroCliente'}
            style={{ fontSize: 14, fontWeight: 500, letterSpacing: '0.04em' }}
          />
          {/* ⚠️ Altura reservada. O resultado chega ao SAIR do campo, que é o
              instante em que a pessoa já está indo clicar na data logo abaixo —
              um bloco aparecendo aqui empurraria o alvo do clique no meio do
              movimento. Reservando, a dica e o resultado ocupam o mesmo espaço
              e nada se mexe. */}
          {/* ⚠️ `role="status"` mora AQUI, no wrapper que nunca desmonta, e não
              no bloco colorido: vários leitores de tela não anunciam uma região
              viva que aparece no DOM já com texto dentro. Estando o wrapper
              sempre montado, quem troca é só o filho.

              ⚠️ 56 e não 42: medido em 2026-09-22, o bloco de "outro cliente"
              ocupa 54,8px a 1280 e 72,2px a 390. Os 42 só bastavam a partir de
              1440 — abaixo disso ele empurrava o campo de data, que é o alvo do
              clique seguinte, que é exatamente o que reservar altura evita. */}
          <div
            id="hub-code-estado"
            role="status"
            style={{ minHeight: 56, display: 'flex', alignItems: 'center' }}
          >
            <ConferenciaDoCodigo veredito={veredito} />
          </div>
        </FieldBlock>

        {/* Dates */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
          <FieldBlock label="Início" required>
            <input
              className="input"
              type="date"
              value={value.start_date}
              onChange={e => setField('start_date', e.target.value)}
            />
          </FieldBlock>
          <FieldBlock label="Fim" required>
            <input
              className="input"
              type="date"
              value={value.end_date}
              min={value.start_date}
              onChange={e => setField('end_date', e.target.value)}
            />
          </FieldBlock>
        </div>

        {days != null && (
          <div style={{
            padding: '10px 14px', borderRadius: 'var(--radius-md)',
            background: 'var(--c-action-light, rgba(232,30,117,0.08))',
            color: 'var(--c-action)',
            fontSize: 12, fontWeight: 600,
            display: 'flex', alignItems: 'center', gap: 8,
            width: 'fit-content',
          }}>
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
              <rect x="2" y="3" width="12" height="11" rx="1.5" />
              <path d="M11 1.5v3M5 1.5v3M2 6.5h12" />
            </svg>
            Campanha de {days} {days === 1 ? 'dia' : 'dias'} corridos
          </div>
        )}
      </div>

      {/* ───── Preview column ───── */}
      <aside style={{
        position: 'sticky', top: 24,
        background: 'var(--c-surface)',
        border: '1px solid var(--c-border)',
        borderRadius: 'var(--radius-xl)',
        padding: 24,
        display: 'flex', flexDirection: 'column', gap: 18,
        boxShadow: 'var(--shadow-sm)',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <span style={{
            fontSize: 10, fontWeight: 700, letterSpacing: '0.12em',
            color: 'var(--c-text-3)', textTransform: 'uppercase',
          }}>
            Pré-visualização
          </span>
          <span style={{
            width: 8, height: 8, borderRadius: '50%',
            background: value.name ? 'var(--c-action)' : 'var(--c-surface-2)',
            transition: 'background 200ms',
          }} />
        </div>

        <div>
          <div style={{
            fontFamily: 'var(--font-heading)', fontWeight: 700,
            fontSize: 18, color: 'var(--c-text)', lineHeight: 1.25,
            wordBreak: 'break-word',
            minHeight: 22,
          }}>
            {value.name || (
              <span style={{ color: 'var(--c-text-3)', fontWeight: 500 }}>
                Sua campanha aparece aqui
              </span>
            )}
          </div>
          {selectedClient ? (
            <div style={{ fontSize: 12, color: 'var(--c-text-2)', marginTop: 6, fontWeight: 500 }}>
              {selectedClient.name}
            </div>
          ) : (
            <div style={{ fontSize: 12, color: 'var(--c-text-3)', marginTop: 6 }}>
              Cliente não selecionado
            </div>
          )}
        </div>

        <div style={{ height: 1, background: 'var(--c-border)' }} />

        <PreviewRow label="Início" value={fmtDate(value.start_date)} />
        <PreviewRow label="Fim"    value={fmtDate(value.end_date)} />
        <PreviewRow label="Duração" value={days != null ? `${days} ${days === 1 ? 'dia' : 'dias'}` : '—'} />

        <div style={{
          marginTop: 4, padding: '10px 12px',
          background: 'var(--c-bg)', borderRadius: 'var(--radius-md)',
          border: '1px dashed var(--c-border)',
          fontSize: 11, color: 'var(--c-text-3)', lineHeight: 1.55,
        }}>
          <strong style={{ color: 'var(--c-text-2)', fontWeight: 600 }}>Próximo:</strong>{' '}
          escolher quais emissoras vão monitorar essa campanha.
        </div>
      </aside>

      {/* Responsive: collapse the preview below the form on narrow screens */}
      <style>{`
        @media (max-width: 960px) {
          .wizard-basic-grid {
            grid-template-columns: minmax(0,1fr) !important;
          }
          .wizard-basic-grid aside {
            position: static !important;
          }
        }
      `}</style>
    </div>
  )
}

function FieldBlock({ label, required, hint, children, htmlFor }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      {/* `htmlFor` é opcional para não mexer nos campos que já existiam, mas
          quem o passa ganha o vínculo de verdade: clicar no rótulo foca o
          campo, e o leitor de tela anuncia os dois juntos. */}
      <label htmlFor={htmlFor} style={{
        fontSize: 12, fontWeight: 600,
        color: 'var(--c-text)',
        fontFamily: 'var(--font-heading)',
        display: 'flex', alignItems: 'center', gap: 4,
      }}>
        {label}
        {required && <span style={{ color: 'var(--c-action)', fontWeight: 700 }}>*</span>}
      </label>
      {children}
      {hint && (
        <span style={{ fontSize: 11, color: 'var(--c-text-3)', lineHeight: 1.5 }}>
          {hint}
        </span>
      )}
    </div>
  )
}

/*
As quatro caras da §4.3, e a razão de cada cor.

⚠️ O texto NÃO é a cor de status crua. Medido contra o branco: `--c-success` dá
3,30:1 e `--c-warning` 2,89:1 — os dois abaixo dos 4,5:1 que a AA pede para
texto, e o âmbar abaixo até dos 3:1 de elemento gráfico. Escurecendo a própria
cor em 28% eles passam, e continuam tingidos da própria hue, que é o que o texto
sobre superfície colorida pede (cinza ali seria lavado).

⚠️ E a conta é contra o FUNDO DESTE BLOCO, não contra o branco da página — o
bloco pinta o próprio fundo, e é sobre ele que o texto assenta. Medido no
navegador em 2026-09-22, lendo o `getComputedStyle` dos dois:

    verde    rgb(16,117,53)  sobre rgb(234,247,239)  =  5,23:1
    vermelho rgb(158,27,27)  sobre rgb(252,235,235)  =  6,93:1
    âmbar    rgb(145,99,3)   sobre rgb(250,245,232)  =  4,80:1

Os três passam a AA para texto normal, mas a margem do âmbar é 0,30 e não a
0,65 que a medição contra branco sugeria: mexer na tinta do fundo tira o âmbar
da conformidade antes de qualquer aviso.

⚠️ E a cor nunca fala sozinha: cada estado tem um ÍCONE de forma diferente
(certo, cruz, triângulo) e uma frase que diz o que houve. Quem não distingue
verde de vermelho lê a mesma coisa.
*/
function ConferenciaDoCodigo({ veredito }) {
  const { tipo } = veredito

  if (tipo === 'idle') {
    return (
      <span style={dicaStyle}>
        Crie a campanha no hub primeiro e cole o código aqui — é ele que liga as duas.
      </span>
    )
  }
  if (tipo === 'pendente') {
    return <span style={dicaStyle}>Confere quando você sair do campo.</span>
  }
  if (tipo === 'conferindo') {
    return (
      <span style={{ ...dicaStyle, display: 'inline-flex', alignItems: 'center', gap: 8 }}>
        <span style={{
          width: 12, height: 12, borderRadius: '50%', flexShrink: 0,
          border: '2px solid var(--c-border)',
          borderTopColor: 'var(--c-action)',
          animation: 'spin 0.7s linear infinite',
        }} />
        Conferindo com a Central…
      </span>
    )
  }

  const cores = {
    achou: 'var(--c-success)',
    outroCliente: 'var(--c-danger)',
    inexistente: 'var(--c-danger)',
    naoConferido: 'var(--c-warning)',
  }
  const cor = cores[tipo] ?? 'var(--c-text-2)'
  const campanha = veredito.campanha

  const conteudo = {
    achou: campanha && (
      <>
        {/* ⚠️ Sem este prefixo, o leitor de tela anuncia só "Verão 2026 ·
            01/12/2026 a 28/02/2027" — nada diz que o código CONFERE, e o ícone
            é decorativo. Era o único estado em que a cor falava sozinha. */}
        <span>Confere: </span>
        <strong style={{ fontWeight: 700 }}>{campanha.nome}</strong>
        {campanha.inicio && campanha.fim && (
          <span style={{ opacity: 0.85 }}>
            {' · '}{fmtDate(campanha.inicio)} a {fmtDate(campanha.fim)}
          </span>
        )}
      </>
    ),
    outroCliente: (
      <>
        Esse código é da campanha <strong style={{ fontWeight: 700 }}>{campanha?.nome}</strong>,
        {' '}do cliente <strong style={{ fontWeight: 700 }}>{campanha?.cliente?.nome}</strong>.
        {' '}Escolha esse cliente acima ou cole outro código.
      </>
    ),
    inexistente: <>Código não encontrado na Central. Confira no cabeçalho da campanha, no hub.</>,
    naoConferido: <>Não deu para conferir agora. Pode seguir: o código é conferido ao salvar.</>,
  }[tipo]

  return (
    <span
      style={{
        display: 'inline-flex', alignItems: 'flex-start', gap: 8,
        padding: '9px 12px',
        borderRadius: 'var(--radius-md)',
        background: `color-mix(in srgb, ${cor} 9%, var(--c-surface))`,
        border: `1px solid color-mix(in srgb, ${cor} 30%, var(--c-surface))`,
        color: `color-mix(in srgb, ${cor} 72%, #000)`,
        fontSize: 12, fontWeight: 500, lineHeight: 1.45,
      }}
    >
      <IconeDoVeredito tipo={tipo} />
      <span>{conteudo}</span>
    </span>
  )
}

// Ícones desenhados, no mesmo traço 1.75 do resto desta tela.
function IconeDoVeredito({ tipo }) {
  const comum = {
    width: 15, height: 15, viewBox: '0 0 16 16', fill: 'none',
    stroke: 'currentColor', strokeWidth: 1.75,
    strokeLinecap: 'round', strokeLinejoin: 'round',
    // Decorativo: o texto ao lado já diz o desfecho, e um SVG sem nome
    // acessível seria anunciado como "imagem" sem conteúdo.
    'aria-hidden': true, focusable: false,
    style: { flexShrink: 0, marginTop: 1 },
  }
  if (tipo === 'achou') {
    return <svg {...comum}><circle cx="8" cy="8" r="6.25" /><path d="M5.3 8.2l1.9 1.9 3.5-4" /></svg>
  }
  if (tipo === 'naoConferido') {
    return <svg {...comum}><path d="M8 2.1 1.7 13.2h12.6L8 2.1Z" /><path d="M8 6.3v3.1M8 11.4h.01" /></svg>
  }
  return <svg {...comum}><circle cx="8" cy="8" r="6.25" /><path d="M6.1 6.1l3.8 3.8M9.9 6.1l-3.8 3.8" /></svg>
}

const dicaStyle = { fontSize: 11, color: 'var(--c-text-3)', lineHeight: 1.5 }

function PreviewRow({ label, value }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
      <span style={{ fontSize: 11, color: 'var(--c-text-3)', fontWeight: 500, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
        {label}
      </span>
      <span style={{ fontSize: 13, color: 'var(--c-text)', fontWeight: 600, fontFamily: 'var(--font-heading)' }}>
        {value}
      </span>
    </div>
  )
}
