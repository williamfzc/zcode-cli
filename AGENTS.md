# AGENTS.md — zcodecli development handoff

Goal: turn **ZCode (an Electron desktop app)** into a standalone command line,
`zcodecli` (a single-file Go binary).

## Methodology: use the global `cdp2cli` command (already on PATH; do not look for the cdp2cli repository)

Before starting, read in order:

- `cdp2cli skill build` — the SOP for spinning a standalone CLI out of a contract
  (project layout, CDP client, lifecycle, command template, verification loop, the
  Go linker LC_UUID pitfall). Primarily follow this one.
- `cdp2cli skill attach` — how to attach to Electron (`open -a ZCode --args
  --remote-debugging-port=9227`; note that launching the ZCode binary directly
  exits immediately, so `open -a` is required; the single-instance lock means you
  must quit first, then relaunch with the flag).
- `cdp2cli skill explore` — forensic recipes (bridge object enumeration,
  `window.zcode` methods, Network, DOM).
- `cdp2cli skill safety` — pitfalls and safety.
- `cdp2cli contracts ssh-list` — ZCode's first verified read-only contract sample
  (`ssh list`).

## Established ZCode facts (from earlier exploration; a starting point, but re-verify yourself)

- Electron 41, debug port 9227. CDP invocation: `Runtime.evaluate` calling
  `window.zcode.<method>` directly (method-level contextBridge), **not** HTTP
  request-replay.
- `window.zcode` methods are `writable=false, configurable=false` (frozen); they
  cannot be wrapped or hooked, only called directly.
- About 120 `zcode:`-prefixed IPC channels enumerated (68 invoke request-response
  + 18 one-way send).
- Verified command: `ssh list` (see `cdp2cli contracts ssh-list`, returns an array).
- These facts come from historical exploration. Probe and re-verify them on the
  current app version before landing anything (the app may have updated).

## Requirements

1. Standalone Go project (this directory), structured per `cdp2cli skill build`:
   main/cdp/lifecycle/session/commands/skill.
2. Lifecycle status/start/stop uses `open -a ZCode --args
   --remote-debugging-port=9227` (not the raw binary).
3. Build `ssh list` first and prove it on a real machine; then use the forensic
   recipes to enumerate the other read-only methods on `window.zcode` and turn
   the stable, side-effect-free ones into commands (models/list, workspace,
   various status/get/list). During probing, write methods are never truly
   invoked; if such a method becomes a CLI command, it still executes directly
   but only when the user explicitly triggers it.
4. Every command must get both a human-readable table and `--json` right.
   Contract mismatch exits non-zero. `make build` (with `-linkmode=external`).
5. Verify each command on a real machine (start by launching ZCode with 9227).
   Keep README/AGENTS status current, and commit as you go.

## Chat surface: actually driving the agent (implemented; see chat.go)

There are **no** session/message methods on `window.zcode`, and the frozen bridge
cannot be wrapped. Chat goes through the renderer-internal workspace services,
reached via the React fiber tree — no globals, hooks, or listeners installed:

- Forensic path: start from the DOM's `__reactFiber$*` / `__reactContainer$*`
  handles, walk the fiber's `child/sibling` links, and deep-search
  `memoizedProps/pendingProps` for an object containing `workspaceScopedServices`
  (the active workspace's service bag). The bag provides `activeWorkspacePath`,
  optional `workspaceIdentity`/`workspaceRemoteSessionId`, and
  `zcodeSessionService` / `zcodeTaskService` / `zcodeAgentService` (RPC proxies
  whose methods are directly callable; empty own-property enumeration is normal).
- Every command re-walks the fiber tree; the bag is used as soon as it is found —
  never cached, never injected as a global.
- Read: `zcodeSessionService.listSessions({workspacePath, includeArchived:false,
  limit})` returns the session array; `zcodeTaskService.getTaskSnapshot({taskId,
  workspacePath, messageLimit?})` fetches messages. Do **not** use
  `zcodeSessionService.readSession` — it fails with -32004 "Session is not
  active" for non-active history.
- Write: `zcodeAgentService.sendConversationCommandV4({workspacePath,
  workspaceIdentity?, envelope})`.
  - Create: `envelope={clientId, commandId(fresh), issuedAt, sessionId:null,
    type:'createSession', payload:{workspaceId: identity?.trim()||workspacePath}}`.
    No config/model is passed, so the workspace's real defaults apply. Success
    ACK: `status:'accepted'` with `result.type:'createSession'` and a native
    `result.sessionId`.
  - Send: `type:'sendText'` with `sessionId` set to the native id and
    `payload:{text}`. Success ACK: `result.type:'inputAccepted'`.
  - **Key pitfall**: `envelope.clientId` must equal the conversation client id the
    renderer registered during handshake (localStorage key
    `zcode-v4-client-id:v1`, value shaped like `client-<uuid>`); an invented id is
    rejected by the host as `fault.command.clientMismatch`. Read this value inside
    the page; never fabricate it on the Go side.
  - `createSession`/`sendText` are not CAS/row-target commands and need no
    `baseRevision/baseLogEpoch`.
  - An ACK's `accepted` only means accepted for processing; it does not include
    the final reply.
- Reply collection: after sending, poll `getTaskSnapshot`. For a new session the
  target turn is 0; for a continuation it is the baseline snapshot's max
  turnIndex + 1. Done when that turn has a user message matching the prompt text,
  the same turn's assistant has non-empty visible text (`content`, or the
  concatenation of `parts` entries with `type:'content'`, ignoring
  thought/tool-call), and `meta.status` is no longer running/paused. Exit
  non-zero on `meta.status='error'` or a non-empty `runtime.pendingPermissions`
  (the CLI cannot grant permissions; the user must act inside the app). Poll
  every 2s; time out after 10 minutes.
- Only operate on workspaces **already open** in the app; no workspace
  switching/opening, remote connections, update installs, or authorization
  pairing. Session ids come only from real `chat list` or `chat new` output.
- Contract shapes live in `docs/contracts/chat.json` (no real ids/paths/titles/
  history content).

## Conventions (from the workspace standards in ../skills)

- One language: code, identifiers, comments, commits, and docs are all English.
- Commit messages follow Angular Conventional Commits (`feat:`, `fix:`, `docs:`,
  `chore:`, ...); one commit names one move.
- Verify before committing: `make fmt && make vet && make test && make build`
  must pass, and command changes must be re-verified against the live app.
- A fact lives in one place; link to it instead of restating it.
- Every source file opens with a short header stating what it is.

## Hard constraints

- During probing, only call read-only methods; write/delete/execute operations
  are never sent on their own, and ids come only from real returns.
- Do not attempt to wrap the frozen bridge; call it directly via
  `Runtime.evaluate` only.
- One CDP WebSocket connection at a time, closed when done; the debug port is a
  plaintext control surface, so `stop` it when finished.
- Mark uncertain or side-effectful capabilities as blocked with a clear reason;
  do not force them.
- Finish with a summary: which commands were added, real-machine returns, the
  commit, and which domains remain blocked.
