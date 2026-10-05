# ADR-0024: the machine bearer class (FB Move's load doors)

Status: Proposed

Drafted in cycle HH-LOAD-1, item LD-2, against the plan LOAD-0-r1 (section 3,
the gate's design) and Colton's CP0-2 answer A: FB Move alone calls the load
doors, with a machine credential of its own held in FB Vault, good for that
dealer and those doors only; the doors are switched off unless a deployment
turns them on; every batch is logged. It amends ADR-0020's bearer classes
exactly where the addendum leaves the door open: a token claiming neither
configured audience "is nobody for this API until ... a third value is
configured". This record configures that third value.

## Decision

There is a third bearer class, machine, beside staff and tenant, carried by
its own audience value resolved from verified claims before any authorization
decision (ADR-0020's classification rule, unchanged). The class exists only
where a deployment configures it:

- The machine audience and its key set arrive by configuration together or
  not at all. There is no default audience and no default key set: with
  neither set, a machine audience token is nobody for this API, exactly as
  before this record.
- The key set is selected by the token's audience (the review's P1-1
  ruling): a machine audience token consults the machine key set alone, and
  every other token consults the platform key set alone, so a staff or
  tenant login never waits on or fails because of FB Move's key endpoint,
  which matters exactly when the doors are switched on for the parallel run
  or the cut over load and every login on the deployment would otherwise
  sit behind that endpoint. The audience read that selects the set is
  unverified and trusted for nothing else; the class is confirmed after the
  signature verifies. A machine audience token whose key id the machine set
  does not carry is a credential failure (the machine class never rides the
  platform set), and a machine audience token whose key id also sits in the
  platform set is a deployment defect answered as operational, never
  resolved by an ordering guess; that ambiguity signal belongs to the
  machine audience alone, because platform tokens never read the machine
  set. A token signed by the machine key set can never claim the staff or
  tenant audience.
- Every restriction the verifier already carries binds the machine class
  unchanged: the asymmetric algorithm set (the HMAC family refused before
  key material is touched), the issuer, the required subject, expiry with
  no grace, issued-at and not-before, the audience exclusivity, and the
  tenant claim read on no class but tenant.
- The token's subject names the machine principal. The token carries no
  authority claim of any kind and none is read.

The machine principal is a record in the core, not a person and not a staff
login: one row per dealer org, carrying its org, its name, whether it is
enabled, and the doors (the contract operation ids) it may call. It holds no
staff role and appears in no roleconfig cell, so no staff role, no roleconfig
cell and no dealer setting can widen what it reaches. The principal's private
key never enters this core: FB Move keeps it in FB Vault and injects it into
its own run; the core holds the public key set's URL by environment only.

The gate (the machine leg, `core/internal/platform/authz/machinegate.go`):

- X-Org is required and must resolve to the principal's own org. Every
  other answer carries the staff leg's one 403 body: a missing header, a
  repeated header, a malformed slug, an unknown slug, another org, a
  disabled principal, an unknown principal and a door the principal does
  not hold are one answer, so the doors are no org directory, no principal
  directory and no door list. The staff and tenant legs keep refusing every
  other class exactly as they do (the wrong caller ledger's row 7), and a
  machine credential on any staff or tenant route answers the same 403.
- A check that cannot run (the principal source, the org anchor, the key
  set) answers the 503 family, never an admission: the operational
  inability rule ADR-0020 pins, carried to the third class.
- The census word `machine` (the contract's x-role and the MachineBearer
  security scheme name each other; machine operations carry no x-access,
  as every non-staff operation does not) registers only behind the machine
  gate, and the role gate registrar stops the boot when a machine worded
  registration arrives without it, the same idiom an unannotated staff
  route carries. The door the gate checks is the route's own operation id
  against the principal's door list.
- The doors register only where the deployment's configuration switches
  them on (`CORE_LOAD_DOORS=on`), off by default, following the feedback
  route's conditional registration idiom. Switched on, the machine
  audience and public key set URL are required with it (`CORE_MACHINE_AUDIENCE`,
  `CORE_MACHINE_JWKS_URL`); switched off, setting either is a contradiction
  the spawn refuses, never an unused credential. The intended shape is the
  dealer's staging copy for the parallel run and production only for the
  cut over load, on Colton's go for that run, then off again.

One audit row per batch, with the machine principal as actor and the counts
by outcome (`audit_log.actor_id` carries no foreign key, so a system actor
fits). The wiring lands inside `core/internal/load` with the load frame
itself; this record names it so the door's design carries its log from the
first batch.

## Why not a staff login

Rejected before the plan asked (LOAD-0 section 3.3): the dealer's highest
staff tier is held by the General Manager and by HR, so a door at that level
lets HR bulk-write prices and receivables; a person-shaped login for a
machine puts a password into automation and bends D38's staff gates; and no
"load operator" role answers the rule that agents operate under scoped keys,
gates and a log, with production Colton's. The machine principal holds
exactly the doors it holds, in its own record, beside the role
configuration, reachable by no cell.

## Why not the contract's operator word

The `operator` word (D4) is declared for the platform launcher's own binary
and left unregistered by this core; giving it a gate would revive a surface
this core never serves. The machine class is its own word with its own key
material, and the registrar's fail-closed census rule keeps any word without
a gate from serving.

## What this does not change

- ADR-0020's two legs: the staff and tenant routes resolve orgs exactly as
  they did, refuse every other class exactly as they did, and answer the
  same bodies. The machine class adds a leg; it re-points none.
- The staff gates stay a person's (D38, D3): no machine credential resolves
  a role, a tier or a branch, and no staff role reaches a door.
- Loading a dealer's real data stays FB Move's own plan and its own go
  (ADR-017 sections 2.6 and 2.7); the switch governs the doors' existence,
  never the decision to load.

## Sessions gated

- HH-LOAD-1 LD-1: the load frame's routes register behind the switch and
  the machine gate; the audit row per batch wires inside the load module
  with the principal as actor.
- HH-LOAD-1 LD-10: the end to end ledger re-proves the wrong caller rows
  against the real doors on the merged stack.
