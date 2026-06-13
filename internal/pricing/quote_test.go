package pricing

import "testing"

func TestSide_String(t *testing.T) {
	cases := []struct {
		side Side
		want string
	}{
		{Buy, "BUY"},
		{Sell, "SELL"},
		{Side(0), sideUnknown},  // zero value
		{Side(99), sideUnknown}, // out of range
	}
	for _, c := range cases {
		if got := c.side.String(); got != c.want {
			t.Errorf("Side(%d).String(): got %q, want %q", int(c.side), got, c.want)
		}
	}
}
