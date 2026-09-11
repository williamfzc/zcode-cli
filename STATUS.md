# zcodecli status

## Live verification

Verified against ZCode `3.11.2` (`3.11.2.6792`), Electron 41 / Chrome 146 CDP, debug port `9227`.

- `start` restarts ZCode with `open -a ZCode --args --remote-debugging-port=9227`.
- `status` reports app state, CDP readiness, browser/protocol version, and page targets.
- `stop` gracefully quits ZCode and waits for exit; it does not force-kill.
- `ssh list` returned 6 SSH aliases in two stable reads.
- `explore methods` returned 120 bridge methods; all methods are non-writable and non-configurable.
- Additional read methods were called twice through one CDP WebSocket and returned stable shapes.
- Every exposed business command was checked in both table output and single-line JSON.
- `make build`, `make test`, and `go vet ./...` pass. `make test` also uses external linking for the Go test binary.

### Chat (drives the agent)

Chat does not go through `window.zcode` (it exposes no session/message methods). `zcodecli` walks the renderer's React fiber tree to the active workspace's `workspaceScopedServices` and uses `zcodeSessionService`, `zcodeTaskService`, and `zcodeAgentService` RPC proxies.

- `chat list` returned the active workspace's sessions with `sessionId`, generated title, status, mode, kind, and timestamps in both table and JSON.
- `chat show <id>` and `--all` returned the chronological user/assistant turns; visible assistant text is taken from `content` (falling back to `type:"content"` parts; thoughts and tool calls are excluded).
- `chat new "reply with the single word ok"` created a native session, submitted the prompt, waited for the turn to finish, and printed a final reply of exactly `ok`.
- `chat send <same-id> "reply with the single word done" --json` continued that session and returned `{"reply":"done",...,"status":"completed"}`.
- The two replies were read back with `chat show` and matched exactly.
- Submission acknowledgement only means "accepted"; the final reply is collected by polling `getTaskSnapshot` until the matching turn has non-empty assistant text and the task is no longer running.
- `envelope.clientId` must be the renderer's registered conversation client id (localStorage `zcode-v4-client-id:v1`); an invented id is rejected by the host with `fault.command.clientMismatch`.
- The create/send calls omit model/config so the workspace's real defaults are used; nothing is fabricated.

## Exposed read commands

| Command | Bridge method | Live result |
|---|---|---|
| `ssh list` | `listSSHConfigAliases` | array, 6 items |
| `system locale` | `getSystemLocale` | non-empty string |
| `system device-id` | `getDeviceId` | UUID-shaped string |
| `editors list` | `getInstalledEditors` | array, 6 items with `id`, `name`, `iconDataUrl` |
| `docker status` | `isDockerAvailable` | boolean |
| `docker containers` | `listDockerContainers` | array; empty on the verification machine |
| `wsl list` | `listWSLDistros` | array; empty on macOS verification machine |
| `updates status` | `getAutoUpdatePreferences`, `getUpdateState` | object with update settings/state |
| `window state` | `getDesktopWindowChromeState`, `getDesktopZoomLevel`, `getWindowControlsOverlayMetrics` | object with window state |
| `remote status` | `getWebRemoteControlStatus` | object with non-empty status |
| `desktop activity` | `getDesktopSessionActivity` | object with non-negative counts |
| `community status` | `canOpenCommunity` | boolean |
| `explore methods` | local metadata enumeration | 120 methods; no bridge invocation |

| `chat list` | `zcodeSessionService.listSessions` via workspace services | array of sessions |
| `chat show` | `zcodeTaskService.getTaskSnapshot` | chronological messages + meta |
| `chat new` | `zcodeAgentService.sendConversationCommandV4` (`createSession` then `sendText`) | new session id + final reply |
| `chat send` | `zcodeAgentService.sendConversationCommandV4` (`sendText`) | final reply |

## Blocked or not exposed

- No model list methods exist on `window.zcode`; `getModels`, `listModels`, and `getAvailableModels` were absent.
- Workspace methods are activation, binding, opening, or navigation operations, not stable read-list operations.
- Remote-control connect, disconnect, start, stop, pairing, and task/workspace sync methods can change state or manage sessions.
- Update download, cancel, skip, quit-and-install, and update-window methods are write/lifecycle operations.
- Browser-view attach, detach, close, restore, resize, and visibility methods are UI control operations.
- File methods such as save, select, open in editor/file manager, export logs, and print-to-PDF have filesystem or UI side effects.
- MCP load/save/migrate methods can change user configuration.
- OAuth, payment, permission onboarding, and CUA helper drag methods are authorization/consent flows.
- Telemetry, logging, custom-event, and trace-report methods send data.
- Event methods named `on*` register subscriptions and are not read commands.
- `getApplicationIcon` returned `null`; `getPathForFile` requires an unclear parameter; neither is exposed.
- Internal debug/trace methods (`getRendererActionTraceConfig`, `getZCodeStdioTapDevState`) are stable reads but are not user-facing commands.
- Screenshot/capture methods can read screen contents and are not exposed.

- A chat turn that needs a permission/approval prompt fails non-zero; `zcodecli` cannot grant approvals, so the user must open ZCode for such turns.
- Sending into archived/inactive history was not exercised; ids must come from the non-archived `chat list` for the already-open workspace.
- Streaming token deltas are not surfaced; `chat new`/`chat send` return the completed reply (use the app UI for live streaming).

## Audit fixes (2026-09-10)

- The compiled-in `skill` guide now covers the full command surface, including the chat domain and its discipline (ids from real output, permission turns fail non-zero, no workspace switching or remote sessions); the outdated wording that listed all "send" operations as human-only actions is gone.
- Contract mismatch errors now uniformly append "the application version may have changed" (via a shared `contractMismatch` helper).
- `make build`, `make test`, and `go vet ./...` pass after these changes.
