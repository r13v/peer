import type { EngineInterface as Engine, Register } from 'claude-code'

import type { PeerLogEntry, PeerMark, PeerState, PeerMember, PeerMessage, PeerRoom } from '../types'

const PANE = 'peer'
const EVERYONE = '*'
// ICON stands for peer in the prompt footer.
const ICON = '👥'

// All of the plugin's session state is one value: the engine lists the
// values a module reads and writes, so each reference is a literal.
const STATE = { plugin: 'peer', key: 'state' } as const

const INITIAL: PeerState = {
  rooms: {},
  messages: {},
  selected: null,
  tab: 'chat',
  confirm: null,
  led: [],
  watchError: null,
  log: [],
  // delivered marks, per led room, the last message for main that is in
  // the chat, so a reload goes on from it.
  delivered: {},
  // notified lists the led rooms whose end main has been told about.
  notified: [],
  expanded: [],
  seen: {},
}

const rooms = 'rooms'
const messages = 'messages'
const selected = 'selected'
const tab = 'tab'
const confirm = 'confirm'
const led = 'led'
const watchError = 'watchError'
const log = 'log'
const delivered = 'delivered'
const notified = 'notified'
const expanded = 'expanded'
const seen = 'seen'

const HANDOFF =
  'The peer plugin reads this room for main and puts its messages into this chat as a turn of their own, once this session is free. Do not run peer wait for this room; go on with the task.'

// Module variables start over on a reload; everything drawn is in $.state.
let isReady = false
let isStarted = false
let isPumping = false
let isPumpWanted = false
// Whether the person holds the pane's keyboard: then a new message does not
// scroll it, so what they read stays put.
let isPaneFocused = false
// The host's offset from UTC in minutes: the module's environment does not
// know the host's time zone, so times are shifted by hand.
let utcOffset = 0
// The checkout's files, so a message's file:line becomes a link.
let files = { repo: '', paths: new Set<string>() }

// get reads a field of the plugin's state; while a drawing reads it, a
// later put draws that drawing again.
async function get<K extends keyof PeerState>($: Engine, field: K): Promise<PeerState[K]> {
  const { value } = await $.state.get(STATE)
  return (value ?? INITIAL)[field]
}

// snapshot reads the whole state at once: a drawing reads it once, not
// once per field.
async function snapshot($: Engine): Promise<PeerState> {
  const { value } = await $.state.get(STATE)
  return { ...INITIAL, ...value }
}

// put applies fn to a field and writes the state, again when another write
// came first. A write that changes nothing is skipped, since each write
// draws every drawing that reads the state again.
async function put<K extends keyof PeerState>($: Engine, field: K, fn: (old: PeerState[K]) => PeerState[K]) {
  for (;;) {
    const { value, version } = await $.state.get(STATE)
    const old = value ?? INITIAL
    const next = fn(old[field])
    if (next === old[field]) return
    const written = await $.state.set(STATE, { ...old, [field]: next }, { ifVersion: version })
    if (written.isSet) return
  }
}

// begin starts the session's background work once; session.start calls it,
// and so do the first hooks of a mod loaded into a running session.
async function begin($: Engine) {
  if (isStarted) return
  isStarted = true
  // A pane left open by the module before a reload is drawn by this one.
  $.ui.invalidate('ui.render')
  watch($)
  $.clock.every(3000, () => void refresh($))
  void loadOffset($)
  $.clock.every(60_000, () => void loadFiles($))
  void loadFiles($)
}

// loadOffset reads the host's UTC offset, as +0300, and draws the times again.
async function loadOffset($: Engine) {
  try {
    const { stdout } = await $.process.run(['date', '+%z'], { timeoutMs: 5000 })
    const match = /^([+-])(\d\d)(\d\d)$/.exec(stdout.trim())
    if (!match) return
    utcOffset = (match[1] === '-' ? -1 : 1) * (Number(match[2]) * 60 + Number(match[3]))
    $.ui.invalidate('ui.render')
  } catch {
    // times stay in UTC
  }
}

// loadFiles lists the checkout's tracked files.
async function loadFiles($: Engine) {
  try {
    const repo = current(await snapshot($))?.repo ?? (await $.session.cwd())
    const { exitCode, stdout } = await $.process.run(['git', '-C', repo, 'ls-files'], { timeoutMs: 10_000 })
    if (exitCode === 0) files = { repo, paths: new Set(stdout.split('\n').filter(Boolean)) }
  } catch {
    // no links until the next try
  }
}

// toNewest scrolls the pane to its top, where the newest message is.
function toNewest($: Engine) {
  void $.ui.scroll({ in: PANE, to: 'start' }).catch(() => ({}))
}

function peer($: Engine, argv: string[], timeoutMs = 30_000) {
  return $.process.run(['peer', ...argv], { timeoutMs })
}

// activeRooms lists the active rooms, the ones this session leads first,
// then the newest first.
function activeRooms(st: PeerState) {
  const isLed = (room: PeerRoom) => (st.led.includes(room.id) ? 0 : 1)
  return Object.values(st.rooms)
    .filter(room => !room.ended_at)
    .sort((a, b) => isLed(a) - isLed(b) || b.started_at.localeCompare(a.started_at))
}

// current is the room the pane shows: the one picked, else the first active.
function current(st: PeerState) {
  const picked = st.selected ? st.rooms[st.selected] : undefined
  return picked ?? activeRooms(st)[0] ?? null
}

async function openPane($: Engine) {
  const opened = await $.ui.open({ id: PANE, title: 'peer' }).catch(() => ({ isPlaced: false }))
  if (opened.isPlaced) toNewest($)
  return opened
}

// watch keeps rooms and messages current from peer watch, which reads
// the store without moving anyone's cursor.
function watch($: Engine) {
  void (async () => {
    let buffer = ''
    let stderr = ''
    try {
      for await (const { stream, text } of $.process.spawn({ argv: ['peer', 'watch'] })) {
        if (stream === 'stderr') {
          stderr += text
          continue
        }
        buffer += text
        const lines = buffer.split('\n')
        buffer = lines.pop() ?? ''
        await apply($, lines)
      }
    } catch (err) {
      stderr += String(err)
    }
    $.ui.log(`peer watch stopped: ${stderr.trim().slice(0, 300) || 'no output'}`, { to: 'transcript' })
    await put($, watchError, () =>
      /usage|unknown|not found|no such file/i.test(stderr)
        ? 'peer watch failed: update the peer CLI (peer update or brew upgrade --cask peer)'
        : `peer watch stopped${stderr ? `: ${stderr.trim().slice(0, 200)}` : ''}; retrying`,
    )
    $.clock.after(5000, () => watch($))
  })()
}

async function apply($: Engine, lines: string[]) {
  const roomEvents: PeerRoom[] = []
  const newMessages: Record<string, PeerMessage[]> = {}
  let ready = false
  for (const line of lines) {
    if (!line.trim()) continue
    let event: { event?: string; room?: unknown; message?: PeerMessage }
    try {
      event = JSON.parse(line)
    } catch {
      continue
    }
    if (event.event === 'room' && event.room && typeof event.room === 'object') {
      roomEvents.push(event.room as PeerRoom)
    } else if (event.event === 'message' && typeof event.room === 'string' && event.message) {
      ;(newMessages[event.room] ??= []).push(event.message)
    } else if (event.event === 'ready') {
      ready = true
    }
  }
  const before = await get($, rooms)
  const known = await get($, messages)
  // A restarted watch replays the transcript: keep each message once.
  const fresh: Record<string, PeerMessage[]> = {}
  for (const [id, list] of Object.entries(newMessages)) {
    const ids = new Set((known[id] ?? []).map(m => m.id))
    const added = list.filter(m => !ids.has(m.id))
    if (added.length > 0) fresh[id] = added
  }
  if (roomEvents.length > 0) {
    await put($, rooms, all => {
      const next = { ...all }
      for (const room of roomEvents) next[room.id] = room
      return next
    })
  }
  if (Object.keys(fresh).length > 0) {
    await put($, messages, all => {
      const next = { ...all }
      for (const [id, list] of Object.entries(fresh)) next[id] = [...(next[id] ?? []), ...list]
      return next
    })
  }
  await put($, watchError, old => (old === null ? old : null))
  if (isReady) {
    for (const room of roomEvents) {
      const was = before[room.id]
      if (room.ended_at && was && !was.ended_at) $.ui.toast(`peer: room ${room.id} ended`)
    }
    for (const [id, list] of Object.entries(fresh)) {
      for (const m of list) {
        if (m.from === 'peer') $.ui.toast(`${id}: ${m.text}`)
      }
    }
    const st = await snapshot($)
    if (!isPaneFocused && st.tab === 'chat' && fresh[current(st)?.id ?? '']) toNewest($)
  }
  if (ready) {
    // A watch started after a room ended does not list it: read the rest
    // of a led room's transcript, then its state, from log and status.
    const listed = new Set(roomEvents.map(room => room.id))
    const isLed = new Set(await get($, led))
    for (const room of Object.values(await get($, rooms))) {
      if (room.ended_at || listed.has(room.id)) continue
      if (isLed.has(room.id) && !(await readLog($, room.id))) continue
      const { exitCode, stdout } = await peer($, ['status', room.id])
      if (exitCode === 0 && stdout.trim()) {
        const status = JSON.parse(stdout) as PeerRoom
        await put($, rooms, all => ({ ...all, [status.id]: status }))
      }
    }
    if (!isReady) {
      isReady = true
      if (activeRooms(await snapshot($)).length > 0) void openPane($)
    }
  }
  void pump($)
}

// readLog adds a room's whole transcript from peer log to the messages,
// for what a watch may not have printed yet; it says whether it could.
async function readLog($: Engine, id: string) {
  const { exitCode, stdout, isStdoutTruncated } = await peer($, ['log', id, '--json'])
  if (exitCode !== 0) return false
  if (isStdoutTruncated) {
    await put($, watchError, () => `the transcript of ${id} is over 4 MiB; read its end with peer log ${id}`)
    return false
  }
  const list = stdout.split('\n').filter(line => line.trim()).map(line => JSON.parse(line) as PeerMessage)
  await put($, messages, all => {
    const ids = new Set((all[id] ?? []).map(m => m.id))
    return { ...all, [id]: [...(all[id] ?? []), ...list.filter(m => !ids.has(m.id))] }
  })
  return true
}

// lead makes this session main's reader for room id. Messages come from
// peer watch's transcript, not from wait, so a reload or a failed delivery
// loses none; main's cursor moves only after a delivery. A reload between
// a queued turn and its mark brings that batch again: at least once, not
// exactly once.
async function lead($: Engine, id: string, isNew = false) {
  if ((await get($, led)).includes(id)) return
  if (!(id in (await get($, delivered)))) {
    // From a new room, deliver everything; otherwise start after what main
    // already read with wait, which status counts as not unread.
    let mark: PeerMark | null = null
    if (!isNew) {
      const { stdout } = await peer($, ['status', id])
      const unread = (JSON.parse(stdout || '{}') as PeerRoom).members?.find(m => m.role === 'main')?.unread ?? 0
      const list = forMain((await get($, messages))[id] ?? [])
      const last = list[list.length - unread - 1]
      mark = last ? { id: last.id, at: last.at } : null
    }
    await put($, delivered, all => ({ ...all, [id]: mark }))
  }
  await put($, led, list => [...list, id])
  void pump($)
}

function forMain(list: PeerMessage[]) {
  return list.filter(m => m.from !== 'main' && (m.to === 'main' || m.to === EVERYONE))
}

// after returns the messages of list that come after mark.
function after(list: PeerMessage[], mark: PeerMark | null) {
  if (!mark) return list
  const i = list.findIndex(m => m.id === mark.id)
  if (i >= 0) return list.slice(i + 1)
  const at = Date.parse(mark.at)
  return list.filter(m => Date.parse(m.at) > at)
}

// pump puts what is new for main in each led room into the chat, in order;
// a mark moves only once its batch is there, and a failure is tried again.
async function pump($: Engine) {
  if (!isReady) return
  // A call made while a pump runs is not dropped: that pump runs once more.
  if (isPumping) {
    isPumpWanted = true
    return
  }
  isPumping = true
  isPumpWanted = false
  try {
    for (const id of await get($, led)) {
      // watch can print a room's end before its last messages: read the
      // whole transcript once the room has ended, before the end note.
      const hasEnded = Boolean((await get($, rooms))[id]?.ended_at)
      if (hasEnded && !(await readLog($, id))) throw new Error('no log')
      for (;;) {
        const pending = after(forMain((await get($, messages))[id] ?? []), (await get($, delivered))[id] ?? null)
        const batch = pending.slice(0, 16)
        const last = batch[batch.length - 1]
        if (!last) break
        if (!(await deliver($, id, batch))) throw new Error('not delivered')
        await put($, delivered, all => ({ ...all, [id]: { id: last.id, at: last.at } }))
        await ack($, id, last)
      }
      // Ack the saved mark too: an ack that failed before a reload is
      // tried again here.
      await ack($, id, (await get($, delivered))[id] ?? null)
      const room = (await get($, rooms))[id]
      if (hasEnded && room) {
        if (!(await get($, notified)).includes(id)) {
          const reason = room.ended_reason ? ` (${room.ended_reason})` : ''
          const note = `peer room ${id} has ended${reason}. Its members no longer read or answer messages.`
          if (!(await deliver($, id, [], note))) throw new Error('not delivered')
          await put($, notified, list => [...list, id])
        }
        await put($, led, list => list.filter(one => one !== id))
      }
    }
  } catch {
    $.clock.after(3000, () => void pump($))
  } finally {
    isPumping = false
  }
  if (isPumpWanted) void pump($)
}

// ack moves main's cursor just past mark, the last message in the chat, so
// unread counts stay right and a wait run after the plugin is turned off
// misses nothing. A failed ack throws, and the pump tries again later.
async function ack($: Engine, id: string, mark: PeerMark | null) {
  if (!mark) return
  const { exitCode } = await peer($, ['ack', id, '--as', 'main', '--upto', mark.id])
  if (exitCode !== 0) throw new Error('not acked')
}

// deliver queues one batch, or a note, as a turn of its own: the session
// runs it once it is idle, so no message lands in a turn that is ending.
// It says whether the turn was queued.
async function deliver($: Engine, id: string, list: PeerMessage[], note?: string) {
  const body = list
    .map(m => `[${m.from} → ${m.to === EVERYONE ? 'all' : m.to} · ${clock(m.at, true)}]\n${m.text}`)
    .join('\n\n')
  const text =
    note ??
    `peer room ${id}: ${list.length} new message${list.length === 1 ? '' : 's'} for main, delivered by the peer plugin; you do not run peer wait for this room.\n\n${body}`
  const submitted = await $.prompt.submit({ text })
  return !('drop' in submitted && submitted.drop)
}

// refresh keeps the log tab current while the pane shows it.
async function refresh($: Engine) {
  const pane = (await $.ui.panes()).find(one => one.id === PANE && one.isPlaced)
  if (!pane) return
  const st = await snapshot($)
  const room = current(st)
  if (!room) return
  // What the pane shows counts as read for the footer's count.
  const count = (st.messages[room.id] ?? []).length
  if (pane.isShown && st.seen[room.id] !== count) await put($, seen, all => ({ ...all, [room.id]: count }))
  if ((await get($, tab)) !== 'chat') await loadLog($, room)
}

// loadLog reads the log of the member whose tab is shown.
async function loadLog($: Engine, room: PeerRoom) {
  const role = await get($, tab)
  if (role === 'chat') return
  const { exitCode, stdout } = await peer($, ['memberlog', room.id, role])
  if (exitCode !== 0) return
  const entries: PeerLogEntry[] = []
  for (const line of stdout.split('\n')) {
    try {
      if (line.trim()) entries.push(JSON.parse(line))
    } catch {
      // skip a line that is not JSON
    }
  }
  await put($, log, () => entries.slice(-200))
}

const state = (m: PeerMember) =>
  m.kicked ? 'kicked' : m.exited ? 'exited' : m.pid ? 'running' : m.role === 'main' ? 'leads' : 'joined'


export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    const started = await next(e)
    await begin($)
    return started
  })

  on('turn.start', async ($, e, next) => {
    await begin($)
    return next(e)
  })

  on('tool.call', { tool: 'Bash' }, async ($, e, next) => {
    await begin($)
    const all = shellCommands(e.command)
    const commands = all.map(peerArgs).filter((args): args is string[] => args !== null)
    const wait = commands.find(args => args[0] === 'wait' && asMain(args))
    if (wait?.[1] && all.length > 1 && (await get($, led)).includes(wait[1])) {
      // Answering the whole line would skip its other commands.
      return { deny: `Run the other commands without peer wait. ${HANDOFF}` }
    }
    if (wait?.[1] && all.length === 1) {
      const id = wait[1]
      const room = (await get($, rooms))[id]
      // led first: rooms comes from watch and can lag behind a start.
      if ((await get($, led)).includes(id) || (room && !room.ended_at)) {
        await lead($, id)
        return { result: { stdout: JSON.stringify({ status: 'timeout', note: HANDOFF }), stderr: '', interrupted: false } }
      }
      return next(e)
    }
    const startedAt = new Date(Date.now() - 1000).toISOString()
    const ran = await next(e)
    if (ran.deny !== undefined || ran.isError) return ran
    // Main ended the room itself, so it needs no note that it ended.
    for (const args of commands) {
      if (args[0] === 'end' && args[1] && asMain(args)) await put($, notified, list => [...list, args[1] ?? ''])
    }
    const start = commands.find(args => args[0] === 'start' && args[1])
    if (!start) return ran
    const stdout = (ran.result as { stdout?: string } | undefined)?.stdout ?? ''
    const room = (await startedRoom(stdout)) ?? (await newRoom($, start[1] ?? '', startedAt))
    if (!room) return ran
    await put($, selected, () => room.id)
    await lead($, room.id, true)
    void openPane($)
    return { ...ran, context: [HANDOFF] }
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    isPaneFocused = e.props.isFocused
    const st = await snapshot($)
    const els = $.ui.resolve(e)
    const { Box, Text, Button, Markdown, Code } = els
    const room = current(st)
    if (!room) {
      return (
        <Box flexDirection="column">
          <Text dimColor>No active peer room in this checkout. Start one with /peer:peer TASK.</Text>
          {st.watchError && <Text color="red">{st.watchError}</Text>}
        </Box>
      )
    }
    const active = activeRooms(st)
    const isOpen = !room.ended_at
    const all = st.messages[room.id] ?? []
    // A kicked member keeps its tab, ○, for its log.
    const others = room.members.filter(m => m.role !== 'main')
    // A member's tab from another room shows this room's chat.
    const shown = others.some(m => m.role === st.tab) ? st.tab : 'chat'
    const pending = st.confirm

    // armed draws a button that acts on its second press: the first turns
    // it into confirm, and three seconds without the second turn it back.
    const armed = (what: string, key: string, label: string, argv: string[]) =>
      pending === what ? (
        <Button
          key={key}
          label="confirm"
          variant="primary"
          onPress={async () => {
            await put($, confirm, () => null)
            const { exitCode, stderr } = await peer($, argv)
            if (exitCode !== 0) $.ui.toast(`peer: ${stderr.trim()}`)
          }}
        />
      ) : (
        <Button
          key={key}
          label={label}
          variant="secondary"
          onPress={async () => {
            await put($, confirm, () => what)
            $.clock.after(3000, () => void put($, confirm, now => (now === what ? null : now)))
          }}
        />
      )

    // One row: the chat, a tab per member and, at the right, the end button.
    // A member's tab shows its log.
    const pick = (one: string) => async () => {
      await put($, tab, () => one)
      if (one === 'chat') toNewest($)
      else await loadLog($, room)
    }
    // Every tab is a button of one size, so a switch moves nothing; the
    // dot is part of the label, so it is pressed with the rest: ● runs, ○
    // has stopped, as every member of an ended room has.
    const dot = (isOn: boolean) => (isOn ? '●' : '○')
    const header = (
      <Box flexDirection="column" gap={1}>
        {active.length > 1 && 'Select' in els && (
          <els.Select
            key="room"
            label="room"
            value={room.id}
            options={active.map(one => ({ value: one.id }))}
            onSelect={value => put($, selected, () => value)}
          />
        )}
        <Box gap={1} flexWrap="wrap">
          <Button
            key="tab-chat"
            label={`${dot(isOpen)} chat ${all.length}`}
            variant={shown === 'chat' ? 'primary' : 'secondary'}
            onPress={pick('chat')}
          />
          {others.map(m => (
            <Button
              key={`tab-${m.role}`}
              label={`${dot(isOpen && !m.exited && !m.kicked)} ${m.role}`}
              variant={shown === m.role ? 'primary' : 'secondary'}
              onPress={pick(m.role)}
            />
          ))}
          <Box flexGrow={1} />
          {isOpen && armed('end', 'end', '⏹ end', ['end', room.id, '--as', 'user'])}
        </Box>
        {st.watchError && <Text color="red">{st.watchError}</Text>}
      </Box>
    )

    let body
    if (shown === 'chat') {
      // Newest first: the chat grows down from the tabs.
      const list = all.slice(-60).reverse()
      body = (
        <Box flexDirection="column" gap={1}>
          {e.surface !== 'terminal' && 'Svg' in els && list.length > 0 && (
            <els.Svg source={timeline(room, list)} alt={`Message timeline of ${room.id}`} />
          )}
          {list.length === 0 && <Text dimColor>No messages yet.</Text>}
          {groups(list).map(group =>
            group.notices ? (
              // One line reads in time order, though the lines run newest first.
              <Box key={`notice-${group.notices[0]?.id}`} gap={1}>
                <Text dimColor>{clock(group.notices[group.notices.length - 1]?.at ?? '')}</Text>
                <Text dimColor>{clip([...group.notices].reverse().map(m => m.text).join(' · '), 2000)}</Text>
              </Box>
            ) : (
              message(group.message)
            ),
          )}
        </Box>
      )
    } else {
      const member = room.members.find(m => m.role === shown)
      body = (
        <Box flexDirection="column" gap={1}>
          {member && (
            <Box gap={1}>
              <Text dimColor>
                {[member.agent, member.model, member.effort].filter(Boolean).join(' / ') || 'joined by hand'} · {state(member)}
                {member.worktree ? ` · worktree ${member.worktree}` : ''}
                {member.branch ? ` · ${member.branch}` : ''}
              </Text>
              <Box flexGrow={1} />
              {isOpen && !member.kicked && (
                armed(`kick:${member.role}`, 'kick', 'kick', ['kick', room.id, member.role, '--as', 'user'])
              )}
            </Box>
          )}
          {st.log.length === 0 && <Text dimColor>No log yet.</Text>}
          {st.log.slice(-80).reverse().map((entry, i) => (
              <Box key={`log-${i}`} gap={1}>
                <Text dimColor>{entry.at && !entry.at.startsWith('0001') ? clock(entry.at, true) : '        '}</Text>
                <Box flexGrow={1} flexShrink={1}>
                  {entry.kind === 'tool' ? (
                    <Code source={clip(entry.text, 500)} language="bash" />
                  ) : (
                    <Text dimColor={entry.kind !== 'text'} wrap={entry.kind === 'text' ? 'wrap' : 'truncate-end'}>
                      {clip(entry.kind === 'text' ? entry.text : entry.text.split('\n').slice(0, 3).join(' ⏎ '), 500)}
                    </Text>
                  )}
                </Box>
              </Box>
            ))}
        </Box>
      )
    }

    function message(m: PeerMessage) {
      const color = authorColor(room!, m.from)
      const lines = m.text.split('\n')
      const isLong = lines.length > 14 && !st.expanded.includes(m.id)
      const text = isLong ? excerpt(lines, 12) : m.text
      return (
        <Box key={`msg-${m.id}`} flexDirection="column" borderStyle="single" borderColor={borderOf(room!, m.from)} paddingX={1}>
          <Box gap={1}>
            <Text dimColor>{clock(m.at)}</Text>
            <Text bold color={color}>
              {m.from}
            </Text>
            <Text dimColor>→</Text>
            <Text color={m.to === EVERYONE ? undefined : authorColor(room!, m.to)}>{m.to === EVERYONE ? 'all' : m.to}</Text>
          </Box>
          <Markdown key={`md-${m.id}`} text={clip(linkify(text), 8000)} />
          {lines.length > 14 && (
            <Button
              key={`more-${m.id}`}
              label={isLong ? `show all ${lines.length} lines` : 'show less'}
              plain
              onPress={() => put($, expanded, list => (isLong ? [...list, m.id] : list.filter(one => one !== m.id)))}
            />
          )}
        </Box>
      )
    }

    return (
      <Box flexDirection="column" gap={1}>
        {header}
        {body}
      </Box>
    )
  })

  // The prompt footer gets the peer icon, with the count of messages that
  // came since the pane last showed: one room opens the pane on it; with
  // several, a numbered button per room, in the pane's order.
  on('ui.render', { component: 'SessionMode' }, async ($, e, next) => {
    const st = await snapshot($)
    const active = activeRooms(st)
    if (active.length === 0) return next(e)
    const { Box, Text, Button } = $.ui.resolve(e)
    const show = (id: string) => async () => {
      await put($, selected, () => id)
      await openPane($)
    }
    const fresh = (room: PeerRoom) => {
      const n = (st.messages[room.id] ?? []).length - (st.seen[room.id] ?? 0)
      return n > 0 ? ` ${n}` : ''
    }
    return (
      <Box gap={1}>
        {e.props.modes.length > 0 && <Text dimColor>{e.props.modes.join(' & ')}</Text>}
        {active.length === 1 && active[0] && (
          <Button key="peer-open" label={`${ICON}${fresh(active[0])}`} plain onPress={show(active[0].id)} />
        )}
        {active.length > 1 && <Text>{ICON}</Text>}
        {active.length > 1 &&
          active.map((room, i) => (
            <Button key={`peer-open-${i + 1}`} label={`${i + 1}${fresh(room)}`} onPress={show(room.id)} />
          ))}
      </Box>
    )
  })

  // The rows this plugin puts into the chat, a delivery or an end
  // note, are hidden there: the toast, the pane and the footer's count show
  // them. ctrl+o shows each as the model reads it. A surface without ctrl+o,
  // such as the desktop, marks every row expanded, so it always hides them.
  on('ui.render', { component: 'UserMessage' }, ($, e, next) => {
    const hasCtrlO = e.surface === 'terminal' || e.surface === 'vscode'
    if ((hasCtrlO && e.props.isExpanded) || !isPeerRow(e.props.text)) return next(e)
    const { Box } = $.ui.resolve(e)
    return <Box />
  })
}

// startedRoom reads the room peer start printed, when the output reached
// the tool whole.
async function startedRoom(stdout: string) {
  try {
    const room = JSON.parse(stdout.trim().split('\n')[0] ?? '') as PeerRoom
    return room.id && room.members?.[0]?.role === 'main' ? room : null
  } catch {
    return null
  }
}

// newRoom finds the room a peer start NAME made when its output was cut or
// piped away: NAME, or NAME-2 and so on, started since the command ran.
async function newRoom($: Engine, name: string, since: string) {
  const { exitCode, stdout } = await peer($, ['status'])
  if (exitCode !== 0) return null
  const rooms: PeerRoom[] = []
  for (const line of stdout.split('\n')) {
    try {
      if (line.trim()) rooms.push(JSON.parse(line))
    } catch {
      // skip a line that is not JSON
    }
  }
  const named = new RegExp(`^${name.replace(/[^a-z0-9-]/g, '')}(-\\d+)?$`)
  return rooms.filter(room => named.test(room.id) && room.started_at >= since).sort((a, b) => b.started_at.localeCompare(a.started_at))[0] ?? null
}

// shellCommands splits a shell command line into its commands, each a list
// of words with quotes removed and leading VAR=value words dropped; text
// inside quotes, as in echo "peer wait", is not a command. It reads simple
// command lines, not every shell form.
function shellCommands(line: string): string[][] {
  const commands: string[][] = []
  let words: string[] = []
  let word = ''
  let hasWord = false
  let quote = ''
  const endWord = () => {
    if (hasWord) words.push(word)
    word = ''
    hasWord = false
  }
  const endCommand = () => {
    endWord()
    const at = words.findIndex(w => !/^[A-Za-z_][A-Za-z0-9_]*=/.test(w))
    if (at >= 0) commands.push(words.slice(at))
    words = []
  }
  for (const c of line) {
    if (quote) {
      if (c === quote) quote = ''
      else word += c
    } else if (c === '"' || c === "'") {
      quote = c
      hasWord = true
    } else if (c === ' ' || c === '\t') {
      endWord()
    } else if (c === ';' || c === '&' || c === '|' || c === '\n' || c === '(' || c === ')') {
      endCommand()
    } else {
      word += c
      hasWord = true
    }
  }
  endCommand()
  return commands
}

// peerArgs returns a command's arguments when it runs peer.
function peerArgs(words: string[]) {
  const name = words[0] ?? ''
  return name === 'peer' || name.endsWith('/peer') ? words.slice(1) : null
}

function asMain(args: string[]) {
  return args.some((arg, i) => arg === '--as=main' || (arg === '--as' && args[i + 1] === 'main'))
}

// authorColor gives each participant the color of its kind: main, the
// user, peer's notices, workers and the other members.
function authorColor(room: PeerRoom, name: string) {
  if (name === 'main') return '#4c8dff'
  if (name === 'user') return '#e5c07b'
  if (name === 'peer') return 'gray'
  return room.members.find(m => m.role === name)?.worker ? '#ff9f43' : '#b48cff'
}

// borderOf is a participant's color mixed into the dark background, so a
// card's frame tells its author without drawing the eye.
function borderOf(room: PeerRoom, name: string) {
  const color = authorColor(room, name)
  if (!color.startsWith('#')) return '#3a3a3a'
  const mix = (at: number) => Math.round(Number.parseInt(color.slice(at, at + 2), 16) * 0.3 + 0x1b * 0.7)
  return `#${[1, 3, 5].map(at => mix(at).toString(16).padStart(2, '0')).join('')}`
}

type Group = { message: PeerMessage; notices?: undefined } | { notices: PeerMessage[]; message?: undefined }

// groups puts runs of peer's notices on one line between the messages.
function groups(list: PeerMessage[]): Group[] {
  const out: Group[] = []
  for (const m of list) {
    const last = out[out.length - 1]
    if (m.from !== 'peer') out.push({ message: m })
    else if (last?.notices) last.notices.push(m)
    else out.push({ notices: [m] })
  }
  return out
}

// excerpt keeps the first n lines and closes a code fence left open.
function excerpt(lines: string[], n: number) {
  const head = lines.slice(0, n)
  const fences = head.filter(line => line.trimStart().startsWith('```')).length
  return [...head, ...(fences % 2 === 1 ? ['```'] : []), '…'].join('\n')
}

const PATH_REF = /(?:\/|\.{1,2}\/)?(?:[\w.@-]+\/)*[\w@-][\w.@-]*\.[A-Za-z]\w*(?::\d+)?(?::\d+)?/g

// linkify turns references to the checkout's files, as file.go:42 or
// `file.go:42`, into file links; code blocks stay as written.
function linkify(text: string) {
  if (files.paths.size === 0) return text
  const link = (ref: string, shown: string) => {
    const path = ref.split(':')[0] ?? ''
    const rel = path.startsWith(`${files.repo}/`) ? path.slice(files.repo.length + 1) : path.replace(/^\.\//, '')
    return files.paths.has(rel) ? `[${shown}](file://${encodeURI(`${files.repo}/${rel}`)})` : shown
  }
  return text
    .split(/(```[\s\S]*?```)/)
    .map(part =>
      part.startsWith('```')
        ? part
        : part
            .split(/(`[^`\n]+`)/)
            .map(span =>
              span.startsWith('`')
                ? new RegExp(`^${PATH_REF.source}$`).test(span.slice(1, -1))
                  ? link(span.slice(1, -1), span)
                  : span
                : span.replace(PATH_REF, ref => link(ref, ref)),
            )
            .join(''),
    )
    .join('')
}

// clock shows an RFC 3339 time in the host's time zone, as 15:04 or,
// with seconds, 15:04:05.
function clock(at: string, withSeconds = false) {
  const t = Date.parse(at)
  if (Number.isNaN(t)) return ''
  const d = new Date(t + utcOffset * 60_000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}${withSeconds ? `:${pad(d.getUTCSeconds())}` : ''}`
}

// isPeerRow tells a row this plugin submitted by its text; the engine's
// framing may surround it.
function isPeerRow(text: string) {
  return /(?:^|\n)peer room \S+: \d+ new messages? for main, delivered by the peer plugin|(?:^|\n)peer room \S+ has ended\. Its members/.test(text)
}

// clip cuts text to at most max characters: the desktop refuses a whole
// drawing with a text over 10,000 characters, and a member's log carries
// tool results of any size.
function clip(text: string, max: number) {
  return text.length > max ? `${text.slice(0, max - 1)}…` : text
}

// timeline draws one row per participant and one dot per message it sent.
function timeline(room: PeerRoom, list: PeerMessage[]) {
  const roles = ['main', ...room.members.filter(m => m.role !== 'main').map(m => m.role), 'user', 'peer']
  const used = roles.filter(role => role === 'main' || list.some(m => m.from === role))
  const start = Date.parse(room.started_at)
  const end = room.ended_at ? Date.parse(room.ended_at) : Math.max(start + 1, ...list.map(m => Date.parse(m.at)))
  const width = 560
  const left = 110
  const row = 18
  const height = used.length * row + 8
  const x = (at: string) => left + ((Date.parse(at) - start) / Math.max(1, end - start)) * (width - left - 10)
  const esc = (s: string) => s.replace(/[<>&"]/g, c => `&#${c.charCodeAt(0)};`)
  const rows = used
    .map((role, i) => {
      const y = i * row + 12
      const dots = list
        .filter(m => m.from === role)
        .map(m => `<circle cx="${x(m.at).toFixed(1)}" cy="${y}" r="3.5" fill="${role === 'peer' ? '#999' : authorColor(room, role)}"/>`)
        .join('')
      return `<text x="0" y="${y + 4}" font-size="11" fill="#888">${esc(role)}</text><line x1="${left}" y1="${y}" x2="${width - 10}" y2="${y}" stroke="#8884"/>${dots}`
    })
    .join('')
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${width} ${height}" width="${width}" height="${height}" font-family="sans-serif">${rows}</svg>`
}
