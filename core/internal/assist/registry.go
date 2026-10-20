package assist

// registry.go, the one door every command goes through.
//
// THE WHOLE SECURITY ARGUMENT OF THIS FEATURE IS IN Run(). There is exactly one function that turns
// a command code into a command execution, and it is this one. Command.run is unexported, so no
// caller, not the HTTP handler, not the chat service, not a future consumer, can hold a Command
// value and invoke it around the gate. The chat provider does not even receive Command values; it
// receives the tool specs List() produced for THIS caller, and its tool calls are dispatched back
// through Run() with the SAME Caller the request arrived with.
//
// That is what "the assistant runs as the asking user" means in code: not a claim in a document,
// but the absence of any other path.
//
// WHAT Run() CHECKS, IN ORDER, AND WHY THAT ORDER
//
//	1. does the command exist            → ErrUnknownCommand
//	2. is it switched on in config       → ErrDisabled
//	3. may THIS caller run it            → ErrRefused
//	4. are the arguments present         → 400
//	5. run it, as the caller             → the read service's own scoping applies
//
// Existence before authority is deliberate and is the one ordering that leaks a bit: a caller can
// learn that "payment.lookup" is a real code and "payment.delete" is not. That is a fair trade for
// error messages that can tell a client bug from a permissions problem, and the enumeration it
// leaks is a fixed list in open source anyway. What Run() must never do is the reverse, check
// authority first and then reveal, through the shape of the failure, whether a command exists.
//
// NO CACHING ACROSS USERS, ANYWHERE. Policy is re-read per call. It is one small table; the cost is
// a query and the benefit is that there is no cache that could ever be keyed wrongly. The single
// most likely way to break the invariant this package exists to hold would be a per-command result
// cache that forgot to include the caller in its key, so there is no result cache at all.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// badArg builds the 400 a user sees when they typed something the command cannot use. It is
// httpx.BadRequestError so every handler in the app maps it the same way, a typo has been a 400
// and not a 500 since DIB-443.
func badArg(format string, a ...any) error { return httpx.BadRequestf(format, a...) }

// UnavailableError marks a command whose read service is not wired in this deployment. It is a 503,
// not a 500: nothing failed, a seam is simply not connected, and the honest answer names it.
type UnavailableError struct{ What string }

func (e UnavailableError) Error() string {
	return fmt.Sprintf("assist: %s is not available in this deployment", e.What)
}

func errUnwired(what string) error { return UnavailableError{What: what} }

// Policy is one row of assist_command_policy: an administrator's narrowing of a command.
type Policy struct {
	Enabled bool
	MaxTier int
}

// PolicyStore reads the command policy table. Declared as an interface so every test in this
// package runs without a database, and so a deployment with no policy rows at all still works -
// see policyFor.
type PolicyStore interface {
	CommandPolicy(ctx context.Context) (map[string]Policy, error)
}

// StaticPolicy is a PolicyStore with no database behind it: whatever map it holds, forever.
// It is what tests use, and it is also the safe fallback if the policy table is ever unreadable.
type StaticPolicy map[string]Policy

func (s StaticPolicy) CommandPolicy(context.Context) (map[string]Policy, error) {
	return map[string]Policy(s), nil
}

// Registry holds the commands and enforces the gate.
type Registry struct {
	byCode map[string]Command
	order  []string
	deps   Deps
	policy PolicyStore
}

// NewRegistry builds the registry over the built-in commands.
//
// A nil PolicyStore is legal and means "no administrator narrowing", every command runs at its
// code floor. That is the behaviour a deployment gets before migration 0153 has been applied, and
// it is the SAFE direction to fail in only because the code floor is the strict one: an unreadable
// policy table can never widen access.
func NewRegistry(deps Deps, policy PolicyStore) *Registry {
	return newRegistryWith(BuiltinCommands(), deps, policy)
}

func newRegistryWith(cmds []Command, deps Deps, policy PolicyStore) *Registry {
	r := &Registry{byCode: make(map[string]Command, len(cmds)), deps: deps, policy: policy}
	for _, c := range cmds {
		r.byCode[c.Code] = c
		r.order = append(r.order, c.Code)
	}
	return r
}

// effectiveTier is the authority gate, and it is the ONE place the "config narrows, never widens"
// rule is implemented. Returning the tighter (numerically smaller) of the code floor and the
// configured tier means an administrator, or anyone who gets write access to the config table -
// can only ever take permissions away.
func effectiveTier(codeFloor, configured int) int {
	if configured <= 0 || configured > 3 {
		// A missing or nonsensical row is not a licence to widen. Fall back to the code floor.
		return codeFloor
	}
	if configured < codeFloor {
		return configured
	}
	return codeFloor
}

// visible reports whether the caller may run this command under this policy, and why not if not.
func visible(cmd Command, p Policy, has bool, c Caller) error {
	if has && !p.Enabled {
		return ErrDisabled
	}
	if !c.HasRole() {
		// Tier 0: an account with no role assigned. Refused before any comparison, because
		// "unassigned" is an unknown authority and unknown authority is not frontline authority.
		return ErrRefused
	}
	tier := cmd.MaxTier
	if has {
		tier = effectiveTier(cmd.MaxTier, p.MaxTier)
	}
	if c.Tier > tier {
		return ErrRefused
	}
	return nil
}

func (r *Registry) policyMap(ctx context.Context) map[string]Policy {
	if r.policy == nil {
		return nil
	}
	m, err := r.policy.CommandPolicy(ctx)
	if err != nil {
		// An unreadable policy table degrades to the code floors rather than to an outage. The
		// direction is safe (code floors are the strict ones) and the assistant keeps working, which
		// matters because half B is supposed to work "forever even if AI is off".
		return nil
	}
	return m
}

// List returns the commands THIS caller may run, in menu order. It is the only enumeration of the
// registry any client gets, which is why it is filtered rather than annotated: a menu that shows a
// greyed-out "Customer balance" to a yard hand is a list of things to go and ask for.
func (r *Registry) List(ctx context.Context, c Caller) []Command {
	pm := r.policyMap(ctx)
	out := make([]Command, 0, len(r.order))
	for _, code := range r.order {
		cmd := r.byCode[code]
		p, has := pm[code]
		if err := visible(cmd, p, has, c); err != nil {
			continue
		}
		if cmd.describe != nil {
			cmd.Summary = cmd.describe(c)
		}
		out = append(out, cmd)
	}
	return out
}

// Lookup returns a command by code without any authority check. Unexported-by-convention usage:
// it exists for List/Run and for tests, and it deliberately does NOT expose run, so a caller who
// gets a Command value from it still cannot execute anything.
func (r *Registry) Lookup(code string) (Command, bool) {
	c, ok := r.byCode[strings.TrimSpace(code)]
	return c, ok
}

// Run executes one command as the caller. See the file header for the ordering argument.
func (r *Registry) Run(ctx context.Context, c Caller, code string, a Args) (Result, error) {
	cmd, ok := r.byCode[strings.TrimSpace(code)]
	if !ok {
		return Result{}, ErrUnknownCommand
	}
	pm := r.policyMap(ctx)
	p, has := pm[cmd.Code]
	if err := visible(cmd, p, has, c); err != nil {
		return Result{}, err
	}
	if a == nil {
		a = Args{}
	}
	for _, spec := range cmd.Args {
		if spec.Required && a.get(spec.Name) == "" {
			return Result{}, badArg("%s needs %s, for example %s.", cmd.Title, strings.ToLower(spec.Label), spec.Example)
		}
	}
	return cmd.run(ctx, r.deps, c, a)
}

// Codes returns every registered command code, sorted. Used by the config surface and by the test
// that pins the seeded policy rows to the code floors.
func (r *Registry) Codes() []string {
	out := append([]string(nil), r.order...)
	sort.Strings(out)
	return out
}
