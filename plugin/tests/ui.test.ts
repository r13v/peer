import { expect, mock, test } from 'claude-code/testing'

const PANE = {
  title: 'peer',
  isFocused: false,
  bodyColumns: 70,
  placement: 'dock',
  scroll: { offset: 0, bodyRows: 30 },
  view: {},
} as never

const ROOM = {
  id: 'csv-export',
  repo: '/repo',
  started_at: '2026-10-05T20:00:00Z',
  members: [
    { role: 'main', agent: 'claude' },
    { role: 'reader', agent: 'codex', pid: 1 },
  ],
}

const LONG = Array.from({ length: 20 }, (_, i) => `line ${i + 1}`).join('\n')

const MESSAGES = [
  { id: 'a', at: '2026-10-05T20:01:00Z', from: 'peer', to: 'main', text: 'reader joined' },
  { id: 'b', at: '2026-10-05T20:01:01Z', from: 'peer', to: 'main', text: 'tester joined' },
  { id: 'c', at: '2026-10-05T20:02:00Z', from: 'reader', to: 'main', text: 'See `main.go:42` and docs/x.md' },
  { id: 'd', at: '2026-10-05T20:03:00Z', from: 'main', to: '*', text: LONG },
]

const lines = (...events: unknown[]) => events.map(event => `${JSON.stringify(event)}\n`).join('')

const WATCH = lines({ event: 'room', room: ROOM }, ...MESSAGES.map(message => ({ event: 'message', room: ROOM.id, message })), { event: 'ready' })

// start runs the plugin against a checkout that tracks main.go, in UTC+3,
// with watch printing what it is given and memberlog printing memberlog,
// and records the commands it runs and the panes it opens. The session
// joins each room that watch prints unless isOwn is false, so the rooms
// are its own.
async function start(
  $: Parameters<Parameters<typeof test>[1]>[0],
  on: Parameters<Parameters<typeof test>[1]>[1],
  watch: string | { stderr: string } = WATCH,
  memberlog = '',
  isOwn = true,
) {
  const ran: string[][] = []
  on('process.spawn', async function* (_, e, next) {
    if (e.argv[1] !== 'watch') return yield* next(e)
    ran.push([...e.argv])
    if (typeof watch !== 'string') {
      yield { stream: 'stderr', text: watch.stderr }
      return { code: 1, signal: null } as never
    }
    yield { stream: 'stdout', text: watch }
    return { code: 0, signal: null } as never
  })
  on('process.run', (_, e) => {
    ran.push([...e.argv])
    const stdout = e.argv[3] === 'ls-files' ? 'main.go\n' : e.argv[0] === 'date' ? '+0300\n' : e.argv[1] === 'memberlog' ? memberlog : ''
    return { value: { exitCode: 0, stdout, stderr: '' } } as never
  })
  on('session.start', (_, e) => ({ cwd: e.cwd }) as never)
  on('session.cwd', () => ({ value: '/repo' }) as never)
  // The pane is shown from its open to its close.
  let isShown = false
  on('ui.open', (_, e) => {
    ran.push(['ui.open', e.id])
    isShown = true
    return { value: { isPlaced: true } } as never
  })
  on('ui.close', (_, e) => {
    ran.push(['ui.close', e.id])
    isShown = false
    return { value: undefined } as never
  })
  on('ui.scroll', () => ({ value: {} }) as never)
  on('ui.panes', () => ({ value: isShown ? [{ id: 'peer', title: 'peer', isShown, isPlaced: true }] : [] }) as never)
  // A Bash call prints the room it names, as peer join and peer start do;
  // a join as main, a reserved role, fails and prints nothing.
  on('tool.call', { tool: 'Bash' }, (_, e) => {
    const command = (e as { command: string }).command
    const id = /peer \w+ ([a-z0-9-]+)/.exec(command)?.[1] ?? ROOM.id
    if (/^peer join \S+ main$/.test(command)) return { result: { stdout: '', stderr: 'peer: role "main" is reserved\n', interrupted: false } } as never
    return { result: { stdout: `${JSON.stringify({ ...ROOM, id })}\n`, stderr: '', interrupted: false } } as never
  })
  await $.session.start({ source: 'startup', cwd: '/repo' } as never)
  await new Promise(resolve => setTimeout(resolve, 50))
  if (isOwn && typeof watch === 'string') {
    const ids = new Set([...watch.matchAll(/"event":"room","room":\{"id":"([^"]+)"/g)].map(match => match[1]))
    for (const id of ids) await $.tool.call({ tool: 'Bash', command: `peer join ${id} reader` } as never)
  }
  return ran
}

test('the pane draws an empty checkout', async ($, on) => {
  await start($, on, lines({ event: 'ready' }))
  const ui = await $.ui.mount({ plugin: 'peer', surface: 'desktop', component: 'Pane', requestId: 'peer', props: PANE })
  expect(JSON.stringify(await ui.drawn())).toContain('No active peer room')
})

test('messages are cards with the time first, notices one line, file refs links', async ($, on) => {
  await start($, on)
  // The terminal draws the same room, without what only the desktop has.
  const terminal = await $.ui.mount({ plugin: 'peer', surface: 'terminal', component: 'Pane', requestId: 'peer', props: PANE })
  expect(JSON.stringify(await terminal.drawn())).toContain('main.go:42')
  const ui = await $.ui.mount({ plugin: 'peer', surface: 'desktop', component: 'Pane', requestId: 'peer', props: PANE })
  const drawn = JSON.stringify(await ui.drawn())
  // A reader's card: its purple mixed into the background.
  expect(drawn).toContain('"borderStyle":"single","borderColor":"#493d5f"')
  // Times are in the host's zone, UTC+3 here, newest first, under the tabs.
  expect(drawn.indexOf('chat 4')).toBeLessThan(drawn.indexOf('23:03'))
  expect(drawn.indexOf('23:03')).toBeLessThan(drawn.indexOf('23:02'))
  expect(drawn).toContain('reader joined · tester joined')
  expect(drawn).toContain('[`main.go:42`](file:///repo/main.go)')
  // docs/x.md is not tracked, so it stays text.
  expect(drawn).not.toContain('file:///repo/docs/x.md')
})

test('a long message shows its start until it is expanded', async ($, on) => {
  await start($, on)
  const ui = await $.ui.mount({ plugin: 'peer', surface: 'desktop', component: 'Pane', requestId: 'peer', props: PANE })
  let drawn = JSON.stringify(await ui.drawn())
  expect(drawn).toContain('show all 20 lines')
  expect(drawn).not.toContain('line 20')
  await ui.press({ key: 'more-d' })
  drawn = JSON.stringify(await ui.drawn())
  expect(drawn).toContain('line 20')
  expect(drawn).toContain('show less')
})

test('the footer counts messages the pane has not shown and toggles the pane', async ($, on) => {
  const ran = await start($, on)
  for (const surface of ['terminal', 'desktop'] as const) {
    ran.length = 0
    const ui = await $.ui.mount({ plugin: 'peer', surface, component: 'SessionMode', props: { modes: ['focus'] } })
    const drawn = JSON.stringify(await ui.drawn())
    expect(drawn).toContain('👀 4')
    expect(drawn).toContain('focus')
    await ui.press({ key: 'peer-open' })
    expect(ran).toContainEqual(['ui.open', 'peer'])
    await ui.press({ key: 'peer-open' })
    expect(ran).toContainEqual(['ui.close', 'peer'])
  }
})

test('with two rooms, each footer button opens the pane on its room', async ($, on) => {
  const other = { ...ROOM, id: 'auth-fix', started_at: '2026-10-05T19:00:00Z' }
  await start($, on, lines({ event: 'room', room: ROOM }, { event: 'room', room: other }, { event: 'ready' }))
  const footer = await $.ui.mount({ plugin: 'peer', surface: 'desktop', component: 'SessionMode', props: { modes: [] } })
  expect(JSON.stringify(await footer.drawn())).toContain('"label":"2"')
  // csv-export is newer, so auth-fix is the second room.
  await footer.press({ key: 'peer-open-2' })
  const pane = await $.ui.mount({ plugin: 'peer', surface: 'desktop', component: 'Pane', requestId: 'peer', props: PANE })
  expect(JSON.stringify(await pane.drawn())).toContain('auth-fix')
})

test('a member log with an entry over the desktop text limit still draws', async ($, on) => {
  await start($, on, WATCH, lines({ at: '2026-10-05T20:04:00Z', kind: 'result', text: 'x'.repeat(20_000) }))
  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await $.ui.mount({ plugin: 'peer', surface, component: 'Pane', requestId: 'peer', props: PANE })
    await ui.press({ key: 'tab-reader' })
    const drawn = JSON.stringify(await ui.drawn())
    expect(drawn).toContain('xxxx')
    expect(drawn.length).toBeLessThan(15_000)
  }
})

test('end room acts on a second press within three seconds', async ($, on) => {
  const clock = mock.clock(on)
  const ran = await start($, on)
  const ui = await $.ui.mount({ plugin: 'peer', surface: 'desktop', component: 'Pane', requestId: 'peer', props: PANE })
  await ui.press({ key: 'end' })
  expect(JSON.stringify(await ui.drawn())).toContain('"label":"confirm"')
  await clock.advance(3000)
  expect(JSON.stringify(await ui.drawn())).toContain('⏹ end')
  expect(ran.some(argv => argv[1] === 'end')).toBe(false)
  await ui.press({ key: 'end' })
  await ui.press({ key: 'end' })
  expect(ran.find(argv => argv[1] === 'end')).toEqual(['peer', 'end', 'csv-export', '--as', 'user'])
})

test('an ended room shows every member stopped', async ($, on) => {
  // The room this session started stays in the pane once it has ended.
  on('prompt.submit', (_, e) => ({ text: e.text }) as never)
  await start($, on, lines({ event: 'room', room: { ...ROOM, ended_at: '2026-10-05T21:00:00Z' } }, { event: 'ready' }))
  await $.tool.call({ tool: 'Bash', command: 'peer start csv-export --agent claude' } as never)
  const ui = await $.ui.mount({ plugin: 'peer', surface: 'desktop', component: 'Pane', requestId: 'peer', props: PANE })
  const drawn = JSON.stringify(await ui.drawn())
  expect(drawn).toContain('○ reader')
  expect(drawn).not.toContain('● reader')
})

test("the plugin's own rows are hidden; others and the terminal's ctrl+o draw as the engine does", async ($, on) => {
  // Beneath the plugin, the engine's own drawing of a row.
  on('ui.render', { component: 'UserMessage' }, () => ({ type: 'Text', props: {}, children: ['engine'] }) as never)
  await start($, on)
  const row = (text: string, origin: unknown, isExpanded = false) =>
    $.ui.mount({ plugin: 'peer', surface: 'desktop', component: 'UserMessage', props: { text, origin, isExpanded } as never })
  const delivery =
    'peer room csv-export: 2 new messages for main, delivered by the peer plugin; you do not run peer wait for this room.\n\n' +
    '[reader → main · 23:02:00]\nSee `main.go:42`'
  const plugin = { kind: 'plugin', name: 'peer' }
  // A submitted prompt, also inside the engine's framing, and an end note.
  for (const [text, origin] of [
    [delivery, plugin],
    [`The peer plugin sent a message:\n${delivery}\n\nThis is how Claude Code surfaces a prompt`, plugin],
    ['peer room csv-export has ended. Its members no longer read or answer messages.', plugin],
  ] as const) {
    expect(JSON.stringify(await (await row(text, origin)).drawn())).not.toContain('engine')
  }
  // The desktop has no ctrl+o and marks every row expanded: it still hides.
  expect(JSON.stringify(await (await row(delivery, plugin, true)).drawn())).not.toContain('engine')
  const terminal = await $.ui.mount({
    plugin: 'peer',
    surface: 'terminal',
    component: 'UserMessage',
    props: { text: delivery, origin: plugin, isExpanded: true } as never,
  })
  expect(JSON.stringify(await terminal.drawn())).toContain('engine')
  expect(JSON.stringify(await (await row('peer room is a nice idea', { kind: 'composer' })).drawn())).toContain('engine')
})

test("another session's room of the checkout is not shown", async ($, on) => {
  // Beneath the plugin, the engine's own footer.
  on('ui.render', { component: 'SessionMode' }, () => ({ type: 'Text', props: {}, children: ['engine'] }) as never)
  const ran = await start($, on, WATCH, '', false)
  expect(ran.some(argv => argv[0] === 'ui.open')).toBe(false)
  const ui = await $.ui.mount({ plugin: 'peer', surface: 'desktop', component: 'Pane', requestId: 'peer', props: PANE })
  const drawn = JSON.stringify(await ui.drawn())
  expect(drawn).toContain('No active peer room')
  expect(drawn).not.toContain('reader joined')
  const footer = await $.ui.mount({ plugin: 'peer', surface: 'desktop', component: 'SessionMode', props: { modes: [] } })
  expect(JSON.stringify(await footer.drawn())).toBe(JSON.stringify({ type: 'Text', props: {}, children: ['engine'] }))
  // A failed join adopts nothing: alone, or inside a longer command line
  // that prints the room with main in it.
  await $.tool.call({ tool: 'Bash', command: 'peer join csv-export main' } as never)
  await $.tool.call({ tool: 'Bash', command: 'peer join csv-export main; peer status csv-export' } as never)
  expect(JSON.stringify(await footer.drawn())).not.toContain('👀')
})

test('outside a Git checkout, a failing watch writes nothing to the chat', async ($, on) => {
  const clock = mock.clock(on)
  const logged: string[] = []
  on('ui.log', (_, e) => {
    logged.push(e.text)
    return { value: undefined } as never
  })
  const ran = await start($, on, { stderr: 'peer: run this command inside the shared Git checkout\n' })
  for (let i = 0; i < 12; i++) {
    await clock.advance(10_000)
    await new Promise(resolve => setTimeout(resolve, 5))
  }
  const tries = ran.filter(argv => argv[1] === 'watch').length
  expect(logged).toEqual([])
  // It tries again, less and less often: a cd into a checkout is picked up.
  expect(tries).toBeGreaterThan(1)
  expect(tries).toBeLessThan(10)
})

test('a state from before own counts the rooms it led as its own', async ($, on) => {
  // The session's state as an older module wrote it, until the first write.
  const old = { selected: null, tab: 'chat', led: ['csv-export'], rooms: {}, messages: {} }
  on('state.get', async (_, e, next) => {
    const read = (await next(e)) as { value: { value: unknown; version: number } }
    return (read.value.value === undefined ? { value: { ...read.value, value: old } } : read) as never
  })
  const ran = await start($, on, WATCH, '', false)
  const ui = await $.ui.mount({ plugin: 'peer', surface: 'desktop', component: 'Pane', requestId: 'peer', props: PANE })
  expect(ran.some(argv => argv[0] === 'ui.open')).toBe(true)
  expect(JSON.stringify(await ui.drawn())).toContain('reader joined')
})

test('after a rewind the rooms its conversation started are its own and led again', async ($, on) => {
  // A rewind starts the session with an empty state; its chat holds the start.
  const use = { tool_use_id: 't1', tool: 'Bash', input: { command: 'peer start csv-export --agent claude' }, result: { stdout: `${JSON.stringify(ROOM)}\n` } }
  on('session.messages', () => ({ value: [{ role: 'assistant', text: '', toolUses: [use] }] }) as never)
  const ran = await start($, on, WATCH, '', false)
  expect(ran.some(argv => argv[0] === 'ui.open')).toBe(true)
  // Leading it reads main's unread count, so what main read is not delivered again.
  expect(ran.some(argv => argv[1] === 'status' && argv[2] === ROOM.id)).toBe(true)
})
