package dbx

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// DSNParts is a parsed connection string.
//
// Parsed rather than handed to a driver because two callers need to read parts
// of it - the migrator and the operator CLI - and because a password must be
// re-attached out of band. A parsed DSN with no password is safe to log, print
// in a preflight report, or include in an error message.
type DSNParts struct {
	Host     string
	Port     int
	Database string
	User     string
	SSLMode  string
	// Password is parsed so a caller can reject a DSN that carries one. Nothing
	// in this product wants it in the environment or in a URL.
	Password string
}

// String renders the parts back to a DSN with no password.
func (p DSNParts) String() string {
	u := url.URL{Scheme: "postgres", Host: fmt.Sprintf("%s:%d", p.Host, p.Port), Path: "/" + p.Database}
	if p.User != "" {
		u.User = url.User(p.User)
	}
	q := u.Query()
	if p.SSLMode != "" {
		q.Set("sslmode", p.SSLMode)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// ParseDSN reads a postgres:// or postgresql:// URL.
func ParseDSN(dsn string) (DSNParts, error) {
	var p DSNParts
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		return p, fmt.Errorf("dbx: %q is not a postgres:// DSN", dsn)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return p, fmt.Errorf("dbx: parse DSN: %w", err)
	}
	if u.User != nil {
		p.User = u.User.Username()
		p.Password, _ = u.User.Password()
	}
	p.Host = u.Hostname()
	if ps := u.Port(); ps != "" {
		if p.Port, err = strconv.Atoi(ps); err != nil {
			return p, fmt.Errorf("dbx: parse DSN port %q: %w", ps, err)
		}
	}
	p.Database = strings.TrimPrefix(u.Path, "/")
	p.SSLMode = u.Query().Get("sslmode")
	if p.Host == "" || p.Database == "" {
		return p, fmt.Errorf("dbx: DSN must name a host and a database")
	}
	return p, nil
}

// ApplyDSN overlays a DSN onto a Config. A field absent from the DSN keeps the
// Config's value, so a site configuration can set defaults that a one-off
// override only partially replaces.
//
// "Absent" means the DSN did not supply it, not that the DSN's zero value should
// be written over the configuration. url.Parse leaves Port at 0 and User empty
// when they are not in the DSN, and assigning them unconditionally turned
// ApplyDSN("postgres://host/db") over a DefaultConfig() into
// "postgres://:0/db" — a connection string that names a port no listener holds.
// Password is not applied at all: it is parsed so a caller can reject a DSN that
// carries one, and it is never read from a config file or the environment.
func (c *Config) ApplyDSN(dsn string) error {
	p, err := ParseDSN(dsn)
	if err != nil {
		return err
	}
	// Host and Database are not guarded, and cannot be: ParseDSN refuses a DSN
	// that supplies neither, so by the time this runs they are always the DSN's
	// own values and an absent one is already an error.
	c.Host = p.Host
	c.Database = p.Database
	if p.Port != 0 {
		c.Port = p.Port
	}
	if p.User != "" {
		c.User = p.User
	}
	if p.SSLMode != "" {
		c.SSLMode = p.SSLMode
	}
	return nil
}
