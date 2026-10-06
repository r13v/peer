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
// and records the commands it runs and the panes it opens.
async function start(
  $: Parameters<Parameters<typeof test>[1]>[0],
  on: Parameters<Parameters<typeof test>[1]>[1],
  watch = WATCH,
  memberlog = '',
) {
  const ran: string[][] = []
  on('process.spawn', async function* (_, e, next) {
    if (e.argv[1] !== 'watch') return yield* next(e)
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
  on('ui.open', (_, e) => {
    ran.push(['ui.open', e.id])
    return { value: { isPlaced: true } } as never
  })
  on('ui.scroll', () => ({ value: {} }) as never)
  on('ui.panes', () => ({ value: [] }) as never)
  await $.session.start({ source: 'startup', cwd: '/repo' } as never)
  await new Promise(resolve => setTimeout(resolve, 50))
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

test('the footer counts messages the pane has not shown and opens the pane', async ($, on) => {
  const ran = await start($, on)
  for (const surface of ['terminal', 'desktop'] as const) {
    ran.length = 0
    const ui = await $.ui.mount({ plugin: 'peer', surface, component: 'SessionMode', props: { modes: ['focus'] } })
    const drawn = JSON.stringify(await ui.drawn())
    expect(drawn).toContain('👥 4')
    expect(drawn).toContain('focus')
    await ui.press({ key: 'peer-open' })
    expect(ran).toContainEqual(['ui.open', 'peer'])
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
  on('tool.call', { tool: 'Bash' }, () => ({ result: { stdout: `${JSON.stringify(ROOM)}\n`, stderr: '', interrupted: false } }) as never)
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
