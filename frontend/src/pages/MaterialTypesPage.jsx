import { useEffect, useRef, useState } from 'react'
import {
  useMaterialTypes, useCreateMaterialType,
  useUpdateMaterialType, useDeleteMaterialType,
} from '../api/hooks'
import { useConfirm } from '../components/ConfirmModal'

export default function MaterialTypesPage() {
  const { data: types = [], isLoading } = useMaterialTypes()
  const create = useCreateMaterialType()
  const update = useUpdateMaterialType()
  const del = useDeleteMaterialType()
  const confirm = useConfirm()
  const [editing, setEditing] = useState(null) // null | 'new' | type object
  const [form, setForm] = useState({ name: '', color: '#94a3b8', description: '' })

  function openNew() {
    setForm({ name: '', color: '#94a3b8', description: '' })
    setEditing('new')
  }
  function openEdit(t) {
    setForm({ name: t.name, color: t.color, description: t.description ?? '' })
    setEditing(t)
  }
  async function save() {
    const payload = { name: form.name, color: form.color, description: form.description || null }
    if (editing === 'new') {
      await create.mutateAsync(payload)
    } else {
      await update.mutateAsync({ id: editing.id, ...payload })
    }
    setEditing(null)
  }
  async function handleDelete(t) {
    const ok = await confirm(`Excluir o tipo "${t.name}"?`)
    if (!ok) return
    del.mutate(t.id)
  }

  return (
    <div>
      <div className="page-header">
        <h2>Tipos de material</h2>
        <button onClick={openNew} className="btn btn-primary btn-sm">+ Novo tipo</button>
      </div>

      {isLoading ? <SkeletonList /> : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {types.map(t => (
            <div key={t.id} style={{
              padding: 12, background: '#fff', border: '1px solid #e2e8f0',
              borderRadius: 8, display: 'flex', alignItems: 'center', gap: 12,
            }}>
              <span style={{ width: 6, height: 24, borderRadius: 2, background: t.color }} />
              <div style={{ flex: 1 }}>
                <div style={{ fontWeight: 600 }}>{t.name}</div>
                {t.description && <div style={{ fontSize: 11, color: '#64748b' }}>{t.description}</div>}
              </div>
              <button onClick={() => openEdit(t)} className="btn btn-secondary btn-sm">Editar</button>
              <button onClick={() => handleDelete(t)} className="btn btn-danger-ghost btn-sm">🗑</button>
            </div>
          ))}
        </div>
      )}

      {editing && (
        <div style={{
          position: 'fixed', inset: 0, background: 'rgba(15,23,42,0.4)',
          display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 50,
        }} onClick={() => setEditing(null)}>
          <div onClick={e => e.stopPropagation()} style={{
            background: '#fff', padding: 24, borderRadius: 12, width: 400,
          }}>
            <h3 style={{ marginTop: 0 }}>{editing === 'new' ? 'Novo tipo' : 'Editar tipo'}</h3>
            <div className="field">
              <label>Nome *</label>
              <input className="input" value={form.name}
                onChange={e => setForm({ ...form, name: e.target.value })} autoFocus />
            </div>
            <div className="field">
              <label>Cor</label>
              <ColorPicker
                value={form.color}
                onChange={color => setForm({ ...form, color })}
              />
            </div>
            <div className="field">
              <label>Descrição (opcional)</label>
              <input className="input" value={form.description}
                onChange={e => setForm({ ...form, description: e.target.value })} />
            </div>
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 16 }}>
              <button onClick={() => setEditing(null)} className="btn btn-secondary btn-sm">Cancelar</button>
              <button onClick={save} disabled={!form.name} className="btn btn-primary btn-sm">Salvar</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

// Mirrors the loaded card geometry: barra colorida 6×24, nome (largura
// variada), descrição opcional só em algumas linhas, dois botões à direita.
const SKEL_ROWS = [
  { nameW: 96,  descW: 0   },
  { nameW: 130, descW: 140 },
  { nameW: 78,  descW: 0   },
  { nameW: 112, descW: 96  },
  { nameW: 86,  descW: 0   },
]

const PALETTE = [
  '#64748b', '#94a3b8', '#0ea5e9', '#3b82f6',
  '#10b981', '#22c55e', '#84cc16', '#14b8a6',
  '#eab308', '#f59e0b', '#f97316', '#ef4444',
  '#f43f5e', '#ec4899', '#d946ef', '#8b5cf6',
]

const HEX_RE = /^#[0-9a-fA-F]{6}$/

function ColorPicker({ value, onChange }) {
  const normalized = (value || '').toLowerCase()
  const hexInputRef = useRef(null)
  const [draft, setDraft] = useState((value || '').toUpperCase())

  // Sync external value -> draft, except while the user is actively typing.
  useEffect(() => {
    if (document.activeElement === hexInputRef.current) return
    const up = (value || '').toUpperCase()
    if (HEX_RE.test(value) && up !== draft) setDraft(up)
  }, [value, draft])

  function handleHexChange(e) {
    let v = e.target.value.trim()
    if (v && !v.startsWith('#')) v = '#' + v
    v = v.slice(0, 7).toUpperCase()
    setDraft(v)
    if (HEX_RE.test(v)) onChange(v.toLowerCase())
  }

  function handleNativeChange(e) {
    const v = e.target.value
    setDraft(v.toUpperCase())
    onChange(v)
  }

  return (
    <div className="color-picker">
      <div className="color-picker-palette" role="radiogroup" aria-label="Cores predefinidas">
        {PALETTE.map(c => {
          const selected = c.toLowerCase() === normalized
          return (
            <button
              key={c}
              type="button"
              role="radio"
              aria-checked={selected}
              aria-label={`Cor ${c}`}
              title={c.toUpperCase()}
              className={`color-picker-chip${selected ? ' is-selected' : ''}`}
              style={{ color: c }}
              onClick={() => onChange(c)}
            >
              {selected && (
                <svg
                  className="color-picker-chip-check"
                  width="14" height="14" viewBox="0 0 16 16" fill="none"
                  stroke="currentColor" strokeWidth="2.5"
                  strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"
                >
                  <path d="M3 8.5l3 3 7-7" />
                </svg>
              )}
            </button>
          )
        })}
      </div>

      <div className="color-picker-custom">
        <label className="color-picker-custom-swatch" style={{ color: HEX_RE.test(value) ? value : '#94a3b8' }} title="Escolher cor personalizada">
          <input
            type="color"
            className="color-picker-native"
            value={HEX_RE.test(value) ? value : '#94a3b8'}
            onChange={handleNativeChange}
            aria-label="Selecionar cor personalizada"
          />
        </label>
        <input
          ref={hexInputRef}
          type="text"
          className="input color-picker-hex"
          value={draft}
          onChange={handleHexChange}
          maxLength={7}
          spellCheck={false}
          placeholder="#94A3B8"
          aria-label="Cor em hexadecimal"
        />
      </div>
    </div>
  )
}

function SkeletonList() {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      {SKEL_ROWS.map((r, i) => (
        <div key={i} style={{
          padding: 12, background: '#fff', border: '1px solid #e2e8f0',
          borderRadius: 8, display: 'flex', alignItems: 'center', gap: 12,
        }}>
          <span className="skeleton" style={{ width: 6, height: 24, borderRadius: 2, flexShrink: 0 }} />
          <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 5 }}>
            <span className="skeleton" style={{ width: r.nameW, height: 13, borderRadius: 4 }} />
            {r.descW > 0 && (
              <span className="skeleton" style={{ width: r.descW, height: 10, borderRadius: 3 }} />
            )}
          </div>
          <span className="skeleton" style={{ width: 58, height: 28, borderRadius: 6, flexShrink: 0 }} />
          <span className="skeleton" style={{ width: 32, height: 28, borderRadius: 6, flexShrink: 0 }} />
        </div>
      ))}
    </div>
  )
}
