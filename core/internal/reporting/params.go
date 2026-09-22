package reporting

// params.go, the ONLY door between a caller's input and a query. Everything in this file exists to
// make one sentence true: a value supplied by a caller reaches Postgres as a BOUND PARAMETER of a known
// type, or it does not reach Postgres at all.
//
// -- WHY REFUSAL, NOT TOLERANCE -----------------------------------------------------------------------
// The tempting implementation ignores parameters it does not recognise. That is friendlier and it is
// wrong, for a reason that has nothing to do with security theatre: once unknown parameters are
// ignored, callers stop being able to tell a typo from a filter that silently did nothing. A report
// that quietly ignored `?branch=KGN` (instead of `branch_id`) shows the whole company's numbers under a
// heading that says Kingston, and the person reading it has no way to know. So:
//
//	an unknown parameter        → 400, naming it
//	a malformed value           → 400, naming the parameter and the expected kind
//	an enum value off the list  → 400, naming the allowed values
//	a missing required value    → 400, naming it
//
// -- WHY THERE IS NO free-text PARAMETER KIND ---------------------------------------------------------
// Every kind here has a total validation: a date parses or it does not, a uuid parses or it does not,
// an enum is on a closed list or it is not, an integer is in range or it is not. A "text" kind would be
// the one parameter shape whose validation is "looks fine to me", and it is the shape a future template
// would reach for to add a name search. When that day comes the right answer is a `text_prefix` kind
// with an explicit length cap and a bound `LIKE $n || '%'`, a new kind, reviewed, with its own test -
// not a hole widened in this one.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/google/uuid"
)

// DateLayout is the one accepted date format. ISO-8601 date, no time, no zone: a report parameter is a
// business date, and admitting a timestamp would silently make "2026-08-01" mean different days to two
// people in two timezones looking at the same screen.
const DateLayout = "2006-01-02"

// maxIntParam bounds an integer parameter. No template uses one today; the bound is here so the first
// one that does cannot be handed 2^63 and turn a report into a denial of service.
const maxIntParam = 1_000_000

// bindParams validates `in` against the template's ordered ParamSpecs and returns the $1..$n bind
// arguments plus the NORMALIZED parameter map (what was actually bound, canonicalised).
//
// The returned args slice is positional and its length always equals len(specs), a template's query
// text never varies with which optional parameters were supplied, because the optionality lives in the
// SQL as a NULL-or-empty check. That invariant is what makes "one code, one query" literally true.
func bindParams(specs []ParamSpec, in map[string]string) ([]any, map[string]string, error) {
	known := make(map[string]bool, len(specs))
	for _, s := range specs {
		known[s.Name] = true
	}
	// Refuse first, bind second. A request carrying an unknown parameter is answered before any work.
	for name := range in {
		if !known[name] {
			return nil, nil, httpx.BadRequestf("unknown report parameter %q", name)
		}
	}

	args := make([]any, 0, len(specs))
	norm := make(map[string]string, len(specs))
	for _, s := range specs {
		raw := strings.TrimSpace(in[s.Name])
		if raw == "" {
			if s.Required {
				return nil, nil, httpx.BadRequestf("report parameter %q is required", s.Name)
			}
			// The ABSENT form of each kind. Note that absence is a typed NULL or an empty string, never
			// a missing bind: the query always takes the same number of arguments.
			switch s.Kind {
			case ParamUUID:
				args = append(args, nil)
			case ParamEnum:
				args = append(args, "")
			case ParamInt:
				args = append(args, int64(0))
			case ParamDate:
				args = append(args, nil)
			default:
				return nil, nil, fmt.Errorf("report parameter %q has unknown kind %q", s.Name, s.Kind)
			}
			continue
		}
		switch s.Kind {
		case ParamDate:
			d, err := time.Parse(DateLayout, raw)
			if err != nil {
				return nil, nil, httpx.BadRequestf("report parameter %q must be a date (YYYY-MM-DD)", s.Name)
			}
			args = append(args, d)
			norm[s.Name] = d.Format(DateLayout)
		case ParamUUID:
			u, err := uuid.Parse(raw)
			if err != nil {
				return nil, nil, httpx.BadRequestf("report parameter %q must be a uuid", s.Name)
			}
			args = append(args, u.String())
			norm[s.Name] = u.String()
		case ParamEnum:
			if !onList(s.Values, raw) {
				return nil, nil, httpx.BadRequestf("report parameter %q must be one of: %s",
					s.Name, strings.Join(s.Values, ", "))
			}
			args = append(args, raw)
			norm[s.Name] = raw
		case ParamInt:
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n < 0 || n > maxIntParam {
				return nil, nil, httpx.BadRequestf("report parameter %q must be an integer between 0 and %d",
					s.Name, maxIntParam)
			}
			args = append(args, n)
			norm[s.Name] = strconv.FormatInt(n, 10)
		default:
			return nil, nil, fmt.Errorf("report parameter %q has unknown kind %q", s.Name, s.Kind)
		}
	}
	return args, norm, nil
}

// onList reports whether v is on a closed set. Exact match only, no case folding, no trimming beyond
// the caller's, because an enum whose members differ only by case is a schema bug and folding would
// hide it.
func onList(values []string, v string) bool {
	for _, allowed := range values {
		if allowed == v {
			return true
		}
	}
	return false
}

// paramBranchID extracts the branch_id parameter (if the template has one) for the branch fence. It
// reads the NORMALIZED map, so it can only ever see a value that already parsed as a uuid.
func paramBranchID(norm map[string]string) string { return norm["branch_id"] }
