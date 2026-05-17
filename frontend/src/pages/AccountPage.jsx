import { useState, useEffect } from 'react'
import { useMe, useUpdateMe, useChangeMyPassword } from '../api/hooks'

function formatErr(err) {
  if (!err) return null
  if (typeof err === 'string') return err
  return err.response?.data || err.message || String(err)
}

const errorBox = {
  padding: '10px 12px',
  borderRadius: 'var(--radius-md, 8px)',
  background: '#fee2e2',
  color: '#991b1b',
  fontSize: 13,
}
const successBox = {
  padding: '10px 12px',
  borderRadius: 'var(--radius-md, 8px)',
  background: '#dcfce7',
  color: '#166534',
  fontSize: 13,
}

export default function AccountPage() {
  const meQ = useMe()
  const updateM = useUpdateMe()
  const changeM = useChangeMyPassword()
  const [profile, setProfile] = useState({ name: '', phone: '' })
  const [pwd, setPwd] = useState({ current_password: '', new_password: '', confirm: '' })
  const [pErr, setPErr] = useState(null)
  const [pwdErr, setPwdErr] = useState(null)
  const [pOk, setPOk] = useState(false)
  const [pwdOk, setPwdOk] = useState(false)

  useEffect(() => {
    if (meQ.data) setProfile({ name: meQ.data.name ?? '', phone: meQ.data.phone ?? '' })
  }, [meQ.data])

  function saveProfile(e) {
    e.preventDefault()
    setPErr(null); setPOk(false)
    updateM.mutate({ name: profile.name, phone: profile.phone || null }, {
      onSuccess: () => setPOk(true),
      onError: (err) => setPErr(formatErr(err)),
    })
  }

  function changePwd(e) {
    e.preventDefault()
    setPwdErr(null); setPwdOk(false)
    if (pwd.new_password.length < 12) {
      setPwdErr('A nova senha precisa ter ao menos 12 caracteres.')
      return
    }
    if (pwd.new_password !== pwd.confirm) {
      setPwdErr('As senhas não conferem.')
      return
    }
    changeM.mutate(
      { current_password: pwd.current_password, new_password: pwd.new_password },
      {
        onSuccess: () => {
          setPwdOk(true)
          setPwd({ current_password: '', new_password: '', confirm: '' })
        },
        onError: (err) => setPwdErr(formatErr(err)),
      }
    )
  }

  if (meQ.isLoading) return <div style={{ padding: 24 }}>Carregando…</div>
  if (meQ.error) return <div style={{ padding: 24 }}>Erro ao carregar conta.</div>

  return (
    <div style={{ padding: 24, maxWidth: 600, margin: '0 auto' }}>
      <h1 style={{ margin: '0 0 8px', fontFamily: 'var(--font-heading)', fontSize: 24, color: 'var(--c-text)' }}>
        Minha conta
      </h1>
      <p style={{ margin: '0 0 24px', color: '#475569', fontSize: 14 }}>
        Atualize seus dados pessoais ou troque sua senha. O email é imutável.
      </p>

      <section style={{ marginBottom: 32 }}>
        <h2 style={{ margin: '0 0 12px', fontSize: 16, color: 'var(--c-text)' }}>Dados pessoais</h2>
        <form onSubmit={saveProfile} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div className="field">
            <label>Email (não editável)</label>
            <input className="input" value={meQ.data.email} disabled />
          </div>
          <div className="field">
            <label>Nome</label>
            <input className="input" value={profile.name} onChange={e => setProfile(p => ({ ...p, name: e.target.value }))} />
          </div>
          <div className="field">
            <label>Telefone</label>
            <input className="input" value={profile.phone} onChange={e => setProfile(p => ({ ...p, phone: e.target.value }))} placeholder="(11) 91234-5678" />
          </div>
          {pErr && <div style={errorBox}>{pErr}</div>}
          {pOk && <div style={successBox}>Dados atualizados.</div>}
          <div>
            <button type="submit" className="btn btn-primary" disabled={updateM.isPending}>
              {updateM.isPending ? 'Salvando…' : 'Salvar'}
            </button>
          </div>
        </form>
      </section>

      <section>
        <h2 style={{ margin: '0 0 12px', fontSize: 16, color: 'var(--c-text)' }}>Trocar senha</h2>
        <form onSubmit={changePwd} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div className="field">
            <label>Senha atual</label>
            <input className="input" type="password" value={pwd.current_password}
              onChange={e => setPwd(p => ({ ...p, current_password: e.target.value }))} required />
          </div>
          <div className="field">
            <label>Nova senha (mín 12 caracteres)</label>
            <input className="input" type="password" value={pwd.new_password}
              onChange={e => setPwd(p => ({ ...p, new_password: e.target.value }))} minLength={12} required />
          </div>
          <div className="field">
            <label>Confirmar nova senha</label>
            <input className="input" type="password" value={pwd.confirm}
              onChange={e => setPwd(p => ({ ...p, confirm: e.target.value }))} minLength={12} required />
          </div>
          {pwdErr && <div style={errorBox}>{pwdErr}</div>}
          {pwdOk && <div style={successBox}>Senha alterada.</div>}
          <div>
            <button type="submit" className="btn btn-primary" disabled={changeM.isPending}>
              {changeM.isPending ? 'Trocando…' : 'Trocar senha'}
            </button>
          </div>
        </form>
      </section>
    </div>
  )
}
