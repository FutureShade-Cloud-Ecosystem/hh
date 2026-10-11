// Package likeutil holds the one LIKE/ILIKE pattern escaper every module's search shares, so no
// two searches can grow two different escape rules. A caller's search text is a term to find, not
// a pattern to interpret: under `ESCAPE '\'` its '%', '_' and '\' match literally.
package likeutil

import "strings"

// escaper escapes the SQL LIKE/ILIKE metacharacters so a user search term is matched literally
// under `ESCAPE '\'`: backslash first (it is the escape character), then the % and _ wildcards.
var escaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// EscapeLikePattern neutralizes LIKE wildcards in a user-supplied term before it is bound as a
// LIKE/ILIKE pattern, so '%' and '_' in the term cannot widen a search into a wildcard scan. The
// bound result stays a parameter; the statement that uses it must name the same escape character
// (`ESCAPE '\'`), or the backslashes this adds are matched as text instead of being consumed.
//
// This is the helper the product catalogue search has always used (repository.go's SKU ILIKE and
// the counter search); it lives here so the pos, inventory and payments searches share the one
// rule instead of copying it.
func EscapeLikePattern(s string) string { return escaper.Replace(s) }
