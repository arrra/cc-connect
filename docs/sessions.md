# v1 Context Management Layer (Sessions)

## What sessions are

Each Slack thread or DM is an independent **session** with a bounded working set. When you @mention the bot in a channel, a new session starts scoped to that thread. Replies in the same thread continue the session. Direct messages use the DM channel as the session boundary. Each session maintains only the minimal context needed for the current turn: the root objective, the most recent user message, the most recent tool result, and any pinned items — everything else is evicted. Sessions terminate automatically after 30 minutes of idle time.

To enable v1 sessions, set `CC_CONNECT_SESSIONS_V1=1` before starting cc-connect.

## /pin

Pin items so they survive working-set eviction:

```
/pin <text>                  Pin any text to the current session
/pin                         (while replying) Pin the parent message's text
```

Pinned items appear in every subsequent turn's context until the session terminates.

**Persistence:** Pins are saved to `~/.cc-connect/data/pins.json` on every `/pin` invocation and when a session terminates. On restart, pins for a session key auto-load into any new session spawned for that key — so your pins survive daemon restarts even though sessions themselves do not.

## Feature flag

Sessions v1 is **off by default**. To enable:

```bash
CC_CONNECT_SESSIONS_V1=1 cc-connect
```

To roll back instantly, unset the variable or set it to `0` and restart. The old accumulating-context behavior resumes immediately with no state migration required.

If you use `config.toml`, you can also set:

```toml
[v1_sessions]
enabled = true
```

The environment variable takes precedence over the config file value.

## Restart behavior

Sessions are held in memory. On daemon restart:

- **Sessions are lost** — users re-@mention the bot to start a fresh session.
- **Pinned items survive** — loaded automatically from `~/.cc-connect/data/pins.json` when a new session spawns for the same thread/DM.

After a restart, start a new session in the same thread with a new @mention. Your pins will be available immediately.

## Known v1 limitations

The following are deliberate v1 constraints, not bugs. They are planned for v2/v3:

- **Bot responses not in working set** — the bot's own prior replies are intentionally excluded from each turn's context. If the agent needs to reference its prior output, pin it explicitly with `/pin`.
- **In-memory sessions die on restart** — durable session storage (survive restarts) is v2.
- **No correction / reference detection** — the agent does not detect when a message refers to a prior response; repeat context explicitly or use `/pin`. This is v2 Meter/Composer scope.
- **Single coarse mutex** — the session store uses one `sync.Mutex` for all operations. Per-key locking and read-write separation are v2 performance improvements.
- **Hex retrieval always fires** — v1 does not gate Hex retrieval based on working-set content; every turn triggers retrieval regardless. The Retrieval Gate is v2.
