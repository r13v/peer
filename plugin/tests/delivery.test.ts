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

type Tools = { submits: string[]; acks: string[]; failAcks: number; release: () => void }

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
  const tools: Tools = { submits: [], acks: [], failAcks: 0, release: () => {} }
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
    // ack moves main's cursor just past the --upto message.
    if (e.argv[1] === 'ack') {
      tools.acks.push(e.argv[e.argv.indexOf('--upto') + 1] ?? '')
      if (tools.failAcks > 0) {
        tools.failAcks -= 1
        return { value: { exitCode: 1, stdout: '', stderr: 'peer: no message' } } as never
      }
    }
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
  // The first batch is in the chat, so the cursor moves past it.
  expect(tools.acks).toEqual(['m16'])
  await clock.advance(3000)
  await new Promise(resolve => setTimeout(resolve, 50))
  expect(tools.submits).toHaveLength(3)
  expect(delivered(tools.submits[2] ?? '')).toEqual([17, 18, 19, 20])
  expect(tools.acks.at(-1)).toBe('m20')
})

test('a message that comes while a batch waits for its turn follows it, and the cursor never passes it', async ($, on) => {
  let acksAtSecond = -1
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
      } else acksAtSecond = held.acks.length
      return { text }
    },
  )
  await new Promise(resolve => setTimeout(resolve, 100))
  expect(tools.submits.map(delivered)).toEqual([[1], [2]])
  // The second message waits behind the first turn, not behind its ack.
  expect(acksAtSecond).toBeLessThanOrEqual(1)
  expect(tools.acks.at(-1)).toBe('m2')
})

test('a failed ack is tried again, and the batch is not delivered twice', async ($, on) => {
  const clock = mock.clock(on)
  const tools = await lead($, on, [{ event: 'message', room: ROOM.id, message: message(1) }], [], (text, n, held) => {
    if (n === 1) held.failAcks = 1
    return { text }
  })
  expect(tools.acks).toEqual(['m1'])
  await clock.advance(3000)
  await new Promise(resolve => setTimeout(resolve, 50))
  expect(tools.acks).toEqual(['m1', 'm1'])
  expect(tools.submits).toHaveLength(1)
})
