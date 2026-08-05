import { useState, useEffect, useMemo, useRef } from 'react'
import { useMe, useUpdateMe, useChangeMyPassword, useClients } from '../api/hooks'
import { useAuth } from '../contexts/AuthContext'
import './AccountPage.css'

// ── Helpers ───────────────────────────────────────────────────

function formatErr(err) {
  if (!err) return null
  if (typeof err === 'string') return err
  if (err.response?.data) {
    if (typeof err.response.data === 'string') return err.response.data
    if (err.response.data.error) return err.response.data.error
    return JSON.stringify(err.response.data)
  }
  return err.message || String(err)
}

function classifyRole(role) {
  if (role === 'viewer') return { label: 'Cliente', kind: 'client' }
  return { label: 'Administrador', kind: 'admin' }
}

const MONTHS = ['jan', 'fev', 'mar', 'abr', 'mai', 'jun', 'jul', 'ago', 'set', 'out', 'nov', 'dez']
function parseDate(iso) {
  if (!iso) return null
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? null : d
}
function formatMemberSince(iso) {
  const d = parseDate(iso)
  return d ? `${MONTHS[d.getMonth()]} ${d.getFullYear()}` : null
}
function formatDateShort(iso) {
  const d = parseDate(iso)
  return d ? `${String(d.getDate()).padStart(2, '0')} ${MONTHS[d.getMonth()]} ${d.getFullYear()}` : null
}
function formatRelativeOrTime(iso) {
  const d = parseDate(iso)
  if (!d) return null
  const now = new Date()
  const sameDay = d.toDateString() === now.toDateString()
  const yesterday = new Date(now); yesterday.setDate(now.getDate() - 1)
  const isYesterday = d.toDateString() === yesterday.toDateString()
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  if (sameDay) return `hoje, ${hh}:${mm}`
  if (isYesterday) return `ontem, ${hh}:${mm}`
  // Mesma semana → "qua, HH:MM" não vale o ROI; vai pra data curta
  return `${String(d.getDate()).padStart(2, '0')} ${MONTHS[d.getMonth()]}, ${hh}:${mm}`
}

// Quick heuristic — não pretende substituir zxcvbn, só dar feedback inline
function passwordStrength(pwd) {
  if (!pwd) return { score: 0, label: '', cls: '' }
  let score = 0
  if (pwd.length >= 12) score += 1
  if (pwd.length >= 16) score += 1
  if (/[a-z]/.test(pwd) && /[A-Z]/.test(pwd)) score += 1
  if (/\d/.test(pwd)) score += 1
  if (/[^A-Za-z0-9]/.test(pwd)) score += 1
  if (score <= 1) return { score: 1, label: 'Fraca',  cls: 's-weak'   }
  if (score <= 3) return { score: 3, label: 'Média',  cls: 's-medium' }
  return { score: 5, label: 'Forte', cls: 's-strong' }
}

// ── Inline icons ──────────────────────────────────────────────

const Icon = {
  check:  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"><path d="M20 6L9 17l-5-5"/></svg>,
  x:      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"><path d="M18 6L6 18M6 6l12 12"/></svg>,
  alert:  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>,
  eyeOn:  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>,
  eyeOff: <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"/><path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/><path d="M14.12 14.12a3 3 0 1 1-4.24-4.24"/><line x1="1" y1="1" x2="23" y2="23"/></svg>,
  err:    <svg width="44" height="44" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>,
}

// ── Toast (não-bloqueante, full border — sem stripe lateral) ──

function useToast() {
  const [toast, setToast] = useState(null)
  const timeoutRef = useRef(null)

  function show(kind, title, msg, ttl = 4000) {
    if (timeoutRef.current) clearTimeout(timeoutRef.current)
    setToast({ kind, title, msg })
    if (ttl > 0) {
      timeoutRef.current = setTimeout(() => setToast(null), ttl)
    }
  }
  function dismiss() {
    if (timeoutRef.current) clearTimeout(timeoutRef.current)
    setToast(null)
  }
  useEffect(() => () => timeoutRef.current && clearTimeout(timeoutRef.current), [])

  return { toast, show, dismiss }
}

function Toast({ toast, onClose }) {
  if (!toast) return null
  const icon = toast.kind === 'success' ? Icon.check : Icon.alert
  return (
    <div className="account-toast-region" aria-live="polite">
      <div className={`account-toast t-${toast.kind}`} role="status">
        <span className="account-toast-icon">{icon}</span>
        <div className="account-toast-body">
          <p className="account-toast-title">{toast.title}</p>
          {toast.msg && <p className="account-toast-msg">{toast.msg}</p>}
        </div>
        <button className="account-toast-close" onClick={onClose} aria-label="Fechar">
          {Icon.x}
        </button>
      </div>
    </div>
  )
}

// ── Skeleton (document shape, não card grid) ─────────────────

function AccountSkeleton() {
  return (
    <div className="account-page">
      <header className="account-masthead">
        <div className="account-skel" style={{ width: 220, height: 36, marginBottom: 14 }} />
        <div className="account-skel" style={{ width: 320, height: 12 }} />
      </header>

      {[1, 2, 3].map(i => (
        <section key={i} className="account-section">
          <div className="account-section-head">
            <div className="account-skel" style={{ width: 36, height: 12 }} />
            <div className="account-skel" style={{ width: 140, height: 20 }} />
          </div>
          {[64, 96, 80].map((w, j) => (
            <div key={j} style={{ marginTop: 20 }}>
              <div className="account-skel" style={{ width: w, height: 10, marginBottom: 8 }} />
              <div className="account-skel" style={{ width: '100%', height: 36 }} />
            </div>
          ))}
        </section>
      ))}
    </div>
  )
}

// ── Error state ──────────────────────────────────────────────

function AccountError({ onRetry }) {
  return (
    <div className="account-page">
      <div className="account-error-state">
        <div className="account-error-icon">{Icon.err}</div>
        <h2 className="account-error-title">Não foi possível carregar sua conta</h2>
        <p className="account-error-msg">Tente novamente em instantes. Se persistir, contate o administrador.</p>
        <button className="btn btn-primary" onClick={onRetry}>Tentar novamente</button>
      </div>
    </div>
  )
}

// ── Section header (numeral + title inline + dirty signal) ───

function SectionHead({ numeral, title, dirty }) {
  return (
    <header className="account-section-head">
      <h2 className="account-section-title">
        <span className="account-section-numeral" aria-hidden="true">§ {numeral}</span>
        <span className="account-section-title-text">{title}</span>
        {dirty && <span className="account-section-dot" aria-label="alterações não salvas" />}
      </h2>
    </header>
  )
}

// ── Ficha técnica (aside desktop, oculta no mobile) ──────────

function FichaTecnica({ me }) {
  const criada    = formatDateShort(me.created_at)
  const ultimo    = formatRelativeOrTime(me.last_login_at)
  const atualizada = formatDateShort(me.updated_at)

  return (
    <aside className="account-ficha" aria-label="Ficha técnica da conta">
      <div className="account-ficha-eyebrow">ficha</div>

      <dl className="account-ficha-list">
        {criada && (
          <div className="account-ficha-row">
            <dt>conta criada</dt>
            <dd>{criada}</dd>
          </div>
        )}
        {ultimo && (
          <div className="account-ficha-row">
            <dt>último acesso</dt>
            <dd>{ultimo}</dd>
          </div>
        )}
        {atualizada && (
          <div className="account-ficha-row">
            <dt>atualizada</dt>
            <dd>{atualizada}</dd>
          </div>
        )}
        <div className="account-ficha-row">
          <dt>status</dt>
          <dd>
            <span className="account-ficha-status">
              <span className="account-ficha-status-dot" />
              ativa
            </span>
          </dd>
        </div>
      </dl>

      <p className="account-ficha-foot">
        Dados de leitura. Para alterar, fale com o administrador.
      </p>
    </aside>
  )
}

// ─────────────────────────────────────────────────────────────
//  Página principal
// ─────────────────────────────────────────────────────────────

export default function AccountPage() {
  const meQ = useMe()
  const updateM = useUpdateMe()
  const changeM = useChangeMyPassword()
  const { clientIds, isAdmin } = useAuth()
  // /clients é scope-aware: admin recebe a lista inteira, Cliente recebe a
  // própria carteira. Buscamos pra qualquer um que tenha vínculo — antes isto
  // era gateado por isAdmin, e o Cliente acabava sem ver vínculo nenhum.
  const clientsQ = useClients({ enabled: isAdmin || clientIds.length > 0 })
  const { toast, show, dismiss } = useToast()

  const [profile, setProfile] = useState({ name: '', phone: '' })
  const [pwd, setPwd] = useState({ current_password: '', new_password: '', confirm: '' })
  const [showCur, setShowCur] = useState(false)
  const [showNew, setShowNew] = useState(false)
  const [showConf, setShowConf] = useState(false)
  const [pwdInlineErr, setPwdInlineErr] = useState({ confirm: '' })

  useEffect(() => {
    if (meQ.data) {
      setProfile({ name: meQ.data.name ?? '', phone: meQ.data.phone ?? '' })
    }
  }, [meQ.data])

  const profileDirty = useMemo(() => {
    if (!meQ.data) return false
    return (
      (meQ.data.name ?? '') !== profile.name ||
      (meQ.data.phone ?? '') !== profile.phone
    )
  }, [meQ.data, profile])

  // Senha dirty: qualquer um dos três campos preenchido
  const passwordDirty = !!(pwd.current_password || pwd.new_password || pwd.confirm)

  const strength = useMemo(() => passwordStrength(pwd.new_password), [pwd.new_password])

  useEffect(() => {
    if (!pwd.confirm) {
      setPwdInlineErr(e => ({ ...e, confirm: '' }))
      return
    }
    if (pwd.new_password && pwd.confirm !== pwd.new_password) {
      setPwdInlineErr(e => ({ ...e, confirm: 'As senhas não conferem.' }))
    } else {
      setPwdInlineErr(e => ({ ...e, confirm: '' }))
    }
  }, [pwd.new_password, pwd.confirm])

  function saveProfile(e) {
    e.preventDefault()
    if (!profileDirty || updateM.isPending) return
    updateM.mutate(
      { name: profile.name.trim(), phone: profile.phone.trim() || null },
      {
        onSuccess: () => show('success', 'Dados atualizados', 'Suas alterações foram salvas.'),
        onError:   (err) => show('error',   'Não foi possível salvar',   formatErr(err), 0),
      }
    )
  }

  function changePwd(e) {
    e.preventDefault()
    if (changeM.isPending) return
    if (pwd.new_password.length < 12) {
      show('error', 'Senha muito curta', 'A nova senha precisa ter ao menos 12 caracteres.', 0)
      return
    }
    if (pwd.new_password !== pwd.confirm) {
      setPwdInlineErr({ confirm: 'As senhas não conferem.' })
      return
    }
    changeM.mutate(
      { current_password: pwd.current_password, new_password: pwd.new_password },
      {
        onSuccess: () => {
          setPwd({ current_password: '', new_password: '', confirm: '' })
          setShowCur(false); setShowNew(false); setShowConf(false)
          show('success', 'Senha alterada', 'Use a nova senha no próximo login.')
        },
        onError: (err) => show('error', 'Não foi possível alterar a senha', formatErr(err), 0),
      }
    )
  }

  if (meQ.isLoading) return <AccountSkeleton />
  if (meQ.error)     return <AccountError onRetry={() => meQ.refetch()} />

  const me = meQ.data
  const roleInfo = classifyRole(me.role)
  // Carteira completa, não só o principal: um usuário de agência precisa ver
  // TODOS os clientes que acessa. Resolve pelos ids da sessão contra a lista
  // scope-aware do /clients; ids que não resolvem são omitidos em vez de
  // derrubar a seção.
  const linkedClients = clientIds
    .map(id => (clientsQ.data ?? []).find(c => c.id === id))
    .filter(Boolean)
  const memberSince = formatMemberSince(me.created_at)

  // Meta strip: papel · vínculo · membro desde. Com carteira grande a faixa
  // vira "N clientes" pra não estourar a linha — a lista completa fica na
  // seção Identidade logo abaixo.
  const metaParts = [roleInfo.label]
  if (linkedClients.length === 1) metaParts.push(linkedClients[0].name)
  else if (linkedClients.length > 1) metaParts.push(`${linkedClients.length} clientes`)
  else if (roleInfo.kind === 'admin') metaParts.push('E-radios')
  if (memberSince) metaParts.push(`membro desde ${memberSince}`)

  return (
    <div className="account-page">
      <header className="account-masthead">
        <div className="account-masthead-eyebrow">
          <span className="account-masthead-eyebrow-mark" aria-hidden="true" />
          conta · {roleInfo.kind === 'client' ? 'cliente' : 'operação'}
        </div>
        <h1 className="account-masthead-title">Minha conta</h1>
        <p className="account-masthead-meta">
          {metaParts.map((p, i) => (
            <span key={i} className="account-masthead-meta-part">
              {i > 0 && <span className="account-masthead-meta-sep" aria-hidden="true">·</span>}
              {p}
            </span>
          ))}
        </p>
      </header>

      <div className="account-grid">
        <div className="account-main">

      {/* ─── § 01 Identidade ──────────────────────────────────── */}
      <section className="account-section" aria-labelledby="acc-sec-id">
        <SectionHead numeral="01" title="Identidade" dirty={false} />

        <dl className="account-defs">
          <div className="account-def">
            <dt className="account-def-key">email</dt>
            <dd className="account-def-val" title={me.email}>{me.email}</dd>
          </div>
          <div className="account-def">
            <dt className="account-def-key">papel</dt>
            <dd className="account-def-val">
              <span className={`account-role-mark account-role-${roleInfo.kind}`} aria-hidden="true" />
              {roleInfo.label}
            </dd>
          </div>
          {linkedClients.length > 0 && (
            <div className="account-def">
              <dt className="account-def-key">
                {linkedClients.length > 1 ? 'vínculos' : 'vínculo'}
              </dt>
              <dd className="account-def-val">
                {linkedClients.map(c => c.name).join(' · ')}
              </dd>
            </div>
          )}
          {memberSince && (
            <div className="account-def">
              <dt className="account-def-key">membro desde</dt>
              <dd className="account-def-val account-def-val-mono">{memberSince}</dd>
            </div>
          )}
        </dl>

        <p className="account-section-note">
          Email, papel e vínculo só podem ser alterados pelo administrador.
        </p>
      </section>

      {/* ─── § 02 Dados pessoais ──────────────────────────────── */}
      <section className="account-section" aria-labelledby="acc-sec-personal">
        <SectionHead numeral="02" title="Dados pessoais" dirty={profileDirty} />

        <form className="account-form" onSubmit={saveProfile} noValidate>
          <div className="account-field">
            <label htmlFor="acc-name">nome</label>
            <input
              id="acc-name"
              className="account-input"
              value={profile.name}
              onChange={e => setProfile(p => ({ ...p, name: e.target.value }))}
              placeholder="seu nome completo"
              autoComplete="name"
              disabled={updateM.isPending}
              maxLength={120}
            />
          </div>

          <div className="account-field">
            <label htmlFor="acc-phone">telefone</label>
            <input
              id="acc-phone"
              className="account-input"
              value={profile.phone}
              onChange={e => setProfile(p => ({ ...p, phone: e.target.value }))}
              placeholder="(11) 91234-5678"
              autoComplete="tel"
              disabled={updateM.isPending}
              maxLength={32}
            />
          </div>

          <div className="account-section-foot">
            <span className="account-section-foot-hint">
              {profileDirty ? 'alterações não salvas' : 'nada a salvar'}
            </span>
            <button
              type="submit"
              className="btn btn-primary"
              disabled={!profileDirty || updateM.isPending}
            >
              {updateM.isPending
                ? (<><span className="account-spinner" />Salvando…</>)
                : 'Salvar'}
            </button>
          </div>
        </form>
      </section>

      {/* ─── § 03 Senha ───────────────────────────────────────── */}
      <section className="account-section" aria-labelledby="acc-sec-pwd">
        <SectionHead numeral="03" title="Senha" dirty={passwordDirty} />

        <form className="account-form" onSubmit={changePwd} noValidate>
          <div className="account-field">
            <label htmlFor="acc-pwd-cur">senha atual</label>
            <div className="account-pwd-row">
              <input
                id="acc-pwd-cur"
                className="account-input"
                type={showCur ? 'text' : 'password'}
                value={pwd.current_password}
                onChange={e => setPwd(p => ({ ...p, current_password: e.target.value }))}
                autoComplete="current-password"
                disabled={changeM.isPending}
                required
              />
              <button
                type="button"
                className="account-eye"
                onClick={() => setShowCur(s => !s)}
                aria-label={showCur ? 'Ocultar senha' : 'Mostrar senha'}
              >
                {showCur ? Icon.eyeOff : Icon.eyeOn}
              </button>
            </div>
          </div>

          <div className="account-field">
            <label htmlFor="acc-pwd-new">nova senha</label>
            <div className="account-pwd-row">
              <input
                id="acc-pwd-new"
                className="account-input"
                type={showNew ? 'text' : 'password'}
                value={pwd.new_password}
                onChange={e => setPwd(p => ({ ...p, new_password: e.target.value }))}
                autoComplete="new-password"
                minLength={12}
                disabled={changeM.isPending}
                required
              />
              <button
                type="button"
                className="account-eye"
                onClick={() => setShowNew(s => !s)}
                aria-label={showNew ? 'Ocultar senha' : 'Mostrar senha'}
              >
                {showNew ? Icon.eyeOff : Icon.eyeOn}
              </button>
            </div>
            {pwd.new_password.length > 0 ? (
              <div className="account-pwd-strength" aria-live="polite">
                <div className="account-pwd-strength-track">
                  <div
                    className={`account-pwd-strength-fill ${strength.cls}`}
                    style={{ width: `${(strength.score / 5) * 100}%` }}
                  />
                </div>
                <span className={`account-pwd-strength-label ${strength.cls}`}>
                  {strength.label}
                </span>
              </div>
            ) : (
              <span className="account-field-hint">mínimo 12 caracteres</span>
            )}
          </div>

          <div className="account-field">
            <label htmlFor="acc-pwd-conf">confirmar nova senha</label>
            <div className="account-pwd-row">
              <input
                id="acc-pwd-conf"
                className="account-input"
                type={showConf ? 'text' : 'password'}
                value={pwd.confirm}
                onChange={e => setPwd(p => ({ ...p, confirm: e.target.value }))}
                autoComplete="new-password"
                minLength={12}
                disabled={changeM.isPending}
                required
              />
              <button
                type="button"
                className="account-eye"
                onClick={() => setShowConf(s => !s)}
                aria-label={showConf ? 'Ocultar senha' : 'Mostrar senha'}
              >
                {showConf ? Icon.eyeOff : Icon.eyeOn}
              </button>
            </div>
            {pwdInlineErr.confirm && (
              <div className="account-field-error">
                {Icon.alert} {pwdInlineErr.confirm}
              </div>
            )}
          </div>

          <div className="account-section-foot">
            <span className="account-section-foot-hint">
              Você precisará entrar de novo em outros dispositivos.
            </span>
            <button
              type="submit"
              className="btn btn-primary"
              disabled={
                changeM.isPending ||
                !pwd.current_password ||
                pwd.new_password.length < 12 ||
                pwd.new_password !== pwd.confirm
              }
            >
              {changeM.isPending
                ? (<><span className="account-spinner" />Alterando…</>)
                : 'Alterar senha'}
            </button>
          </div>
        </form>
      </section>

        </div>{/* /.account-main */}

        <FichaTecnica me={me} />
      </div>{/* /.account-grid */}

      <Toast toast={toast} onClose={dismiss} />
    </div>
  )
}
