// ClientStep.jsx — passo 1: para qual cliente é o pós-venda.
//
// Um cliente por vez, por definição: o documento é o fechamento de UM
// anunciante, e os destinatários saem dos usuários dele.
import { useMemo } from 'react'

import RSelect from '../../components/RSelect'

export default function ClientStep({ clients, clientId, onChange, recipients, recipientsLoading }) {
  const options = useMemo(
    () => clients.map(c => ({ value: c.id, label: c.name })),
    [clients],
  )
  const selected = options.find(o => o.value === clientId) ?? null
  const count = recipients?.length ?? 0

  return (
    <div className="psa-panel">
      <h2 className="psa-panel-title">Para qual cliente?</h2>
      <p className="psa-panel-hint">
        Todas as pessoas ativas com acesso a este cliente vão receber o email.
      </p>

      <RSelect
        options={options}
        value={selected}
        onChange={o => onChange(o?.value ?? null)}
        placeholder="Escolha o cliente…"
        isSearchable
      />

      {clientId && !recipientsLoading && (
        count === 0 ? (
          <p className="psa-hint psa-hint--warn">
            Este cliente não tem nenhum usuário ativo — cadastre um acesso em
            /admin/usuários antes de enviar o pós-venda.
          </p>
        ) : (
          <>
            <p className="psa-hint">
              {count === 1 ? '1 pessoa será notificada' : `${count} pessoas serão notificadas`}:
            </p>
            <ul className="psa-recipients">
              {recipients.map(r => (
                <li key={r.email}>{r.name || r.email} · {r.email}</li>
              ))}
            </ul>
          </>
        )
      )}
    </div>
  )
}
