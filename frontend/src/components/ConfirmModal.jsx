import { useState, useEffect, useCallback, useRef } from 'react'
import { createPortal } from 'react-dom'

let _confirmResolve = null
let _alertResolve = null

function ConfirmDialog({ message, onConfirm, onCancel }) {
  const cancelRef = useRef(null)

  useEffect(() => {
    cancelRef.current?.focus()

    function onKey(e) {
      if (e.key === 'Escape') onCancel()
      if (e.key === 'Enter')  onConfirm()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onConfirm, onCancel])

  return createPortal(
    <div className="confirm-backdrop" onClick={onCancel} aria-modal="true" role="dialog">
      <div className="confirm-card" onClick={e => e.stopPropagation()}>
        <div className="confirm-icon-wrap">
          <svg width="22" height="22" viewBox="0 0 22 22" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
            <path d="M11 2L2 19H20L11 2Z" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round"/>
            <path d="M11 9V13" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round"/>
            <circle cx="11" cy="16.5" r="0.75" fill="currentColor" stroke="currentColor" strokeWidth="0.5"/>
          </svg>
        </div>

        <div className="confirm-body">
          <p className="confirm-title">Confirmar ação</p>
          <p className="confirm-message">{message}</p>
        </div>

        <div className="confirm-actions">
          <button ref={cancelRef} className="btn btn-secondary btn-sm" onClick={onCancel}>
            Cancelar
          </button>
          <button className="btn btn-danger btn-sm" onClick={onConfirm}>
            Confirmar
          </button>
        </div>
      </div>
    </div>,
    document.body
  )
}

function AlertDialog({ message, onClose }) {
  const okRef = useRef(null)

  useEffect(() => {
    okRef.current?.focus()

    function onKey(e) {
      if (e.key === 'Escape' || e.key === 'Enter') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return createPortal(
    <div className="confirm-backdrop" onClick={onClose} aria-modal="true" role="dialog">
      <div className="confirm-card" onClick={e => e.stopPropagation()}>
        <div className="confirm-icon-wrap">
          <svg width="22" height="22" viewBox="0 0 22 22" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
            <circle cx="11" cy="11" r="9" stroke="currentColor" strokeWidth="1.75"/>
            <path d="M11 7V12" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round"/>
            <circle cx="11" cy="15" r="0.75" fill="currentColor" stroke="currentColor" strokeWidth="0.5"/>
          </svg>
        </div>

        <div className="confirm-body">
          <p className="confirm-title">Aviso</p>
          <p className="confirm-message">{message}</p>
        </div>

        <div className="confirm-actions">
          <button ref={okRef} className="btn btn-primary btn-sm" onClick={onClose}>
            OK
          </button>
        </div>
      </div>
    </div>,
    document.body
  )
}

export function ConfirmProvider({ children }) {
  const [pendingConfirm, setPendingConfirm] = useState(null)
  const [pendingAlert, setPendingAlert] = useState(null)

  useEffect(() => {
    const nativeConfirm = window.confirm
    const nativeAlert = window.alert

    window.confirm = (message) =>
      new Promise((resolve) => {
        _confirmResolve = resolve
        setPendingConfirm(message ?? '')
      })

    window.alert = (message) =>
      new Promise((resolve) => {
        _alertResolve = resolve
        setPendingAlert(String(message ?? ''))
      })

    return () => {
      window.confirm = nativeConfirm
      window.alert = nativeAlert
    }
  }, [])

  const handleConfirm = useCallback(() => {
    setPendingConfirm(null)
    _confirmResolve?.(true)
    _confirmResolve = null
  }, [])

  const handleCancel = useCallback(() => {
    setPendingConfirm(null)
    _confirmResolve?.(false)
    _confirmResolve = null
  }, [])

  const handleAlertClose = useCallback(() => {
    setPendingAlert(null)
    _alertResolve?.()
    _alertResolve = null
  }, [])

  return (
    <>
      {children}
      {pendingConfirm !== null && (
        <ConfirmDialog
          message={pendingConfirm}
          onConfirm={handleConfirm}
          onCancel={handleCancel}
        />
      )}
      {pendingAlert !== null && (
        <AlertDialog
          message={pendingAlert}
          onClose={handleAlertClose}
        />
      )}
    </>
  )
}
