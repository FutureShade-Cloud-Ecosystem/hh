// libpq_service.go writes a libpq service file for a database URL, so that
// psql reads its connection from a file and the URL never sits on a command
// line.
//
// A URL passed as an argument sits in the process list of every process on
// the box, where anything that can read that list can read the database's
// password. The service file is mode owner only, it lives on the shared
// memory tmpfs, the child that reads it never inherits the URL, and it is
// removed as soon as the child is done.
//
// The parser reads the URL's shape exactly:
//
//	postgres://[user[:password]@]host[:port]/database[?params]
//
// and refuses anything it cannot read, naming DATABASE_URL and never a
// value. The parse follows libpq's own reading of a URI: the userinfo ends
// at the LAST '@' (so a password may carry a raw '@'), the user and the
// password split at the FIRST ':', a bracketed host carries its port after
// the ']', and every parameter after the '?' is a libpq connection parameter
// whose name is decoded (libpq reads the URI's names decoded) and checked
// for a line break, raw and encoded, before it is written into the service
// file.
//
// This is the Go half of deploy/finance/database-url.sh, the shell the box's
// entrypoint uses for the same job.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// libpqServiceFile writes the service file for a database URL and answers
// its path and the remover that takes it away again.
func libpqServiceFile(dsn string) (string, func(), error) {
	body, err := libpqServiceBody(dsn)
	if err != nil {
		return "", func() {}, err
	}
	dir := "/dev/shm"
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		dir = ""
	}
	file, err := os.CreateTemp(dir, "finance-box-pgservice-*.conf")
	if err != nil {
		return "", func() {}, fmt.Errorf("the libpq service file could not be written: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	// Mode owner only: the file is the only place the password lives.
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("the libpq service file could not be written: %w", err)
	}
	if _, err := file.WriteString(body); err != nil {
		file.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("the libpq service file could not be written: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("the libpq service file could not be written: %w", err)
	}
	return path, cleanup, nil
}

// libpqServiceBody renders the service file's text for a database URL.
func libpqServiceBody(dsn string) (string, error) {
	raw := strings.TrimSpace(dsn)
	if !strings.HasPrefix(raw, "postgres://") && !strings.HasPrefix(raw, "postgresql://") {
		return "", fmt.Errorf("DATABASE_URL must be a postgres URL (postgres:// or postgresql://)")
	}
	rest := raw[strings.Index(raw, "://")+3:]
	query := ""
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		query = rest[i+1:]
		rest = rest[:i]
	}
	userinfo := ""
	hostport := rest
	if i := strings.LastIndexByte(rest, '@'); i >= 0 {
		userinfo = rest[:i]
		hostport = rest[i+1:]
	}
	authority := hostport
	database := ""
	if i := strings.IndexByte(hostport, '/'); i >= 0 {
		authority = hostport[:i]
		database = hostport[i+1:]
	}
	if database == "" {
		return "", fmt.Errorf("DATABASE_URL must name a database (the path after the host)")
	}
	host := authority
	port := ""
	// The shell half reads a bracketed authority only when it both starts
	// with '[' and carries a ']' somewhere after it; anything else is read
	// as a plain authority, where the host ends at the FIRST colon and the
	// port starts after the LAST one.
	if strings.HasPrefix(authority, "[") && strings.Contains(authority, "]") {
		end := strings.Index(authority, "]")
		host = authority[1:end]
		tail := authority[end+1:]
		if strings.HasPrefix(tail, ":") {
			port = tail[1:]
		}
	} else if strings.Contains(authority, ":") {
		host = authority[:strings.IndexByte(authority, ':')]
		port = authority[strings.LastIndexByte(authority, ':')+1:]
	}
	if host == "" {
		return "", fmt.Errorf("DATABASE_URL must name a host")
	}
	if port != "" {
		for _, r := range port {
			if r < '0' || r > '9' {
				return "", fmt.Errorf("DATABASE_URL's port must be digits")
			}
		}
	}
	user := ""
	password := ""
	if userinfo != "" {
		if i := strings.IndexByte(userinfo, ':'); i >= 0 {
			user = userinfo[:i]
			password = userinfo[i+1:]
		} else {
			user = userinfo
		}
	}
	var params strings.Builder
	for _, pair := range strings.Split(query, "&") {
		if pair == "" {
			continue
		}
		key := pair
		value := ""
		if i := strings.IndexByte(pair, '='); i >= 0 {
			key = pair[:i]
			value = pair[i+1:]
		}
		// The name is read twice: once as it stands, once decoded. The raw
		// check comes first because the decoder reads line by line, so a raw
		// newline would be folded away before a check after it could see
		// it. The decoded check catches the percent encoded newline, and
		// the decoded name is what the service file carries, because
		// libpq reads the URI's parameter names decoded.
		if !oneLine(key) {
			return "", fmt.Errorf("DATABASE_URL carries a line break in one of its parts")
		}
		decodedKey := pgURIDecode(key)
		if !oneLine(decodedKey) {
			return "", fmt.Errorf("DATABASE_URL carries a line break in one of its parts")
		}
		if decodedKey == "" {
			continue
		}
		switch strings.ToLower(decodedKey) {
		case "service", "servicefile":
			return "", fmt.Errorf("DATABASE_URL must not carry a service parameter (the restore reads one database, the one the URL names)")
		}
		decodedValue := pgURIDecode(value)
		if !oneLine(decodedValue) {
			return "", fmt.Errorf("DATABASE_URL carries a line break in one of its parts")
		}
		params.WriteString(decodedKey + "=" + decodedValue + "\n")
	}
	host = pgURIDecode(host)
	database = pgURIDecode(database)
	user = pgURIDecode(user)
	password = pgURIDecode(password)
	for _, part := range []string{host, database, user, password} {
		if !oneLine(part) {
			return "", fmt.Errorf("DATABASE_URL carries a line break in one of its parts")
		}
	}
	var body strings.Builder
	body.WriteString("[" + pgServiceName + "]\n")
	body.WriteString("host=" + host + "\n")
	if port != "" {
		body.WriteString("port=" + port + "\n")
	}
	body.WriteString("dbname=" + database + "\n")
	if user != "" {
		body.WriteString("user=" + user + "\n")
	}
	if password != "" {
		body.WriteString("password=" + password + "\n")
	}
	body.WriteString(params.String())
	return body.String(), nil
}

// oneLine answers whether a value can be written into the service file as
// one line: a line break or a carriage return would break the file's shape,
// and the file is the only place the password lives.
func oneLine(value string) bool {
	return !strings.ContainsAny(value, "\n\r")
}

// pgURIDecode decodes a percent escaped URI part the way libpq reads one and
// the way the shell half's pg_url_decode reads one: the escapes are decoded
// and nothing else is. A '+' stays a '+' (a URI part is not a form body),
// an escape that is not a hex pair stays as it stands, and the decode is byte
// oriented, so a multi byte character comes back as the bytes it was encoded
// from.
func pgURIDecode(value string) string {
	var out strings.Builder
	out.Grow(len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '%' && i+2 < len(value) {
			hi, okHi := hexDigit(value[i+1])
			lo, okLo := hexDigit(value[i+2])
			if okHi && okLo {
				out.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		out.WriteByte(c)
	}
	return out.String()
}

// hexDigit answers the value of one hex digit and whether the byte was one.
func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// libpqServiceFilePath is the file's own path, for the messages that name
// where the service file lives.
func libpqServiceFilePath(path string) string {
	return filepath.Base(path)
}
