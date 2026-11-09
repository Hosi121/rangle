package generation

import (
	"strconv"
	"strings"
)

// Public cacheability is independent of immutable publication paths. A CDN may
// rewrite max-age and omit the optional immutable hint. s-maxage, when present,
// determines shared-cache freshness (RFC 9111 sections 4.2.1 and 5.2.2.10).
func publicArtifactCache(values []string) bool {
	public, maxSeen, sharedSeen := false, false, false
	var maxAge, sharedAge uint64
	for _, field := range values {
		remaining := strings.Trim(field, " \t")
		for remaining != "" {
			if remaining[0] == ',' {
				remaining = strings.TrimLeft(remaining[1:], " \t")
				continue
			}
			name, rest := cacheToken(remaining)
			if name == "" {
				return false
			}
			rest = strings.TrimLeft(rest, " \t")
			value, hasValue := "", false
			if strings.HasPrefix(rest, "=") {
				hasValue = true
				var ok bool
				value, rest, ok = cacheValue(strings.TrimLeft(rest[1:], " \t"))
				if !ok {
					return false
				}
				rest = strings.TrimLeft(rest, " \t")
			}
			if rest != "" && rest[0] != ',' {
				return false
			}
			switch strings.ToLower(name) {
			case "public":
				if hasValue {
					return false
				}
				public = true
			case "private", "no-store", "no-cache":
				return false
			case "max-age", "s-maxage":
				if !hasValue || value == "" {
					return false
				}
				for _, digit := range value {
					if digit < '0' || digit > '9' {
						return false
					}
				}
				seconds, err := strconv.ParseUint(value, 10, 63)
				if err != nil {
					return false
				}
				if strings.EqualFold(name, "s-maxage") {
					if sharedSeen {
						return false
					}
					sharedSeen, sharedAge = true, seconds
				} else {
					if maxSeen {
						return false
					}
					maxSeen, maxAge = true, seconds
				}
			}
			remaining = rest
		}
	}
	if sharedSeen {
		return public && sharedAge > 0
	}
	return public && maxSeen && maxAge > 0
}

func cacheToken(value string) (string, string) {
	for index := 0; index < len(value); index++ {
		b := value[index]
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(b)) {
			continue
		}
		return value[:index], value[index:]
	}
	return value, ""
}

// Consume quoted extension values before looking for the next directive, so
// a word such as public inside a quoted string can never grant cacheability.
func cacheValue(value string) (string, string, bool) {
	if value == "" {
		return "", "", false
	}
	if value[0] != '"' {
		token, rest := cacheToken(value)
		return token, rest, token != ""
	}
	var decoded strings.Builder
	for index := 1; index < len(value); index++ {
		b := value[index]
		if b == '"' {
			return decoded.String(), value[index+1:], true
		}
		if b == '\\' {
			index++
			if index >= len(value) {
				return "", "", false
			}
			b = value[index]
		}
		if b < 32 && b != '\t' || b == 127 {
			return "", "", false
		}
		decoded.WriteByte(b)
	}
	return "", "", false
}
