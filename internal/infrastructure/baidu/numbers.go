package baidu

import (
	"encoding/json"
	"errors"
	"strconv"
)

// Some Pan share responses encode fs_id (and size/time fields) as strings.
// Parse decimal integers directly, preserving IDs larger than 2^53.
type upstreamInteger int64

func (n *upstreamInteger) UnmarshalJSON(raw []byte) error {
	text := string(raw)
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return errors.New("invalid upstream integer")
		}
	}
	v, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return errors.New("invalid upstream integer")
	}
	*n = upstreamInteger(v)
	return nil
}
