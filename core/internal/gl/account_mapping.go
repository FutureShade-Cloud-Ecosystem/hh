package gl

// account_mapping.go, W3 AX-1 item 1: the accounting package export's account mapping. Each HH GL
// account maps to a target package's account code, per target (QuickBooks Online first, Sage 50
// next, decision 1.6). The list is a manager read: the mapping decides what the books say in
// another package's name, so the desk must see it. The set is the Financial Controller's act, the
// service-level narrowing the reopen and the head office set take beside their route gates, and it
// is audited: a trail row without the account words would point at a row a re-mapping or a retired
// account could leave behind, so the codes ride the trail in its allowlisted Extra keys.

import (
	"context"
	"fmt"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/jackc/pgx/v5"
)

// exportTargets is the closed set of packages the mapping knows. The empty target is not a package:
// it is the every-target list read, handled by the callers below.
var exportTargets = map[string]bool{
	TargetQuickBooksOnline: true,
	TargetSage50:           true,
}

// exportTargetWords answers the package's human words for the refusals and the trail. An unknown
// word answers itself, which cannot reach here: both callers refuse it first.
func exportTargetWords(target string) string {
	switch target {
	case TargetQuickBooksOnline:
		return "QuickBooks Online"
	case TargetSage50:
		return "Sage 50"
	}
	return target
}

// actGLAccountMappingSet is the sensitive-access action stamped on a Controller's mapping set.
const actGLAccountMappingSet = "GL_ACCOUNT_MAPPING_SET"

// checkExportTarget refuses an unknown package in words, naming what the mapping knows.
func checkExportTarget(target string) error {
	if exportTargets[target] {
		return nil
	}
	return badReq("gl: unknown export target %q; the mapping knows %s and %s",
		target, TargetQuickBooksOnline, TargetSage50)
}

// ListAccountMapping answers the mapping rows for one target, or every target when the target is
// empty, target then HH account code order. The empty word is the every-target read, not a package,
// so only a non-empty word is checked against the set.
func (s *Service) ListAccountMapping(ctx context.Context, target string) ([]AccountMapping, error) {
	if target != "" {
		if err := checkExportTarget(target); err != nil {
			return nil, err
		}
	}
	return s.store.ListAccountMapping(ctx, target)
}

// SetAccountMapping points one HH account at a target package's account code. Only the Financial
// Controller sets it; the row upserts on (target, account_code), so a re-set moves the mapping and
// never grows a second row for the same account.
func (s *Service) SetAccountMapping(ctx context.Context, target, accountCode, packageAccount string) (AccountMapping, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return AccountMapping{}, err
	}
	if caller.RoleCode != controllerRole {
		return AccountMapping{}, errNotControllerMapping
	}
	if err := checkExportTarget(target); err != nil {
		return AccountMapping{}, err
	}
	accountCode = strings.TrimSpace(accountCode)
	packageAccount = strings.TrimSpace(packageAccount)
	if accountCode == "" {
		return AccountMapping{}, badReq("gl: the mapping needs the HH account code")
	}
	if packageAccount == "" {
		return AccountMapping{}, badReq("gl: the mapping needs the target package's account code")
	}
	m, err := s.store.SetAccountMapping(ctx, target, accountCode, packageAccount, caller.UserID, func(ctx context.Context, tx pgx.Tx, m AccountMapping) error {
		if s.auditTx != nil && tx != nil {
			return s.auditTx.RecordTx(ctx, tx, s.mappingAuditEntry(m, caller.UserID))
		}
		if s.audit != nil {
			return s.audit.Record(ctx, s.mappingAuditEntry(m, caller.UserID))
		}
		return nil
	})
	if err != nil {
		return AccountMapping{}, err
	}
	return m, nil
}

// mappingAuditEntry builds the set's trail row: the row's own id as the entity, and the three code
// words (the package, the HH account, the package account) beside it, all code shaped, never
// identifiers.
func (s *Service) mappingAuditEntry(m AccountMapping, actorID string) audit.SensitiveAccess {
	return audit.SensitiveAccess{
		Action:     actGLAccountMappingSet,
		EntityType: "gl.account_mapping",
		EntityID:   m.ID,
		ActorID:    actorID,
		Extra: map[string]any{
			"export_target":   m.Target,
			"hh_account_code": m.AccountCode,
			"package_account": m.PackageAccount,
		},
	}
}

// unmappedWords renders the refusal's account list: code and name pairs, the words a Controller
// reads to set what is missing.
func unmappedWords(unmapped []UnmappedAccount) string {
	parts := make([]string, 0, len(unmapped))
	for _, u := range unmapped {
		parts = append(parts, fmt.Sprintf("%s %s", u.AccountCode, u.AccountName))
	}
	return strings.Join(parts, ", ")
}
