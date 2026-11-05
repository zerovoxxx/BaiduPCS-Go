package session

import "encoding/json"

type Resource struct {
	Kind, ID, Owner string
	Revision        uint64
	Expires         int64
	Data            json.RawMessage
}
