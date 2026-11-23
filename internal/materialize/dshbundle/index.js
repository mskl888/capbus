// Host half of the capbus bundle.
//
// It answers two routes on the Harness origin and nothing else:
//
//   GET  /capbus/state            the same snapshot `capbus ui` renders
//   POST /capbus/toggle           { name, on } - one capability on or off
//
// Both run the `capbus` binary as a subprocess, so the panel's token never has
// to be shared: there is no second listener and nothing to authenticate
// between the two halves.
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'

const run = promisify(execFile)

export const inject = ['webServer']

function send(res, status, body) {
  const text = JSON.stringify(body)
  res.statusCode = status
  res.setHeader('content-type', 'application/json; charset=utf-8')
  res.setHeader('cache-control', 'no-store')
  res.end(text)
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    let raw = ''
    req.setEncoding('utf8')
    req.on('data', (chunk) => {
      raw += chunk
      if (raw.length > 64 * 1024) reject(new Error('body too large'))
    })
    req.on('end', () => resolve(raw))
    req.on('error', reject)
  })
}

export function apply(ctx, config) {
  const binary = (config && config.binary) || 'capbus'

  async function capbus(args) {
    const { stdout } = await run(binary, args, {
      timeout: 20000,
      windowsHide: true,
      maxBuffer: 8 * 1024 * 1024,
    })
    return JSON.parse(stdout)
  }

  ctx.effect(() =>
    ctx.webServer.register({
      kind: 'prefix',
      path: '/capbus',
      handler: async (req, res) => {
        const path = (req.url || '').split('?')[0]
        try {
          if (path === '/capbus/state') {
            if (req.method !== 'GET') return send(res, 405, { ok: false, error: 'GET only' })
            return send(res, 200, await capbus(['api', 'state']))
          }
          if (path === '/capbus/toggle') {
            if (req.method !== 'POST') return send(res, 405, { ok: false, error: 'POST only' })
            // A page on another origin cannot set this header without a CORS
            // preflight, which this route never answers, so it is the CSRF guard.
            if (req.headers['x-capbus'] !== '1') return send(res, 403, { ok: false, error: 'forbidden' })
            const body = JSON.parse((await readBody(req)) || '{}')
            if (typeof body.name !== 'string' || !body.name) {
              return send(res, 400, { ok: false, error: 'name is required' })
            }
            return send(res, 200, await capbus(['api', 'toggle', body.name, body.on ? 'on' : 'off']))
          }
          return send(res, 404, { ok: false, error: 'not found' })
        } catch (err) {
          return send(res, 500, { ok: false, error: String((err && err.message) || err) })
        }
      },
    }),
  )
}
