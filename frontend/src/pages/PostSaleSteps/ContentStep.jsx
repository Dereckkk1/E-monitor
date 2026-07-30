// ContentStep.jsx — passo 2: os números e o texto, do jeito que o cliente lê.
//
// Os campos de valor vêm PRÉ-PREENCHIDOS com o número do sistema e são
// editáveis. Só viram override quando o valor DIFERE do calculado — se viesse
// tudo como override, todo pós-venda congelaria números à mão e o rastro de
// "quem é do sistema, quem é meu" desapareceria.
//
// O CPM é derivado (valor ÷ impactos × 1000) e recalcula enquanto se digita:
// um CPM digitado contradiria os dois números exibidos ao lado dele.
import { useState } from 'react'

import StationAvatar from '../../components/StationAvatar'
import { stationDial } from '../../components/postsale/motion'
import { IconChevron, IconTrash } from './icons'

const brl = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })
const int = new Intl.NumberFormat('pt-BR', { maximumFractionDigits: 0 })

/** Compara com tolerância de centavo: 2712.5 e 2712.50 são o mesmo número. */
function differs(a, b) {
  if (a == null || b == null) return false
  return Math.abs(Number(a) - Number(b)) > 0.005
}

function cpmOf(valor, impactos) {
  const v = Number(valor) || 0
  const i = Number(impactos) || 0
  if (i <= 0) return 0
  return (v / i) * 1000
}

/**
 * ValueField — um número editável com o valor do sistema como referência.
 *
 * `value` é o que está no formulário; `system` é o que o /insights calculou.
 * Quando divergem, mostra de quanto era e oferece o desfazer.
 */
function ValueField({ label, value, system, onChange, money = false, step = '1' }) {
  const changed = differs(value, system)
  const fmt = money ? brl.format : (n) => int.format(Math.round(n))

  return (
    <div className={money ? 'pv-money' : 'pv-count'}>
      <label>
        <span className="pv-label">{label}</span>
        {money && <span className="pv-money-prefix" aria-hidden="true">R$</span>}
        <input
          className="input"
          type="number"
          inputMode="decimal"
          min="0"
          step={step}
          value={value ?? ''}
          onChange={e => onChange(e.target.value === '' ? null : Number(e.target.value))}
        />
      </label>
      <div className="pv-value-foot">
        {changed ? (
          <>
            <span>sistema: {fmt(system ?? 0)}</span>
            <button type="button" className="pv-reset" onClick={() => onChange(system ?? 0)}>
              usar do sistema
            </button>
          </>
        ) : (
          <span>do sistema</span>
        )}
      </div>
    </div>
  )
}

function RowEditor({ row, onChange, onRemove }) {
  return (
    <li className="pv-row">
      <StationAvatar station={{ name: row.name, logo_url: row.logo_url }} size={32} />

      <span className="pv-row-name">
        {row.name}
        <span className="pv-row-dial">{stationDial(row)}</span>
      </span>

      <label>
        <span className="pv-label">Entrega %</span>
        <input
          className="input"
          type="number"
          min="0"
          max="999"
          value={row.delivery_pct ?? ''}
          onChange={e => onChange({
            ...row,
            delivery_pct: e.target.value === '' ? null : Number(e.target.value),
          })}
        />
      </label>

      {row.kind === 'above' ? (
        <label>
          {/* Quantidade, não dinheiro — o campo em R$ da campanha se chama
              "Valor bonificado". Dois campos com o nome "Bonificação" na mesma
              tela mandaram o admin procurar quantidade onde só tinha valor. */}
          <span className="pv-label">Inserções bônus</span>
          <input
            className="input"
            type="number"
            min="0"
            value={row.bonus_count ?? 0}
            onChange={e => onChange({ ...row, bonus_count: Number(e.target.value) })}
          />
        </label>
      ) : (
        <label>
          <span className="pv-label">Observação</span>
          <input
            className="input"
            type="text"
            placeholder="ex.: compensação em 05/08"
            value={row.note ?? ''}
            onChange={e => onChange({ ...row, note: e.target.value })}
          />
        </label>
      )}

      <button type="button" className="btn btn-icon btn-sm" title={`Remover ${row.name} da lista`}
              onClick={() => onRemove(row)}>
        <IconTrash />
      </button>
    </li>
  )
}

function CampaignPanel({ block, preview, open, onToggle, onChange }) {
  const k = preview?.kpis
  const rows = block.checking_edited
    ? (block.checking_rows ?? [])
    : (preview?.checking_rows ?? [])
  const above = rows.filter(r => r.kind === 'above')
  const comp = rows.filter(r => r.kind === 'compensation')

  // Total de emissoras do período: verdade do banco, não do que está na lista.
  const total = (preview?.checking_rows?.length ?? 0) + (preview?.conforming_count ?? 0)
  const conforming = Math.max(0, total - rows.length)

  // Valor no formulário: o override quando existe, senão o do sistema.
  const ov = block.kpi_overrides ?? {}
  const valor = ov.valor_entregue ?? k?.valor_entregue ?? 0
  const impactos = ov.impactos ?? k?.impactos ?? 0
  const bonificacao = ov.bonificacao ?? k?.bonificacao ?? 0
  const manual =
    differs(ov.valor_entregue, k?.valor_entregue) ||
    differs(ov.impactos, k?.impactos) ||
    differs(ov.bonificacao, k?.bonificacao)

  function setOverride(field, value, systemValue) {
    const next = { ...ov }
    if (value == null || !differs(value, systemValue)) delete next[field]
    else next[field] = value
    onChange({ ...block, kpi_overrides: next })
  }

  function setRow(nextRow) {
    onChange({
      ...block,
      checking_edited: true,
      checking_rows: rows.map(r => (r.station_id === nextRow.station_id ? nextRow : r)),
    })
  }

  function removeRow(target) {
    onChange({
      ...block,
      checking_edited: true,
      checking_rows: rows.filter(r => r.station_id !== target.station_id),
    })
  }

  // Só acusa "sem veiculação" quando o cálculo JÁ VOLTOU: sem o `k`, o zero é
  // ausência de resposta, não ausência de tocada — e o selo aparecia enquanto
  // carregava, acusando falso.
  const zeroed = !!k && (k.impactos ?? 0) === 0 && (k.valor_entregue ?? 0) === 0

  return (
    <section className="pv-camp-block">
      <button type="button" className="pv-camp-block-head" aria-expanded={open} onClick={onToggle}>
        <div style={{ minWidth: 0 }}>
          <h3 className="pv-camp-block-title">{block.campaign_name ?? preview?.name ?? 'Campanha'}</h3>
          <p className="pv-camp-block-meta">
            {preview?.period_label ?? `${block.period_from} a ${block.period_to}`}
            {manual && <span className="pv-tag pv-tag--manual">ajustado à mão</span>}
            {zeroed && <span className="pv-tag pv-tag--zero">sem veiculação no período</span>}
          </p>
        </div>
        <IconChevron />
      </button>

      {open && (
        <div className="pv-camp-body pv-fade">
          {!preview && (
            <div aria-busy="true">
              <span className="pv-sk pv-sk-block" />
              <p className="pv-rest">
                Calculando os números desta campanha no período — o cálculo é o
                mesmo do /insights e pode levar alguns segundos.
              </p>
            </div>
          )}

          {preview && (
            <>
              <div>
                <div className="pv-values">
                  <ValueField
                    label="Valor entregue"
                    money
                    step="0.01"
                    value={valor}
                    system={k?.valor_entregue ?? 0}
                    onChange={v => setOverride('valor_entregue', v, k?.valor_entregue ?? 0)}
                  />
                  <ValueField
                    label="Impactos"
                    value={impactos}
                    system={k?.impactos ?? 0}
                    onChange={v => setOverride('impactos', v, k?.impactos ?? 0)}
                  />
                  {/* Em pricing consolidado a bonificação fica zerada por
                      definição e o documento nem mostra o card — um campo
                      editável aqui seria controle morto. */}
                  {!k?.consolidated && (
                    <ValueField
                      label="Valor bonificado"
                      money
                      step="0.01"
                      value={bonificacao}
                      system={k?.bonificacao ?? 0}
                      onChange={v => setOverride('bonificacao', v, k?.bonificacao ?? 0)}
                    />
                  )}
                  <div>
                    <span className="pv-label">CPM</span>
                    <div className="pv-derived">
                      <span className="pv-derived-value">{brl.format(cpmOf(valor, impactos))}</span>
                      <span className="pv-derived-note">valor ÷ impactos × 1000</span>
                    </div>
                  </div>
                </div>
                {zeroed && (
                  <p className="pv-rest">
                    Esta campanha não tem veiculação registrada no período escolhido —
                    confira o período antes de enviar, ou preencha os valores à mão.
                  </p>
                )}
              </div>

              <label className="pv-field">
                <span className="pv-label">Texto do Checking</span>
                <textarea
                  className="input"
                  rows={3}
                  value={block.checking_text ?? ''}
                  placeholder={preview?.checking_text ?? 'Mídia entregue com excelência! …'}
                  onChange={e => onChange({ ...block, checking_text: e.target.value })}
                />
                <span className="pv-value-foot">
                  Em branco, sai o texto sugerido que aparece no preview.
                </span>
              </label>

              <div>
                {above.length > 0 && (
                  <>
                    <p className="pv-group">
                      Acima do contratado
                      <span className="pv-group-count">{above.length}</span>
                    </p>
                    <ul className="pv-rows">
                      {above.map(r => (
                        <RowEditor key={r.station_id} row={r} onChange={setRow} onRemove={removeRow} />
                      ))}
                    </ul>
                  </>
                )}

                {comp.length > 0 && (
                  <>
                    <p className="pv-group pv-group--amber">
                      Compensações
                      <span className="pv-group-count">{comp.length}</span>
                    </p>
                    <ul className="pv-rows">
                      {comp.map(r => (
                        <RowEditor key={r.station_id} row={r} onChange={setRow} onRemove={removeRow} />
                      ))}
                    </ul>
                  </>
                )}

                <p className="pv-rest">
                  {conforming === 0
                    ? 'Todas as emissoras do período estão listadas acima.'
                    : `As outras ${conforming} ${conforming === 1 ? 'emissora entregou' : 'emissoras entregaram'} conforme o planejado.`}
                  {' '}Remover uma linha a move para essa contagem — o total do período não muda.
                </p>
              </div>
            </>
          )}
        </div>
      )}
    </section>
  )
}

export default function ContentStep({
  title, introMessage, blocks, preview, onMeta, onBlockChange,
}) {
  // Primeira campanha aberta; as outras dobradas. Com 3+ campanhas a página
  // inteira aberta viraria um rolo sem hierarquia.
  const [openId, setOpenId] = useState(blocks[0]?.campaign_id ?? null)
  const previewById = new Map((preview?.campaigns ?? []).map(c => [c.campaign_id, c]))

  return (
    <>
      <section className="pv-panel">
        <div className="pv-panel-head">
          <div>
            <h2 className="pv-panel-title">Abertura</h2>
            <p className="pv-panel-hint">
              É a primeira coisa que o cliente lê. Fale com ele, não sobre ele.
            </p>
          </div>
        </div>

        <label className="pv-field">
          <span className="pv-label">Título</span>
          <input
            className="input"
            type="text"
            value={title}
            onChange={e => onMeta({ title: e.target.value })}
          />
        </label>

        <label className="pv-field">
          <span className="pv-label">Mensagem</span>
          <textarea
            className="input"
            rows={4}
            value={introMessage}
            onChange={e => onMeta({ intro_message: e.target.value })}
          />
        </label>
      </section>

      {blocks.map(b => (
        <CampaignPanel
          key={b.campaign_id}
          block={b}
          preview={previewById.get(b.campaign_id)}
          open={openId === b.campaign_id}
          onToggle={() => setOpenId(openId === b.campaign_id ? null : b.campaign_id)}
          onChange={onBlockChange}
        />
      ))}
    </>
  )
}
