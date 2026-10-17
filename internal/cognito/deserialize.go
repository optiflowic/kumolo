package cognito

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

const (
	msgUnexpectedStructure = "Start of structure or map found where not expected."
	msgUnexpectedList      = "Start of list found where not expected"
)

// serializationError is a body AWS cannot read as the input shape. An empty message is
// sent as a SerializationException without a Message.
type serializationError struct {
	message string
}

// decodeInput reads the body as the input structure the way AWS deserializes it: member
// names match case-sensitively, unknown members are dropped, the last of duplicate members
// wins and integers and booleans are coerced. The result holds only string, int64, bool,
// []any, map[string]any and, inside lists and maps, nil.
func decodeInput(in *inputShape, body []byte) (map[string]any, *serializationError) {
	if !json.Valid(body) {
		return nil, &serializationError{}
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	// The body is valid JSON, so no token read below can fail.
	tok, _ := dec.Token()
	switch tok {
	case json.Delim('{'):
		return decodeStructure(dec, in)
	case json.Delim('['):
		return nil, &serializationError{message: msgUnexpectedList}
	}
	return nil, &serializationError{}
}

func decodeValue(dec *json.Decoder, s *inputShape) (any, *serializationError) {
	tok, _ := dec.Token()
	switch t := tok.(type) {
	case nil:
		return nil, nil
	case json.Delim:
		if t == '[' {
			if s.kind != kindList {
				return nil, &serializationError{message: msgUnexpectedList}
			}
			return decodeList(dec, s)
		}
		switch s.kind {
		case kindStructure:
			return decodeStructure(dec, s)
		case kindMap:
			return decodeMap(dec, s)
		}
		return nil, &serializationError{message: msgUnexpectedStructure}
	}

	switch s.kind {
	case kindStructure:
		return nil, &serializationError{message: "Expected null"}
	case kindMap:
		return nil, &serializationError{message: "Expected map or null"}
	case kindList:
		return nil, &serializationError{message: "Expected list or null"}
	case kindString:
		return decodeString(tok)
	case kindInteger:
		return decodeInteger(tok)
	}
	return decodeBoolean(tok)
}

func decodeStructure(dec *json.Decoder, s *inputShape) (map[string]any, *serializationError) {
	out := map[string]any{}
	for dec.More() {
		keyTok, _ := dec.Token()
		key, _ := keyTok.(string)
		i := -1
		for j, m := range s.members {
			if m.name == key {
				i = j
				break
			}
		}
		if i < 0 {
			var skip json.RawMessage
			_ = dec.Decode(&skip)
			continue
		}
		v, err := decodeValue(dec, s.members[i].shape)
		if err != nil {
			return nil, err
		}
		if v == nil {
			delete(out, key)
		} else {
			out[key] = v
		}
	}
	_, _ = dec.Token()
	return out, nil
}

func decodeList(dec *json.Decoder, s *inputShape) ([]any, *serializationError) {
	out := []any{}
	for dec.More() {
		v, err := decodeValue(dec, s.member)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	_, _ = dec.Token()
	return out, nil
}

func decodeMap(dec *json.Decoder, s *inputShape) (map[string]any, *serializationError) {
	out := map[string]any{}
	for dec.More() {
		keyTok, _ := dec.Token()
		key, _ := keyTok.(string)
		v, err := decodeValue(dec, s.value)
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	_, _ = dec.Token()
	return out, nil
}

func decodeString(tok json.Token) (any, *serializationError) {
	switch t := tok.(type) {
	case string:
		return t, nil
	case json.Number:
		return nil, &serializationError{message: "NUMBER_VALUE can not be converted to a String"}
	case bool:
		return nil, &serializationError{message: boolToken(t) + " can not be converted to a String"}
	}
	// unreachable: decodeValue passes only string, json.Number and bool tokens.
	return nil, &serializationError{}
}

// decodeInteger truncates a fraction toward zero and clamps to the 32-bit range.
func decodeInteger(tok json.Token) (any, *serializationError) {
	switch t := tok.(type) {
	case json.Number:
		f, _ := strconv.ParseFloat(t.String(), 64) // out of range gives ±Inf, clamped below
		return int64(math.Max(math.MinInt32, math.Min(math.MaxInt32, math.Trunc(f)))), nil
	case string:
		return nil, &serializationError{message: "STRING_VALUE can not be converted to an Integer"}
	case bool:
		return nil, &serializationError{
			message: boolToken(t) + " can not be converted to an Integer",
		}
	}
	// unreachable: decodeValue passes only string, json.Number and bool tokens.
	return nil, &serializationError{}
}

func decodeBoolean(tok json.Token) (any, *serializationError) {
	switch t := tok.(type) {
	case bool:
		return t, nil
	case json.Number:
		return nil, &serializationError{message: "NUMBER_VALUE can not be converted to a Boolean"}
	case string:
		switch strings.ToLower(t) {
		case "true", "t", "yes", "y", "1":
			return true, nil
		case "false", "f", "no", "n", "0":
			return false, nil
		}
		return nil, &serializationError{message: "STRING_VALUE can not be converted to an Boolean"}
	}
	// unreachable: decodeValue passes only string, json.Number and bool tokens.
	return nil, &serializationError{}
}

func boolToken(b bool) string {
	if b {
		return "TRUE_VALUE"
	}
	return "FALSE_VALUE"
}
