// Package toml provides a minimal TOML parser for kmazarin.toml boot configuration.
// It handles only the subset of TOML needed for boot config: comments, key=value
// (integers and quoted strings), single-line string arrays, and [[table]] sections.
package toml

import "mazzy/shared/constants"

// Parse parses a kmazarin.toml file and returns a BootConfig.
func Parse(data []byte) *constants.BootConfig {
	cfg := &constants.BootConfig{}
	p := parser{data: data, cfg: cfg}
	p.parse()
	return cfg
}

type section int

const (
	sectionTop             section = iota // top-level key=value
	sectionBootstrapPriest               // inside [[bootstrap_priest]]
	sectionPriest                        // inside [[priest]]
)

type parser struct {
	data []byte
	pos  int
	cfg  *constants.BootConfig
	sec  section
}

func (p *parser) parse() {
	for p.pos < len(p.data) {
		p.skipWhitespace()
		if p.pos >= len(p.data) {
			break
		}

		ch := p.data[p.pos]

		// Comment
		if ch == '#' {
			p.skipLine()
			continue
		}

		// Blank line
		if ch == '\n' {
			p.pos++
			continue
		}

		// [[table]]
		if ch == '[' && p.pos+1 < len(p.data) && p.data[p.pos+1] == '[' {
			p.parseTableHeader()
			continue
		}

		// key = value
		p.parseKeyValue()
	}
}

func (p *parser) parseTableHeader() {
	p.pos += 2 // skip [[
	name := p.readUntil(']')
	// skip ]]
	if p.pos < len(p.data) && p.data[p.pos] == ']' {
		p.pos++
	}
	if p.pos < len(p.data) && p.data[p.pos] == ']' {
		p.pos++
	}
	p.skipLine()

	switch name {
	case "bootstrap_priest":
		if p.cfg.BootstrapPriestCount < constants.MaxBootstrapPriests {
			p.cfg.BootstrapPriestCount++
		}
		p.sec = sectionBootstrapPriest
	case "priest":
		if p.cfg.PriestCount < constants.MaxPriests {
			p.cfg.PriestCount++
		}
		p.sec = sectionPriest
	}
}

func (p *parser) parseKeyValue() {
	key := p.readKey()
	p.skipSpaces()
	if p.pos >= len(p.data) || p.data[p.pos] != '=' {
		p.skipLine()
		return
	}
	p.pos++ // skip '='
	p.skipSpaces()

	if p.pos >= len(p.data) {
		return
	}

	switch p.sec {
	case sectionTop:
		p.parseTopLevel(key)
	case sectionBootstrapPriest:
		idx := p.cfg.BootstrapPriestCount - 1
		if idx >= 0 && idx < constants.MaxBootstrapPriests {
			p.parsePriestField(key, &p.cfg.BootstrapPriests[idx])
		} else {
			p.skipLine()
		}
	case sectionPriest:
		idx := p.cfg.PriestCount - 1
		if idx >= 0 && idx < constants.MaxPriests {
			p.parsePriestField(key, &p.cfg.Priests[idx])
		} else {
			p.skipLine()
		}
	}
}

func (p *parser) parseTopLevel(key string) {
	ch := p.data[p.pos]
	if ch == '"' {
		val := p.readQuotedString()
		if key == "timezone" {
			copyToArray(p.cfg.Timezone[:], val)
		}
		p.skipLine()
	} else if ch == 't' || ch == 'f' {
		val := p.readBool()
		switch key {
		case "suppress_serial_stdio_copy":
			p.cfg.SuppressSerialStdioCopy = val
		case "suppress_kernel_serial":
			p.cfg.SuppressKernelSerial = val
		}
		p.skipLine()
	} else {
		val := p.readInt()
		switch key {
		case "min_cpus":
			p.cfg.MinCpus = val
		case "max_cpus":
			p.cfg.MaxCpus = val
		case "min_ram_mb":
			p.cfg.MinRamMB = val
		case "max_ram_mb":
			p.cfg.MaxRamMB = val
		case "kernel_budget_mb":
			p.cfg.KernelBudgetMB = val
		case "gc_percentage":
			p.cfg.GCPercentage = val
		}
		p.skipLine()
	}
}

func (p *parser) parsePriestField(key string, priest *constants.BootPriest) {
	ch := p.data[p.pos]
	switch {
	case ch == '"':
		val := p.readQuotedString()
		switch key {
		case "name":
			copyToArray(priest.Name[:], val)
		case "path":
			copyToArray(priest.Path[:], val)
		}
		p.skipLine()
	case ch == '[':
		// String array: ["name1=/path1", "name2=/path2"]
		entries := p.readStringArray()
		switch key {
		case "mzr":
			for _, e := range entries {
				if priest.MzrCount >= constants.MaxModulesPerPriest {
					break
				}
				name, path := splitNamePath(e)
				mod := &priest.Mzr[priest.MzrCount]
				copyToArray(mod.Name[:], name)
				copyToArray(mod.Path[:], path)
				priest.MzrCount++
			}
		case "maz":
			for _, e := range entries {
				if priest.MazCount >= constants.MaxModulesPerPriest {
					break
				}
				name, path := splitNamePath(e)
				mod := &priest.Maz[priest.MazCount]
				copyToArray(mod.Name[:], name)
				copyToArray(mod.Path[:], path)
				priest.MazCount++
			}
		}
		p.skipLine()
	default:
		p.skipLine()
	}
}

// readKey reads a bare key (letters, digits, underscore, hyphen).
func (p *parser) readKey() string {
	start := p.pos
	for p.pos < len(p.data) {
		ch := p.data[p.pos]
		if ch == '=' || ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			break
		}
		p.pos++
	}
	return string(p.data[start:p.pos])
}

// readQuotedString reads a "quoted string" (no escape sequences).
func (p *parser) readQuotedString() string {
	if p.pos >= len(p.data) || p.data[p.pos] != '"' {
		return ""
	}
	p.pos++ // skip opening "
	start := p.pos
	for p.pos < len(p.data) && p.data[p.pos] != '"' && p.data[p.pos] != '\n' {
		p.pos++
	}
	end := p.pos
	if p.pos < len(p.data) && p.data[p.pos] == '"' {
		p.pos++ // skip closing "
	}
	return string(p.data[start:end])
}

// readBool reads a TOML boolean (true or false).
func (p *parser) readBool() bool {
	start := p.pos
	for p.pos < len(p.data) && p.data[p.pos] >= 'a' && p.data[p.pos] <= 'z' {
		p.pos++
	}
	return string(p.data[start:p.pos]) == "true"
}

// readInt reads a decimal integer.
func (p *parser) readInt() int {
	val := 0
	for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
		val = val*10 + int(p.data[p.pos]-'0')
		p.pos++
	}
	return val
}

// readStringArray reads ["a", "b", "c"] (single-line).
func (p *parser) readStringArray() []string {
	if p.pos >= len(p.data) || p.data[p.pos] != '[' {
		return nil
	}
	p.pos++ // skip [

	var result []string
	for p.pos < len(p.data) {
		p.skipSpaces()
		if p.pos >= len(p.data) {
			break
		}
		if p.data[p.pos] == ']' {
			p.pos++
			break
		}
		if p.data[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.data[p.pos] == '"' {
			s := p.readQuotedString()
			result = append(result, s)
		} else {
			break
		}
	}
	return result
}

// readUntil reads until the given byte (exclusive), returning the string.
func (p *parser) readUntil(stop byte) string {
	start := p.pos
	for p.pos < len(p.data) && p.data[p.pos] != stop && p.data[p.pos] != '\n' {
		p.pos++
	}
	return string(p.data[start:p.pos])
}

func (p *parser) skipWhitespace() {
	for p.pos < len(p.data) && (p.data[p.pos] == ' ' || p.data[p.pos] == '\t' || p.data[p.pos] == '\r') {
		p.pos++
	}
}

func (p *parser) skipSpaces() {
	for p.pos < len(p.data) && (p.data[p.pos] == ' ' || p.data[p.pos] == '\t') {
		p.pos++
	}
}

func (p *parser) skipLine() {
	for p.pos < len(p.data) && p.data[p.pos] != '\n' {
		p.pos++
	}
	if p.pos < len(p.data) {
		p.pos++ // skip \n
	}
}

// splitNamePath splits "name=/path" on the first '='.
func splitNamePath(s string) (name, path string) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

// copyToArray copies a string into a fixed-size byte array with null termination.
func copyToArray(dst []byte, src string) {
	n := len(src)
	if n > len(dst)-1 {
		n = len(dst) - 1
	}
	for i := 0; i < n; i++ {
		dst[i] = src[i]
	}
	dst[n] = 0
}
