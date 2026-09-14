# core/internal

One flat package per module, following the module boundary rules in `AGENTS.md`:
`model.go`, `repository.go`, `service.go`, `handler.go`, tests beside the code.
Modules self-register via `RegisterRoutes` on the composition root in `cmd/core`.
Module code lands with lane HH-3; this directory is intentionally empty.
