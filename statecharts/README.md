# statecharts

Runtime-agnostic XState-style statechart definitions: no Bun or DOM imports,
guards and actions referenced by name, so a later Go runtime is a port
(context section 2, standing decision 6). The device-local offline sync
machine (`offline -> online -> pulling -> pushing -> idle`, with `conflict`
and `auth_expired`) lives here with model-based tests as the acceptance gate.
