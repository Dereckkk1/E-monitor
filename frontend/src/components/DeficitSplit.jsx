import './DeficitSplit.css'

// DeficitSplit — mostra o déficit separado nos DOIS tipos de falha.
//
// A migration 0065 tirou o out_slot do déficit (D3 do modelo por cota): tocar
// fora da faixa contratada não fecha mais a obrigação. Consequência: um dia
// inteiro veiculado no horário errado passa a ter déficit > 0. Como esse painel
// alimenta o PDF de cobrança que vai pra emissora, mostrar só "faltam N"
// acusaria de silêncio quem de fato veiculou (D7). Por isso os dois tipos
// aparecem sempre separados:
//
//   absent   → contratado e NADA foi ao ar        (vermelho, cor do déficit)
//   offSlot  → veiculou, mas fora da faixa        (âmbar, cor do out_slot no
//                                                  DayCell/BadgePill "yellow")
//
// Invariante do backend (campaign_failures.go): absent + offSlot === total.
//
// Props:
//  - total:   déficit total da linha (deficit / total_deficit)
//  - absent:  deficit_absent   (pode vir undefined de um backend antigo)
//  - offSlot: deficit_off_slot (idem)
//  - stack:   empilha os dois chips em coluna (usado em célula de tabela)
//  - align:   'start' | 'end' — alinhamento horizontal dos chips
export default function DeficitSplit({ total, absent, offSlot, stack = false, align = 'start' }) {
  const t = Number(total) || 0
  const hasSplit = Number.isFinite(absent) || Number.isFinite(offSlot)

  if (t <= 0) return null

  // Backend antigo (sem os campos do split): degrada pro rótulo de antes em vez
  // de renderizar dois zeros e sumir com o déficit da tela.
  if (!hasSplit) {
    return (
      <span className={`dsplit ${align === 'end' ? 'dsplit--end' : ''}`}>
        <span className="dsplit-tag dsplit-tag--absent">
          {t === 1 ? 'falta 1' : <>faltam <span className="dsplit-num">{t.toLocaleString('pt-BR')}</span></>}
        </span>
      </span>
    )
  }

  const a = Number(absent) || 0
  const o = Number(offSlot) || 0

  return (
    <span
      className={`dsplit ${stack ? 'dsplit--stack' : ''} ${align === 'end' ? 'dsplit--end' : ''}`}
      title={`Déficit ${t.toLocaleString('pt-BR')} = ${a.toLocaleString('pt-BR')} não tocou + ${o.toLocaleString('pt-BR')} fora do horário`}
    >
      {a > 0 && (
        <span className="dsplit-tag dsplit-tag--absent">
          não tocou <span className="dsplit-num">{a.toLocaleString('pt-BR')}</span>
        </span>
      )}
      {o > 0 && (
        <span className="dsplit-tag dsplit-tag--offslot">
          fora do horário <span className="dsplit-num">{o.toLocaleString('pt-BR')}</span>
        </span>
      )}
    </span>
  )
}
