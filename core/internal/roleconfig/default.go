package roleconfig

import _ "embed"

// The decided default (D48), carried into hh as the seed of record: the
// matrix's 130 cells, the rules' role lists and the eleven dealer settings,
// derived faithfully from the principal's planning record. default.json's
// source block and the constants here name that record, by sha256. Per
// dealer overrides are later work; until they land, this is the
// configuration every dealer is seeded with.
const (
	// DefaultPlanningData is the planning record's data file the default
	// was derived from.
	DefaultPlanningData = "role-access-r3.json"
	// DefaultPlanningDataSHA256 is that file's sha256 at the derivation.
	DefaultPlanningDataSHA256 = "8f9dc29430750629fe250dbc0b5d9ce6edf14fc8e422b40239392aec18528092"
	// DefaultPlanningDoc is the planning record's narrative document.
	DefaultPlanningDoc = "ROLE-ACCESS-r3.md"
	// DefaultPlanningDocSHA256 is that document's sha256 at the derivation.
	DefaultPlanningDocSHA256 = "7801e4cd398c725d14bad33e9371b938fa4cc0608dc1a6da3002d674338b6733"
)

//go:embed default.json
var defaultData []byte

// DefaultData returns the embedded default document's bytes. The slice is
// a copy; the caller cannot change the seed.
func DefaultData() []byte {
	return append([]byte(nil), defaultData...)
}
