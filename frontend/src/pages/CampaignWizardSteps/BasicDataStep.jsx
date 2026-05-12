import { useMemo } from 'react'
import RSelect from '../../components/RSelect'

function fmtDate(iso) {
  if (!iso) return '—'
  const [y, m, d] = iso.slice(0, 10).split('-')
  return `${d}/${m}/${y}`
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
 *  - value: { name, client_id, start_date, end_date }
 *  - onChange: (newValue) => void
 *  - clients: Array<{id, name}>
 *  - isEditMode: bool — disables client field after creation
 */
export default function BasicDataStep({ value, onChange, clients, isEditMode }) {
  function setField(k, v) {
    onChange({ ...value, [k]: v })
  }

  const selectedClient = useMemo(
    () => clients.find(c => c.id === value.client_id) ?? null,
    [clients, value.client_id]
  )

  const days = diffInDays(value.start_date, value.end_date)

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

function FieldBlock({ label, required, hint, children }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <label style={{
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
