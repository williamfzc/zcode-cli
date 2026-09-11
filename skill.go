// The embedded operator guide printed by `zcodecli skill`.

package main

const skillGuide = `zcodecli is a CDP bridge client for the local ZCode Electron app.

Operator workflow:
1. Run 'zcodecli start' before business commands. It restarts ZCode with debug port 9227.
2. Read commands call 'window.zcode' directly through Runtime.evaluate. Do not wrap or hook
   'window.zcode'; its methods are frozen by contextBridge.
3. Chat commands drive the agent in the workspace that is already open in the app:
   'chat list', 'chat show <sessionId> [--all]', 'chat new "<prompt>"',
   'chat send <sessionId> "<prompt>"'. 'chat new' and 'chat send' block until the agent
   finishes its turn, then print the complete reply.
4. Session ids come only from real command output ('chat list' or 'chat new'); never invent one.
5. A turn that stops on a permission/approval prompt, reports an error, or times out exits
   non-zero. zcodecli cannot grant approvals; open ZCode to continue such a session.
6. Chat commands never switch or open workspaces and never start remote sessions. The same
   goes for install/update, authorization/pairing, and every other state-changing action
   outside the chat domain: do them in the app yourself.
7. Run 'zcodecli stop' when finished because the debug port is a local plaintext control surface.

Commands:
- status / start / stop (app lifecycle)
- chat list | chat show <sessionId> [--all] | chat new "<prompt>" | chat send <sessionId> "<prompt>"
- ssh list; system locale|device-id; editors list; docker status|containers; wsl list;
  updates status; window state; remote status; desktop activity; community status;
  explore methods (metadata only; invokes no bridge methods)
- skill (this guide)
`
