# zcodecli

> **Auto-generated with [cdp2cli](https://github.com/williamfzc/cdp2cli)** — target app
> ZCode 3.11.2 (Electron 41 / Chrome 146), CDP port `9227`, live-verified 2026-09-10.

`zcodecli` is a standalone Go command-line client for the local ZCode Electron app. It drives the app through Chrome DevTools Protocol on port `9227`.

Read commands call the frozen `window.zcode` contextBridge methods directly. Chat commands actually drive the agent (create sessions, send prompts, and collect the finished reply) through the renderer's workspace services, which `zcodecli` reaches by walking the in-page React fiber tree. No hooks are installed and the frozen bridge is never wrapped.

## Build

```sh
make build
```

The Makefile uses `-ldflags="-linkmode=external"` to avoid the macOS Go linker `LC_UUID` issue.

## Development

```sh
make fmt
make vet
make test
make build
```

## Use

Start the app with the local debug port first:

```sh
./zcodecli start
```

Business commands do not restart the app silently. If the debug port is closed, run `start` first.

Chat commands (these drive the agent in the currently open workspace):

```sh
./zcodecli chat list
./zcodecli chat show <sessionId>            # most recent messages
./zcodecli chat show <sessionId> --all       # full history
./zcodecli chat new "reply with the word ok" # create a session, send a prompt, wait for the reply
./zcodecli chat send <sessionId> "follow up" # continue a session, wait for the reply
```

`chat new` and `chat send` block until the agent finishes its turn and then print the final reply text (not just a submission acknowledgement). A turn that stops on a permission/approval prompt, reports an error, or does not finish within the timeout exits non-zero. Session ids always come from `chat list` or the `chat new` output.

Read commands:

```sh
./zcodecli status
./zcodecli ssh list
./zcodecli system locale
./zcodecli system device-id
./zcodecli editors list
./zcodecli docker status
./zcodecli docker containers
./zcodecli wsl list
./zcodecli updates status
./zcodecli window state
./zcodecli remote status
./zcodecli desktop activity
./zcodecli community status
./zcodecli explore methods
```

`./zcodecli skill` prints the built-in agent guide (compiled into the binary).

Every business command supports `--json` for single-line JSON. The default output is a human-readable table. Contract mismatches and connection errors exit non-zero.

Stop the debug-enabled app when finished:

```sh
./zcodecli stop
```

## Safety

The debug port is a plaintext local control surface for a logged-in application. Keep it closed when not in use. The CLI serializes local CDP operations with a lock and closes its WebSocket after each command.

Only stable read commands are exposed for app state. The single state-changing surface is the chat domain (`chat new` / `chat send`), which is the product's core "ask the agent to do work" capability and only runs when invoked explicitly. Workspace switching/opening, remote connect, install/update, authorization/pairing, and other environment-changing actions are intentionally not automated. Chat commands operate on the workspace that is already open in the app; they never switch workspaces or start remote sessions.
