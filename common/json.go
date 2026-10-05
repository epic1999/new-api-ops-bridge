// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
func DecodeJson(reader io.Reader, v any) error {
	d := json.NewDecoder(reader)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON value")
	}
	return nil
}
func DecodeBytes(data []byte, v any) error { return DecodeJson(bytes.NewReader(data), v) }
