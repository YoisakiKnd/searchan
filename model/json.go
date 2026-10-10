package model

import "encoding/json"

func marshalInput(v any) ([]byte, error) { return json.Marshal(v) }
