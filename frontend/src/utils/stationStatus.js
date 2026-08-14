// Rótulos de `stations.monitoring_status`. Compartilhado entre a listagem
// (/stations) e a ficha read-only da emissora, que renderizam o mesmo selo.
export const STATUS_META = {
  active:      { label: 'Ativa',              cls: 'badge-success' },
  calibrating: { label: 'Calibrando',         cls: 'badge-warning' },
  paused:      { label: 'Sem campanha ativa', cls: 'badge-neutral' },
  error:       { label: 'Erro',               cls: 'badge-danger'  },
}

export function statusMetaFor(status) {
  return STATUS_META[status] ?? { label: status, cls: 'badge-neutral' }
}
