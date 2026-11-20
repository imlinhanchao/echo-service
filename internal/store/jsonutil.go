package store

import (
	"encoding/json"

	"gorm.io/datatypes"
)

// JSON marshals v for a GORM JSON column; it never fails (falls back to null).
func JSON(v any) datatypes.JSON {
	b, err := json.Marshal(v)
	if err != nil {
		return datatypes.JSON([]byte("null"))
	}
	return datatypes.JSON(b)
}

// DecodeJSON unmarshals a JSON column into out.
func DecodeJSON(raw datatypes.JSON, out any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
