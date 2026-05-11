import RSelect from '../../components/RSelect'

/**
 * Step 1 of the wizard: campaign basic data.
 *
 * Props:
 *  - value: { name, client_id, start_date, end_date }
 *  - onChange: (newValue) => void
 *  - clients: Array<{id, name}>
 *  - isEditMode: bool — disables certain fields in edit mode (e.g. client_id)
 */
export default function BasicDataStep({ value, onChange, clients, isEditMode }) {
  function setField(k, v) {
    onChange({ ...value, [k]: v })
  }

  return (
    <div style={{ maxWidth: 640, margin: '0 auto' }}>
      <h3 style={{ marginTop: 0, fontSize: 16 }}>Dados básicos</h3>

      <div className="field">
        <label>Nome da campanha *</label>
        <input
          className="input"
          placeholder="ex: Verão 2026"
          value={value.name}
          onChange={e => setField('name', e.target.value)}
          autoFocus
        />
      </div>

      <div className="field">
        <label>Cliente *</label>
        <RSelect
          options={clients.map(c => ({ value: c.id, label: c.name }))}
          value={
            clients.find(c => c.id === value.client_id)
              ? { value: value.client_id, label: clients.find(c => c.id === value.client_id).name }
              : null
          }
          onChange={opt => setField('client_id', opt?.value ?? '')}
          placeholder="Selecione o cliente…"
          isClearable
          isDisabled={isEditMode}
        />
        {isEditMode && (
          <p style={{ fontSize: 12, color: '#64748b', marginTop: 4 }}>
            Cliente não pode ser alterado após criar a campanha.
          </p>
        )}
      </div>

      <div className="form-row">
        <div className="field">
          <label>Início *</label>
          <input
            className="input"
            type="date"
            value={value.start_date}
            onChange={e => setField('start_date', e.target.value)}
          />
        </div>
        <div className="field">
          <label>Fim *</label>
          <input
            className="input"
            type="date"
            value={value.end_date}
            min={value.start_date}
            onChange={e => setField('end_date', e.target.value)}
          />
        </div>
      </div>
    </div>
  )
}
