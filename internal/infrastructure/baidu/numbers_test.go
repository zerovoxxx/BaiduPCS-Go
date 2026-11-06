package baidu

import (
	"encoding/json"
	"testing"
)

func TestP5ShareNumbersAcceptQuotedDecimalWithoutFloatConversion(t *testing.T) {
	for _, raw := range []string{`9007199254740993`, `"9007199254740993"`} {
		var value upstreamInteger
		if err := json.Unmarshal([]byte(raw), &value); err != nil || int64(value) != 9007199254740993 {
			t.Fatal("share ID lost precision", err)
		}
	}
	for _, raw := range []string{`9007199254740993.0`, `"1e3"`, `null`, `"9223372036854775808"`, `true`} {
		var value upstreamInteger
		if json.Unmarshal([]byte(raw), &value) == nil {
			t.Fatal("non-decimal or invalid integer accepted")
		}
	}
}
