import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import api from '../api/client'
import { useConfirm } from './ConfirmModal'
import './CalibrationPanel.css'

// ── Helpers ────────────────────────────────────────────────────────────────────

function formatDate(iso) {
  if (!iso) return null
  try {
    const d = new Date(iso)
    return d.toLocaleDateString('pt-BR', {
      day: '2-digit',
      month: 'short',
      year: 'numeric',
    })
  } catch {
    return null
  }
}

function daysBetween(iso) {
  if (!iso) return 0
  const start = new Date(iso).getTime()
  if (Number.isNaN(start)) return 0
  const ms = Date.now() - start
  return Math.max(0, Math.floor(ms / (1000 * 60 * 60 * 24)))
}

function pluralize(n, singular, plural) {
  return `${n} ${n === 1 ? singular : plural}`
}

// ── Icons ──────────────────────────────────────────────────────────────────────

function GaugeIcon({ size = 20 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor"
         strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M12 14l4-4" />
      <path d="M3.5 18a9 9 0 1 1 17 0" />
      <circle cx="12" cy="14" r="1.25" />
    </svg>
  )
}

function RefreshIcon({ size = 18 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor"
         strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M21 12a9 9 0 1 1-3-6.7" />
      <path d="M21 4v5h-5" />
    </svg>
  )
}

function ZapIcon({ size = 18 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor"
         strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M13 3 4 14h7l-1 7 9-11h-7l1-7Z" />
    </svg>
  )
}

function EmptyIllustration() {
  // Soft, schematic threshold/gauge — no text, no decoration spam.
  return (
    <svg width="56" height="56" viewBox="0 0 56 56" fill="none"
         xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
      <path d="M8 38a20 20 0 0 1 40 0" stroke="currentColor" strokeWidth="2.4"
            strokeLinecap="round" opacity="0.45" />
      <path d="M14 38a14 14 0 0 1 28 0" stroke="currentColor" strokeWidth="2.4"
            strokeLinecap="round" />
      <path d="M28 38l9-9" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" />
      <circle cx="28" cy="38" r="2.6" fill="currentColor" />
    </svg>
  )
}

// ── Sub-components ─────────────────────────────────────────────────────────────

function StatusChip({ calibrating }) {
  if (calibrating) {
    return (
      <span className="cal-chip cal-chip-calibrating">
        <span className="cal-chip-dot" />
        Em calibração
      </span>
    )
  }
  return (
    <span className="cal-chip cal-chip-active">
      <span className="cal-chip-dot" />
      Ativo
    </span>
  )
}

function StatePanel({ data }) {
  const minHashes = data?.min_hashes ?? 0
  const noiseP99 = data?.noise_p99
  const calibrating = !!data?.calibration_mode

  const startedAt = data?.calibration_started_at
  const days = daysBetween(startedAt)
  const progress = Math.min(days / 7, 1) * 100

  return (
    <div className="cal-state">
      <div className="cal-state-header">
        <div className="cal-metric-block">
          <div className="cal-metric-value">
            {minHashes}
            <span
              className="cal-metric-help"
              title="Quantos hashes precisam casar dentro de uma janela de 4s pra contar como hit. Quanto maior, menos ruído consegue forjar uma detecção — e mais difícil confirmar comerciais com áudio degradado."
              aria-label="O que é min_hashes"
            >?</span>
          </div>
          <span className="cal-metric-label">Mínimo de hashes p/ confirmar</span>
          <span className="cal-metric-sub">
            Threshold do matching. Quanto maior, menos falsos positivos.
          </span>
        </div>
        <StatusChip calibrating={calibrating} />
      </div>

      <div className="cal-state-row">
        <div className="cal-metric-block">
          {noiseP99 == null ? (
            <>
              <div className="cal-metric-value-sm cal-metric-value-muted">—</div>
              <span className="cal-metric-label">Pico de ruído (P99)</span>
              <span className="cal-metric-sub">
                Esperando o worker enviar as primeiras amostras de janelas sem comercial tocando.
              </span>
            </>
          ) : (
            <>
              <div className="cal-metric-value-sm">{Number(noiseP99).toFixed(2)}</div>
              <span className="cal-metric-label">Pico de ruído (P99)</span>
              <span className="cal-metric-sub">
                Em 99% das janelas sem comercial, o ruído fica abaixo desse valor.
                <br />
                Fórmula: <code>min_hashes = max(P99 × 1.5, 5)</code>.
              </span>
            </>
          )}
        </div>
      </div>

      {calibrating && (
        <div className="cal-callout">
          <strong>O que está acontecendo agora:</strong> o worker observa janelas de 4s
          quando nenhum comercial está tocando e registra quantos hashes casam por acaso.
          Depois de 7 dias, o sistema calcula o pico desse ruído (P99) e fixa
          <code> min_hashes </code> num valor seguro acima dele. Detecções funcionam
          durante esse período, mas o threshold pode ainda ajustar.
        </div>
      )}

      {calibrating && startedAt && (
        <div className="cal-progress-wrap">
          <div className="cal-progress-meta">
            <span>
              Iniciada em <strong>{formatDate(startedAt)}</strong>
              {' · '}
              <strong>{pluralize(days, 'dia decorrido', 'dias decorridos')}</strong> de 7
            </span>
            <span>{Math.round(progress)}%</span>
          </div>
          <div className="cal-progress-track" aria-label="Progresso da calibração" role="progressbar"
               aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(progress)}>
            <div className="cal-progress-fill" style={{ width: `${progress}%` }} />
          </div>
        </div>
      )}
    </div>
  )
}

// ── Main component ────────────────────────────────────────────────────────────

export default function CalibrationPanel({ stationId }) {
  const qc = useQueryClient()
  const confirm = useConfirm()

  const [refreshState, setRefreshState] = useState({ kind: 'idle', message: '' })
  const [recalState,   setRecalState]   = useState({ kind: 'idle', message: '' })

  const thresholdQuery = useQuery({
    queryKey: ['station-threshold', stationId],
    queryFn: async () => {
      const { data } = await api.get(`/stations/${stationId}/threshold`)
      return data
    },
    enabled: !!stationId,
    refetchInterval: 30_000,
    staleTime: 10_000,
  })

  const refreshMutation = useMutation({
    mutationFn: async () => {
      const { data } = await api.post(`/admin/stations/${stationId}/threshold/refresh`)
      return data
    },
    onMutate: () => setRefreshState({ kind: 'idle', message: '' }),
    onSuccess: () => {
      setRefreshState({ kind: 'success', message: 'Atualização enfileirada' })
    },
    onError: (err) => {
      const status = err?.response?.status
      if (status === 404) {
        setRefreshState({
          kind: 'error',
          message:
            'Nenhum worker rodando para esta estação no momento — operação requer campanha ativa.',
        })
      } else {
        const detail =
          err?.response?.data?.detail ||
          err?.response?.data?.message ||
          'Falha ao enfileirar atualização.'
        setRefreshState({ kind: 'error', message: detail })
      }
    },
  })

  const recalMutation = useMutation({
    mutationFn: async () => {
      const { data } = await api.post('/admin/calibration/run', { station_id: stationId })
      return data
    },
    onMutate: () => setRecalState({ kind: 'idle', message: '' }),
    onSuccess: (data) => {
      setRecalState({
        kind: 'success',
        message: data?.duration_ms
          ? `Recalibração concluída em ${data.duration_ms} ms`
          : 'Recalibração concluída',
      })
      qc.invalidateQueries({ queryKey: ['station-threshold', stationId] })
    },
    onError: (err) => {
      const detail =
        err?.response?.data?.detail ||
        err?.response?.data?.message ||
        'Falha ao executar recalibração.'
      setRecalState({ kind: 'error', message: detail })
    },
  })

  async function handleForceRecal() {
    const ok = await confirm(
      'Vai limpar as amostras de ruído acumuladas e recomeçar a calibração. Continuar?',
    )
    if (!ok) return
    recalMutation.mutate()
  }

  // ── Render ──────────────────────────────────────────────

  const data = thresholdQuery.data
  const isLoading = thresholdQuery.isLoading
  const isError = thresholdQuery.isError

  // Empty state: never monitored — everything is null/zero
  const isEmpty =
    !!data &&
    !data.calibration_mode &&
    (data.noise_p99 == null) &&
    (!data.min_hashes || data.min_hashes === 0) &&
    !data.calibration_started_at

  return (
    <div className="cal-section">
      <h3 className="cal-section-title">
        <span className="cal-section-title-dot" aria-hidden="true" />
        Calibração & Threshold
      </h3>
      <p className="cal-section-intro">
        Cada emissora tem seu próprio nível de ruído acústico (compressão, processamento, qualidade do stream).
        O sistema observa esse ruído por 7 dias e fixa um <code>min_hashes</code> seguro pra evitar
        que ruído casual seja confundido com um comercial. Este painel mostra o estado atual
        e permite intervir quando algo mudou.
      </p>
      <div className="cal-section-body">
        {isLoading && (
          <div className="cal-skeleton-grid" aria-busy="true" aria-live="polite">
            <div className="cal-skeleton-card" />
            <div className="cal-skeleton-card" />
          </div>
        )}

        {isError && !isLoading && (
          <div className="cal-empty">
            <div className="cal-empty-icon" aria-hidden="true">
              <EmptyIllustration />
            </div>
            <div className="cal-empty-text">
              <p className="cal-empty-title">Não foi possível carregar o threshold</p>
              <p className="cal-empty-desc">
                Tente novamente em alguns instantes. Se o erro persistir, verifique se a estação
                ainda existe e se o backend está acessível.
              </p>
            </div>
          </div>
        )}

        {!isLoading && !isError && isEmpty && (
          <div className="cal-empty">
            <div className="cal-empty-icon" aria-hidden="true">
              <EmptyIllustration />
            </div>
            <div className="cal-empty-text">
              <p className="cal-empty-title">Esta estação ainda não foi monitorada</p>
              <p className="cal-empty-desc">
                A calibração começa automaticamente quando uma campanha ativa inclui esta estação.
                O sistema coleta amostras de ruído por 7 dias antes de fixar o threshold.
              </p>
            </div>
          </div>
        )}

        {!isLoading && !isError && !isEmpty && data && (
          <div className="cal-grid">
            {/* ── Left: estado atual ── */}
            <StatePanel data={data} />

            {/* ── Right: ações de operador ── */}
            <div className="cal-actions">
              <article className="cal-action-card">
                <div className="cal-action-head">
                  <div className="cal-action-icon" aria-hidden="true">
                    <RefreshIcon />
                  </div>
                  <div className="cal-action-text">
                    <span className="cal-action-title">Atualizar threshold no worker</span>
                    <span className="cal-action-desc">
                      Faz o worker que está rodando agora reler <code>min_hashes</code> do banco
                      e aplicar imediatamente, sem precisar reiniciar a campanha.
                    </span>
                    <span className="cal-action-when">
                      <strong>Quando usar:</strong> depois que você editou <code>min_hashes</code> manualmente
                      no banco e quer ver o efeito agora. A calibração automática (a cada 5min e
                      no fim do ciclo de 7 dias) já faz isso sozinha — só use o botão se não
                      quiser esperar o próximo refresh.
                    </span>
                    <span className="cal-action-pre">
                      <strong>Pré-requisito:</strong> precisa ter uma campanha ativa que inclua esta estação
                      (sem worker rodando, retorna 404).
                    </span>
                  </div>
                </div>
                <div className="cal-action-foot">
                  {refreshState.kind !== 'idle' ? (
                    <span
                      className={
                        refreshState.kind === 'success'
                          ? 'cal-action-status cal-action-status-success'
                          : 'cal-action-status cal-action-status-error'
                      }
                    >
                      <span className="cal-action-status-dot" />
                      {refreshState.message}
                    </span>
                  ) : <span className="cal-action-status cal-action-status-info" />}
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm cal-action-button"
                    onClick={() => refreshMutation.mutate()}
                    disabled={refreshMutation.isPending}
                  >
                    {refreshMutation.isPending ? 'Enfileirando…' : 'Atualizar agora'}
                  </button>
                </div>
              </article>

              <article className="cal-action-card">
                <div className="cal-action-head">
                  <div className="cal-action-icon cal-action-icon-danger" aria-hidden="true">
                    <ZapIcon />
                  </div>
                  <div className="cal-action-text">
                    <span className="cal-action-title">Forçar recalibração agora</span>
                    <span className="cal-action-desc">
                      Apaga todas as amostras de ruído já coletadas e recomeça os 7 dias do zero.
                      Vai voltar ao estado <em>Em calibração</em> e o threshold só será refixado
                      ao fim do novo ciclo.
                    </span>
                    <span className="cal-action-when">
                      <strong>Quando usar:</strong> apenas quando o ambiente acústico mudou de verdade —
                      troca de transmissor/processador, mudança de formato da emissora, novo padrão
                      de áudio. Nessas situações o ruído antigo não representa mais a realidade.
                    </span>
                    <span className="cal-action-warn">
                      <strong>Quando NÃO usar:</strong> não acione só porque uma detecção saiu errada
                      ou porque o threshold parece "alto demais". O ciclo de 7 dias existe pra
                      suavizar variações pontuais; recomeçar a cada problema deixa o sistema
                      preso em modo calibração indefinidamente.
                    </span>
                  </div>
                </div>
                <div className="cal-action-foot">
                  {recalState.kind !== 'idle' ? (
                    <span
                      className={
                        recalState.kind === 'success'
                          ? 'cal-action-status cal-action-status-success'
                          : 'cal-action-status cal-action-status-error'
                      }
                    >
                      <span className="cal-action-status-dot" />
                      {recalState.message}
                    </span>
                  ) : <span className="cal-action-status cal-action-status-info" />}
                  <button
                    type="button"
                    className="btn btn-primary btn-sm cal-action-button"
                    onClick={handleForceRecal}
                    disabled={recalMutation.isPending}
                  >
                    <GaugeIcon size={14} />
                    <span style={{ marginLeft: 6 }}>
                      {recalMutation.isPending ? 'Recalibrando…' : 'Forçar recalibração'}
                    </span>
                  </button>
                </div>
              </article>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
