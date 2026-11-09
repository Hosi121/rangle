package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// Validate raw syntax before typed decoding loses duplicate/escaped field names
// or coerces null numbers into zero. Optional catalog fields must be omitted.
func catalogJSON(data []byte) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("catalog is not UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return fmt.Errorf("catalog JSON nesting exceeds 64")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		switch token {
		case nil:
			return fmt.Errorf("null catalog field")
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate catalog JSON key")
				}
				seen[name] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case json.Delim('['):
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case json.Delim('}'), json.Delim(']'):
			return fmt.Errorf("unexpected catalog delimiter")
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("catalog requires one JSON document")
	}
	return nil
}
