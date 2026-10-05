import { expect, mock, test } from 'claude-code/testing'

const ROOM = {
  id: 'csv-export',
  repo: '/repo',
  started_at: '2026-10-05T20:00:00Z',
  members: [{ role: 'main', agent: 'claude' }],
}

const message = (i: number) => ({
  id: `m${i}`,
  at: `2026-10-05T20:${String(i).padStart(2, '0')}:00Z`,
  from: 'reader',
  to: 'main',
  text: `finding ${i}`,
})

const lines = (...events: unknown[]) => events.map(event => `${JSON.stringify(event)}\n`).join('')

type Tools = { submits: string[]; waits: number; release: () => void }

// lead starts the plugin, gives it a watch that prints the room and the
// messages, then holds until released and prints more, and makes this
// session main of the room through a peer start it sees.
async function lead(
  $: Parameters<Parameters<typeof test>[1]>[0],
  on: Parameters<Parameters<typeof test>[1]>[1],
  first: unknown[],
  later: unknown[],
  answer: (text: string, n: number, tools: Tools) => unknown,
) {
  const tools: Tools = { submits: [], waits: 0, release: () => {} }
  const gate = new Promise<void>(resolve => (tools.release = resolve))
  on('process.spawn', async function* (_, e, next) {
    if (e.argv[1] !== 'watch') return yield* next(e)
    yield { stream: 'stdout', text: lines({ event: 'room', room: ROOM }, ...first, { event: 'ready' }) }
    await gate
    if (later.length) yield { stream: 'stdout', text: lines(...later) }
    await new Promise(() => {})
    return { code: 0, signal: null } as never
  })
  on('process.run', (_, e) => {
    if (e.argv[1] === 'wait') tools.waits += 1
    return { value: { exitCode: 0, stdout: '', stderr: '' } } as never
  })
  on('prompt.submit', async (_, e) => {
    tools.submits.push(e.text)
    return answer(e.text, tools.submits.length, tools) as never
  })
  on('tool.call', { tool: 'Bash' }, () => ({ result: { stdout: `${JSON.stringify(ROOM)}\n`, stderr: '', interrupted: false } }) as never)
  on('session.start', (_, e) => ({ cwd: e.cwd }) as never)
  on('session.cwd', () => ({ value: '/repo' }) as never)
  on('ui.open', () => ({ value: { isPlaced: true } }) as never)
  on('ui.scroll', () => ({ value: {} }) as never)
  on('ui.panes', () => ({ value: [] }) as never)
  await $.session.start({ source: 'startup', cwd: '/repo' } as never)
  await new Promise(resolve => setTimeout(resolve, 50))
  await $.tool.call({ tool: 'Bash', command: 'peer start csv-export --agent claude' } as never)
  await new Promise(resolve => setTimeout(resolve, 50))
  return tools
}

const delivered = (text: string) => [...text.matchAll(/finding (\d+)/g)].map(match => Number(match[1]))

test('a refused batch is tried again alone; the cursor waits for it', async ($, on) => {
  const clock = mock.clock(on)
  const all = Array.from({ length: 20 }, (_, i) => ({ event: 'message', room: ROOM.id, message: message(i + 1) }))
  // The second batch is dropped once.
  const tools = await lead($, on, all, [], (text, n) => (n === 2 ? { drop: 'busy' } : { text }))
  expect(delivered(tools.submits[0] ?? '')).toEqual(Array.from({ length: 16 }, (_, i) => i + 1))
  expect(delivered(tools.submits[1] ?? '')).toEqual([17, 18, 19, 20])
  expect(tools.waits).toBe(0)
  await clock.advance(3000)
  await new Promise(resolve => setTimeout(resolve, 50))
  expect(tools.submits).toHaveLength(3)
  expect(delivered(tools.submits[2] ?? '')).toEqual([17, 18, 19, 20])
  expect(tools.waits).toBeGreaterThan(0)
})

test('a message that comes while a batch waits for its turn follows it, before the cursor moves', async ($, on) => {
  let waitsAtSecond = -1
  const tools = await lead(
    $,
    on,
    [{ event: 'message', room: ROOM.id, message: message(1) }],
    [{ event: 'message', room: ROOM.id, message: message(2) }],
    async (text, n, held) => {
      if (n === 1) {
        // While the first turn waits, the watch prints another message.
        held.release()
        await new Promise(resolve => setTimeout(resolve, 30))
      } else waitsAtSecond = held.waits
      return { text }
    },
  )
  await new Promise(resolve => setTimeout(resolve, 100))
  expect(tools.submits.map(delivered)).toEqual([[1], [2]])
  expect(waitsAtSecond).toBe(0)
  expect(tools.waits).toBeGreaterThan(0)
})
