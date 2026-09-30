package config

// secretSetters returns, per secret variable, a function that blanks that
// exact field.
//
// Each one reads and writes its own field. The first attempt shared a single
// writer that switched on the field's VALUE, which means two secrets holding the
// same string would blank whichever matched first — and a redaction scheme that
// can blank the wrong field is worse than none.
//
// The blanking is conditional on the value being SET. A fully public camera has
// no password, and a dump saying "<redacted: present>" for it makes a claim
// about the camera that is false: it says a credential exists where none was
// configured.
func (c Config) secretSetters() map[string]func(*Config) {
	return map[string]func(*Config){
		"SP_POC_XPROTECT_CLIENT_SECRET": func(c *Config) {
			if c.XProtectClientSecret != "" {
				c.XProtectClientSecret = Redacted
			}
		},
		"SP_POC_XPROTECT_PASSWORD": func(c *Config) {
			if c.XProtectPassword != "" {
				c.XProtectPassword = Redacted
			}
		},
		"SP_POC_SOURCE_PASSWORD": func(c *Config) {
			if c.SourcePassword != "" {
				c.SourcePassword = Redacted
			}
		},
	}
}

// Redacted is what a secret prints as.
//
// It is a MARKER and not an empty string, because an empty string in a config
// dump reads as "not set" and sends an operator to check the wrong variable.
const Redacted = "<redacted: present>"

// Redacted returns a COPY of the configuration with every secret blanked.
//
// The copy is what the dump prints. `watch --config` calls this rather than
// picking fields, so a fourth secret cannot be added without this covering it.
func (c Config) Redacted() Config {
	out := c
	for _, blank := range c.secretSetters() {
		blank(&out)
	}
	return out
}

// SecretFields reports which fields of the configuration were blanked. The dump
// prints it, so a reader can see WHICH secrets exist without seeing any of them:
// a config dump that silently omits a password reads as "there is no password",
// which is the wrong conclusion about a live camera.
func (c Config) SecretFields() []string {
	blanked := c.secretSetters()
	names := make([]string, 0, len(blanked))
	for _, name := range Secrets() {
		if _, ok := blanked[name]; ok {
			names = append(names, name)
		}
	}
	return names
}
