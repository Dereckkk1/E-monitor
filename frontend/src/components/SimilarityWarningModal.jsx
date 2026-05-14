import { useDeleteMaterial, useAcknowledgeSimilarity } from '../api/hooks'

/**
 * Modal that warns the operator that the uploaded material is highly similar
 * to another material of the same client. Two native HTML5 audio players
 * (left=new, right=existing) allow A/B comparison. Operator decides:
 *  - "Remover material novo" → DELETE /materials/{id}
 *  - "Manter assim mesmo"   → POST /materials/{id}/similarity/acknowledge
 *
 * Props:
 *  - newMaterial:      {id, title, duration_seconds, similarity_score}
 *  - similarMaterial:  {id, title, duration_seconds}
 *  - onClose:          () => void
 */
export default function SimilarityWarningModal({ newMaterial, similarMaterial, onClose }) {
  const del = useDeleteMaterial()
  const ack = useAcknowledgeSimilarity()
  const pct = Math.round((newMaterial.similarity_score ?? 0) * 100)

  async function handleRemove() {
    await del.mutateAsync(newMaterial.id)
    onClose()
  }
  async function handleKeep() {
    await ack.mutateAsync(newMaterial.id)
    onClose()
  }

  return (
    <div
      style={{
        position: 'fixed', inset: 0,
        background: 'rgba(15,23,42,0.55)', backdropFilter: 'blur(6px)',
        zIndex: 60,
        display: 'flex', alignItems: 'center', justifyContent: 'center',
      }}
      onClick={onClose}
    >
      <div
        onClick={e => e.stopPropagation()}
        style={{
          background: 'var(--c-surface)',
          borderRadius: 'var(--radius-lg)',
          boxShadow: 'var(--shadow-lg)',
          width: 'min(680px, 92vw)',
          padding: 24,
          display: 'flex', flexDirection: 'column', gap: 18,
        }}
      >
        <div>
          <div style={{
            fontSize: 11, fontWeight: 700, letterSpacing: '0.12em',
            color: '#a16207', textTransform: 'uppercase',
          }}>
            ⚠ Atenção: material similar
          </div>
          <h3 style={{
            margin: '6px 0 0', fontFamily: 'var(--font-heading)', fontWeight: 700,
            fontSize: 18, color: 'var(--c-text)',
          }}>
            Este material é {pct}% similar a "{similarMaterial.title}"
          </h3>
          <p style={{ margin: '8px 0 0', color: 'var(--c-text-2)', fontSize: 13 }}>
            Tem certeza que quer manter os dois? Materiais duplicados poluem o
            relatório de veiculação (ambos disparam).
          </p>
        </div>

        <div style={{
          display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14,
        }}>
          <PlayerCard
            label="NOVO"
            title={newMaterial.title}
            durationSeconds={newMaterial.duration_seconds}
            audioUrl={`/v1/internal/materials/${newMaterial.id}/audio`}
          />
          <PlayerCard
            label="EXISTENTE"
            title={similarMaterial.title}
            durationSeconds={similarMaterial.duration_seconds}
            audioUrl={`/v1/internal/materials/${similarMaterial.id}/audio`}
          />
        </div>

        <div style={{
          display: 'flex', justifyContent: 'flex-end', gap: 10,
          paddingTop: 8, borderTop: '1px solid var(--c-border)',
        }}>
          <button
            onClick={handleRemove}
            disabled={del.isPending}
            style={{
              padding: '9px 16px', borderRadius: 'var(--radius-md)',
              background: 'var(--c-danger)', color: '#fff',
              border: 0, cursor: 'pointer',
              fontSize: 12, fontWeight: 700, fontFamily: 'var(--font-heading)',
            }}
          >
            {del.isPending ? 'Removendo…' : 'Remover material novo'}
          </button>
          <button
            onClick={handleKeep}
            disabled={ack.isPending}
            style={{
              padding: '9px 16px', borderRadius: 'var(--radius-md)',
              background: 'var(--c-action)', color: '#fff',
              border: 0, cursor: 'pointer',
              fontSize: 12, fontWeight: 700, fontFamily: 'var(--font-heading)',
            }}
          >
            {ack.isPending ? 'Salvando…' : 'Manter assim mesmo'}
          </button>
        </div>
      </div>
    </div>
  )
}

function PlayerCard({ label, title, durationSeconds, audioUrl }) {
  return (
    <div style={{
      background: 'var(--c-bg)',
      border: '1px solid var(--c-border)',
      borderRadius: 'var(--radius-md)',
      padding: 12,
      display: 'flex', flexDirection: 'column', gap: 8,
    }}>
      <div style={{
        fontSize: 10, fontWeight: 700, letterSpacing: '0.10em',
        color: 'var(--c-text-3)',
      }}>{label}</div>
      <div style={{
        fontSize: 13, fontWeight: 600, color: 'var(--c-text)',
        whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
      }}>{title}</div>
      <div style={{ fontSize: 11, color: 'var(--c-text-3)' }}>
        {durationSeconds?.toFixed(1) ?? '—'}s
      </div>
      <audio controls preload="metadata" src={audioUrl} style={{ width: '100%' }} />
    </div>
  )
}
