// Client half of the capbus bundle: one row in the sidebar panel list.
//
// It asks the Host half for /capbus/state, so nothing is authenticated in the
// browser and no token is ever shipped to it. Everything renders with the
// host's theme tokens, with a literal fallback so a renamed token degrades the
// look instead of breaking the panel.
window.__ModuleLoader__.load({
  id: '@local/capbus',
  factory(require) {
    const React = require('react')
    const h = React.createElement
    const { useState, useEffect, useCallback } = React

    const tone = (n) => {
      if (!n.enabled) return 'var(--dsw-alias-text-tertiary, #9aa0a6)'
      if (n.light === 'dead') return 'var(--dsw-alias-danger, #c0392b)'
      if (n.light === 'active') return 'var(--dsw-alias-success, #2e7d32)'
      return 'var(--dsw-alias-warning, #b58900)'
    }

    function Row({ n, busy, onToggle }) {
      return h(
        'button',
        {
          type: 'button',
          title: n.why || '',
          disabled: busy,
          onClick: () => onToggle(n),
          style: {
            display: 'flex',
            alignItems: 'center',
            gap: '8px',
            width: '100%',
            padding: '6px 8px',
            border: '0',
            borderRadius: '6px',
            background: 'transparent',
            color: 'var(--dsw-alias-text-primary, inherit)',
            fontSize: '13px',
            textAlign: 'left',
            cursor: busy ? 'progress' : 'pointer',
            opacity: busy ? 0.6 : 1,
          },
        },
        h('span', {
          style: {
            width: '8px',
            height: '8px',
            borderRadius: '50%',
            flex: '0 0 auto',
            background: tone(n),
          },
        }),
        h('span', { style: { flex: '1 1 auto', overflow: 'hidden', textOverflow: 'ellipsis' } }, n.name),
        h('span', {
          style: {
            flex: '0 0 auto',
            fontSize: '11px',
            color: 'var(--dsw-alias-text-tertiary, #8a8f98)',
          },
        }, n.measured ? n.tokens.toLocaleString() : '—'),
      )
    }

    function Panel() {
      const [state, setState] = useState(null)
      const [open, setOpen] = useState(false)
      const [busy, setBusy] = useState('')
      const [err, setErr] = useState('')

      const load = useCallback(async () => {
        try {
          const r = await fetch('/capbus/state', { credentials: 'same-origin' })
          if (!r.ok) throw new Error('HTTP ' + r.status)
          setState(await r.json())
          setErr('')
        } catch (e) {
          setErr(String((e && e.message) || e))
        }
      }, [])

      useEffect(() => {
        load()
        const t = setInterval(load, 10000)
        return () => clearInterval(t)
      }, [load])

      const onToggle = async (n) => {
        setBusy(n.name)
        try {
          const r = await fetch('/capbus/toggle', {
            method: 'POST',
            credentials: 'same-origin',
            headers: { 'content-type': 'application/json', 'x-capbus': '1' },
            body: JSON.stringify({ name: n.name, on: !n.enabled }),
          })
          if (!r.ok) throw new Error('HTTP ' + r.status)
          setState(await r.json())
          setErr('')
        } catch (e) {
          setErr(String((e && e.message) || e))
        } finally {
          setBusy('')
        }
      }

      const nodes = (state && state.nodes) || []
      const head = state
        ? state.enabled + '/' + state.declared + ' 个能力 · ' + state.tokens.toLocaleString() + ' tok'
        : err || '…'

      return h(
        'div',
        { style: { display: 'flex', flexDirection: 'column', gap: '2px' } },
        h(
          'button',
          {
            type: 'button',
            onClick: () => setOpen(!open),
            'aria-expanded': open ? 'true' : 'false',
            style: {
              display: 'flex',
              alignItems: 'center',
              gap: '8px',
              width: '100%',
              padding: '6px 8px',
              border: '0',
              borderRadius: '6px',
              background: open ? 'var(--dsw-alias-bg-active, rgba(127,127,127,0.12))' : 'transparent',
              color: 'var(--dsw-alias-text-primary, inherit)',
              fontSize: '13px',
              textAlign: 'left',
              cursor: 'pointer',
            },
          },
          h('span', { style: { flex: '1 1 auto' } }, 'capbus'),
          h('span', {
            style: {
              flex: '0 0 auto',
              fontSize: '11px',
              color: 'var(--dsw-alias-text-tertiary, #8a8f98)',
            },
          }, head),
        ),
        open &&
          h(
            'div',
            { style: { display: 'flex', flexDirection: 'column', paddingLeft: '10px' } },
            nodes.length === 0 &&
              h('div', {
                style: { padding: '6px 8px', fontSize: '12px', color: 'var(--dsw-alias-text-tertiary, #8a8f98)' },
              }, err || '还没有声明任何能力'),
            nodes.map((n) => h(Row, { key: n.name, n, busy: busy === n.name, onToggle })),
            err &&
              h('div', {
                style: { padding: '6px 8px', fontSize: '12px', color: 'var(--dsw-alias-danger, #c0392b)' },
              }, err),
          ),
      )
    }

    return {
      inject: ['slots'],
      apply(ctx) {
        ctx.slots.inject('sidebar.panellist', () =>
          ctx.slots.register(
            { name: 'sidebar.panellist', id: 'capbus', order: 60 },
            Panel,
          ),
        )
      },
    }
  },
})
