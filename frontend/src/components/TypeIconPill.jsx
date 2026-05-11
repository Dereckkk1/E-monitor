/**
 * Vertical color bar representing a material type.
 * Width fixed at 5px, height inherits from container.
 *
 * Props:
 *  - color: hex string from material_types.color (e.g. '#3b82f6'). Defaults to gray.
 *  - height: pixel height (default 14)
 */
export default function TypeIconPill({ color = '#94a3b8', height = 14 }) {
  return (
    <span
      style={{
        display: 'inline-block',
        width: 5,
        height,
        borderRadius: 2,
        background: color,
        flexShrink: 0,
      }}
    />
  )
}
