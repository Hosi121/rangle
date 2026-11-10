package publication

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteReceipt atomically creates a receipt without replacing any existing
// content. An identical receipt is an idempotent success after re-verification.
func WriteReceipt(filename string, receipt *Receipt) error {
	if receipt == nil {
		return fmt.Errorf("nil receipt")
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if existing, err := readDocument(filename); err == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return fmt.Errorf("receipt already exists with different content")
	} else if !os.IsNotExist(err) {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(filename), ".rangle-receipt-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Hard-linking is atomic and fails if another verifier already wrote a
	// receipt here. No rename-overwrite race exists between parallel invocations.
	if err := os.Link(tmp.Name(), filename); err != nil {
		if existing, readErr := readDocument(filename); readErr == nil && bytes.Equal(existing, data) {
			return nil
		}
		return err
	}
	return nil
}
