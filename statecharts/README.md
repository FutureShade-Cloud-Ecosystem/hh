# statecharts

Runtime-agnostic statechart definitions: no Bun or DOM imports, no I/O, so a later Go
runtime is a port (context section 2, standing decision 6). A machine here is a pure
`transition(state, event)` returning the next state and the named effects to perform.

`sync/machine.ts` is the device-local offline sync machine: `offline`, `pushing`,
`pulling`, `idle`, `conflict`, `auth_expired` and `unreachable` (a request failed while the
platform says online; a request or a reconnect leaves it). A cycle is push then pull, and a
push fault still pulls. Its table
test is `sync/test/machine.test.mjs`; the package that drives it
(`web/packages/offline-store`) runs that test and the generated path model tests in the
web CI loop.
