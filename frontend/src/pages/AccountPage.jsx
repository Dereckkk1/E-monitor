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

function initialsFor(name, email) {
  const source = (name || '').trim() || email || ''
  if (!source) return '?'
  const parts = source.split(/\s+/).filter(Boolean)
  if (parts.length >= 2) return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase()
  return source.slice(0, 2).toUpperCase()
}

function classifyRole(role) {
  if (role === 'viewer') return { label: 'Cliente', kind: 'client' }
  return { label: 'Administrador', kind: 'admin' }
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
  // 0-1 fraca, 2-3 média, 4-5 forte
  if (score <= 1) return { score: 1, label: 'Fraca',  cls: 's-weak'   }
  if (score <= 3) return { score: 3, label: 'Média',  cls: 's-medium' }
  return { score: 5, label: 'Forte', cls: 's-strong' }
}

// ── Inline icons (mantém consistência com o resto do projeto: SVG) ───

const Icon = {
  shield: <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg>,
  check:  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"><path d="M20 6L9 17l-5-5"/></svg>,
  x:      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"><path d="M18 6L6 18M6 6l12 12"/></svg>,
  alert:  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>,
  eyeOn:  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>,
  eyeOff: <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"/><path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/><path d="M14.12 14.12a3 3 0 1 1-4.24-4.24"/><line x1="1" y1="1" x2="23" y2="23"/></svg>,
  err:    <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>,
}

// ── Toast system (não-bloqueante, 1 toast por hook simples) ──

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
  const icon = toast.kind === 'success' ? Icon.check : toast.kind === 'error' ? Icon.alert : Icon.check
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

// ── Skeleton (replica forma exata do conteúdo) ───────────────

function AccountSkeleton() {
  return (
    <div className="account-page">
      <div className="account-header">
        <div className="account-skel" style={{ width: 180, height: 28, marginBottom: 8 }} />
        <div className="account-skel" style={{ width: 280, height: 14 }} />
      </div>

      {/* Identity strip skeleton */}
      <div className="account-identity">
        <div className="account-skel account-skel-circle" style={{ width: 48, height: 48 }} />
        <div style={{ flex: 1 }}>
          <div className="account-skel" style={{ width: '40%', height: 16, marginBottom: 6 }} />
          <div className="account-skel" style={{ width: '60%', height: 12, marginBottom: 8 }} />
          <div className="account-skel account-skel-pill" style={{ width: 90, height: 18 }} />
        </div>
      </div>

      {/* Card 1 skeleton */}
      <div className="account-card">
        <div className="account-card-head">
          <div className="account-skel" style={{ width: 120, height: 16 }} />
        </div>
        {[60, 100, 80].map((w, i) => (
          <div key={i} style={{ marginBottom: 14 }}>
            <div className="account-skel" style={{ width: w, height: 11, marginBottom: 6 }} />
            <div className="account-skel" style={{ width: '100%', height: 38 }} />
          </div>
        ))}
      </div>

      {/* Card 2 skeleton */}
      <div className="account-card">
        <div className="account-card-head">
          <div className="account-skel" style={{ width: 80, height: 16 }} />
        </div>
        {[90, 110, 130].map((w, i) => (
          <div key={i} style={{ marginBottom: 14 }}>
            <div className="account-skel" style={{ width: w, height: 11, marginBottom: 6 }} />
            <div className="account-skel" style={{ width: '100%', height: 38 }} />
          </div>
        ))}
      </div>
    </div>
  )
}

// ── Error state (load do /me falhou) ─────────────────────────

function AccountError({ onRetry }) {
  return (
    <div className="account-page">
      <div className="account-error-state">
        <div className="account-error-icon">{Icon.err}</div>
        <h3 className="account-error-title">Não foi possível carregar sua conta</h3>
        <p className="account-error-msg">Tente novamente em instantes. Se persistir, contate o administrador.</p>
        <button className="btn btn-secondary btn-sm" onClick={onRetry}>Tentar novamente</button>
      </div>
    </div>
  )
}

// ─────────────────────────────────────────────────────────────
//  Página principal
// ─────────────────────────────────────────────────────────────

export default function AccountPage() {
  const meQ = useMe()
  const updateM = useUpdateMe()
  const changeM = useChangeMyPassword()
  const { clientId } = useAuth()
  // Só carrega clients se o user é viewer (admin pega 403 — useClients já trata)
  // e usamos pra mostrar o nome do cliente vinculado na identity strip.
  const clientsQ = useClients({ enabled: !!clientId })
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

  // Profile dirty detection — desabilita Salvar quando nada mudou
  const profileDirty = useMemo(() => {
    if (!meQ.data) return false
    return (
      (meQ.data.name ?? '') !== profile.name ||
      (meQ.data.phone ?? '') !== profile.phone
    )
  }, [meQ.data, profile])

  // Password strength
  const strength = useMemo(() => passwordStrength(pwd.new_password), [pwd.new_password])

  // Confirm validation em tempo real (mais útil que esperar submit)
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

  // ─── Estados de loading/erro ──────────────────────────────
  if (meQ.isLoading) return <AccountSkeleton />
  if (meQ.error)     return <AccountError onRetry={() => meQ.refetch()} />

  const me = meQ.data
  const roleInfo = classifyRole(me.role)
  const linkedClient = clientId
    ? (clientsQ.data ?? []).find(c => c.id === clientId)
    : null

  return (
    <div className="account-page">
      <header className="account-header">
        <h1>Minha conta</h1>
        <p className="account-header-sub">
          Atualize seus dados de contato ou troque sua senha. Email não pode ser alterado.
        </p>
      </header>

      {/* ─── Identity strip ─────────────────────────────────── */}
      <div className="account-identity" aria-label="Identidade da conta">
        <div className="account-identity-avatar" aria-hidden="true">
          {initialsFor(me.name, me.email)}
        </div>
        <div className="account-identity-body">
          <div className="account-identity-name">{me.name || me.email}</div>
          <div className="account-identity-email" title={me.email}>{me.email}</div>
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
            <span className={`account-identity-tag ${roleInfo.kind === 'client' ? 'account-identity-tag-client' : ''}`}>
              {Icon.shield} {roleInfo.label}
            </span>
            {linkedClient && (
              <span className="account-identity-tag">
                {linkedClient.name}
              </span>
            )}
          </div>
        </div>
      </div>

      {/* ─── Card: Dados pessoais ───────────────────────────── */}
      <section className="account-card">
        <div className="account-card-head">
          <h2 className="account-card-title">Dados pessoais</h2>
          <span className="account-card-meta">
            {profileDirty ? 'alterações não salvas' : 'tudo certo'}
          </span>
        </div>

        <form className="account-form" onSubmit={saveProfile} noValidate>
          <div className="field">
            <label htmlFor="acc-name">Nome</label>
            <input
              id="acc-name"
              className="input"
              value={profile.name}
              onChange={e => setProfile(p => ({ ...p, name: e.target.value }))}
              placeholder="Seu nome completo"
              autoComplete="name"
              disabled={updateM.isPending}
              maxLength={120}
            />
          </div>

          <div className="field">
            <label htmlFor="acc-phone">Telefone</label>
            <input
              id="acc-phone"
              className="input"
              value={profile.phone}
              onChange={e => setProfile(p => ({ ...p, phone: e.target.value }))}
              placeholder="(11) 91234-5678"
              autoComplete="tel"
              disabled={updateM.isPending}
              maxLength={32}
            />
          </div>

          <div className="field">
            <label htmlFor="acc-email">Email</label>
            <input
              id="acc-email"
              className="input"
              value={me.email}
              disabled
              aria-readonly="true"
            />
            <span className="field-hint">
              Email não pode ser alterado. Para trocar, o administrador precisa criar uma nova conta.
            </span>
          </div>

          <div className="account-form-foot">
            <span className="account-form-foot-hint">
              {!profileDirty && 'Nada a salvar.'}
            </span>
            <button
              type="submit"
              className="btn btn-primary"
              disabled={!profileDirty || updateM.isPending}
            >
              {updateM.isPending
                ? (<><span className="account-spinner" />Salvando…</>)
                : 'Salvar alterações'}
            </button>
          </div>
        </form>
      </section>

      {/* ─── Card: Senha ────────────────────────────────────── */}
      <section className="account-card">
        <div className="account-card-head">
          <h2 className="account-card-title">Senha</h2>
          <span className="account-card-meta">mínimo 12 caracteres</span>
        </div>

        <form className="account-form" onSubmit={changePwd} noValidate>
          <div className="field">
            <label htmlFor="acc-pwd-cur">Senha atual</label>
            <div className="account-pwd-row">
              <input
                id="acc-pwd-cur"
                className="input"
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

          <div className="field">
            <label htmlFor="acc-pwd-new">Nova senha</label>
            <div className="account-pwd-row">
              <input
                id="acc-pwd-new"
                className="input"
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
            {pwd.new_password.length > 0 && (
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
            )}
          </div>

          <div className="field">
            <label htmlFor="acc-pwd-conf">Confirmar nova senha</label>
            <div className="account-pwd-row">
              <input
                id="acc-pwd-conf"
                className="input"
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

          <div className="account-form-foot">
            <span className="account-form-foot-hint">
              Você precisará entrar de novo com a nova senha em outros dispositivos.
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

      <Toast toast={toast} onClose={dismiss} />
    </div>
  )
}
