# core/db/queries

This directory currently holds this README alone. sqlc query files would live
here in this directory, one file per module, generating the typed repository
layer the boundary rules in `AGENTS.md` describe. Queries never reach across
module ownership lines; cross-module reads go through the owning module's
interface.
