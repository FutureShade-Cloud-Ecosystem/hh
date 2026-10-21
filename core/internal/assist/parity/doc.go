// Package parity holds the assistant's data path suites, which need a migrated and seeded database: the
// per record parity of the ai views against the order module's own record read, the raw query suite
// that runs the query compiler against the real read path, and the red team cases 2, 8, 11 and 16. It has
// no code of its own; CI maps the visibility function files to it (see visibility_files.txt).
package parity
