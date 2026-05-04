// Package config loads the flat key=value TOML file used by omarchy-wled (no nested tables).
package config

import (
	"os"
	"regexp"
	"strings"
)

// keyValueLine matches one non-indented key = value line in our config file.
var keyValueLine = regexp.MustCompile(`(?m)^(\w+)\s*=\s*(.+)$`)

// LoadFlatFile reads path and returns key→value strings; missing file yields an empty map.
func LoadFlatFile(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	return ParseFlatBytes(data)
}

// ParseFlatBytes extracts key=value pairs from simple flat TOML text (values may be quoted).
func ParseFlatBytes(data []byte) map[string]string {
	cfg := map[string]string{}
	for _, m := range keyValueLine.FindAllStringSubmatch(string(data), -1) {
		val := strings.TrimSpace(m[2])
		val = strings.Trim(val, `"`)
		cfg[m[1]] = val
	}
	return cfg
}

// ParseBool treats common truthy strings as true (matches prior CLI/config behavior).
func ParseBool(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "true" || s == "1" || s == "yes"
}
