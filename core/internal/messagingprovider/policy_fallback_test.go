package messagingprovider

import "testing"

// policy_fallback_test.go, HH-202: a policy variable that falls back to its default must say so.
//
// SMS_RATE_PER_MIN=0 used to leave the cap at 30 with no trace, so an operator who typed 0 meaning
// "pause texts" during a carrier incident saw sends continue. Zero is still refused (no value turns
// the policy off); what changes is that every fallback is recorded for the boot path to log.

func TestPolicyFromEnv_RecordsEveryFallback(t *testing.T) {
	type want struct {
		applied  int
		fallback bool
	}
	vars := []struct {
		name string
		def  int
		read func(p *SendPolicy) int
	}{
		{"SMS_RATE_PER_MIN", DefaultRatePerMinute, func(p *SendPolicy) int { return p.rate }},
		{"SMS_WINDOW_START_MIN", DefaultQuietStartMin, func(p *SendPolicy) int { return p.Window().StartMin }},
		{"SMS_WINDOW_END_MIN", DefaultQuietEndMin, func(p *SendPolicy) int { return p.Window().EndMin }},
	}
	// 7 is valid for the rate; for the window variables 7 is a valid minute and is applied as read
	// (start 7 with end 1200, or start 480 with end 7, is an inverted-but-legal wrap window).
	cases := []struct {
		raw string
		per map[string]want
	}{
		{"", nil},
		{"0", map[string]want{
			"SMS_RATE_PER_MIN": {DefaultRatePerMinute, true},
			// 0 is a legal minute for the window: midnight.
			"SMS_WINDOW_START_MIN": {0, false},
			"SMS_WINDOW_END_MIN":   {0, false},
		}},
		{"-5", map[string]want{
			"SMS_RATE_PER_MIN":     {DefaultRatePerMinute, true},
			"SMS_WINDOW_START_MIN": {DefaultQuietStartMin, true},
			"SMS_WINDOW_END_MIN":   {DefaultQuietEndMin, true},
		}},
		{"abc", map[string]want{
			"SMS_RATE_PER_MIN":     {DefaultRatePerMinute, true},
			"SMS_WINDOW_START_MIN": {DefaultQuietStartMin, true},
			"SMS_WINDOW_END_MIN":   {DefaultQuietEndMin, true},
		}},
		{"7", map[string]want{
			"SMS_RATE_PER_MIN":     {7, false},
			"SMS_WINDOW_START_MIN": {7, false},
			"SMS_WINDOW_END_MIN":   {7, false},
		}},
	}
	for _, v := range vars {
		for _, c := range cases {
			t.Run(v.name+"="+c.raw, func(t *testing.T) {
				for _, k := range []string{"SMS_RATE_PER_MIN", "SMS_WINDOW_START_MIN", "SMS_WINDOW_END_MIN"} {
					t.Setenv(k, "")
				}
				t.Setenv(v.name, c.raw)
				p := PolicyFromEnv()

				applied, fell := v.def, false
				if w, ok := c.per[v.name]; ok {
					applied, fell = w.applied, w.fallback
				}
				if got := v.read(p); got != applied {
					t.Fatalf("%s=%q applied %d, want %d", v.name, c.raw, got, applied)
				}
				fbs := p.Fallbacks()
				if !fell {
					if len(fbs) != 0 {
						t.Fatalf("%s=%q recorded %+v, want no fallback", v.name, c.raw, fbs)
					}
					return
				}
				if len(fbs) != 1 {
					t.Fatalf("%s=%q recorded %+v, want exactly one fallback", v.name, c.raw, fbs)
				}
				f := fbs[0]
				if f.Var != v.name || f.Value != c.raw || f.Default != v.def {
					t.Fatalf("%s=%q recorded %+v, want var %s value %q default %d", v.name, c.raw, f, v.name, c.raw, v.def)
				}
			})
		}
	}
}

// A window pair that NewQuietHours replaces as a whole (out of range, or start equal to end) is a
// fallback of both variables, including the one whose own value was fine.
func TestPolicyFromEnv_RecordsWindowPairReplacement(t *testing.T) {
	for _, tc := range []struct{ start, end string }{
		{"360", "9999"}, // end out of range, the valid start is lost with it
		{"600", "600"},  // degenerate
		{"0", "0"},      // degenerate
	} {
		t.Setenv("SMS_RATE_PER_MIN", "")
		t.Setenv("SMS_WINDOW_START_MIN", tc.start)
		t.Setenv("SMS_WINDOW_END_MIN", tc.end)
		p := PolicyFromEnv()
		if w := p.Window(); w.StartMin != DefaultQuietStartMin || w.EndMin != DefaultQuietEndMin {
			t.Fatalf("%s-%s applied %d-%d, want the house rule", tc.start, tc.end, w.StartMin, w.EndMin)
		}
		got := map[string]string{}
		for _, f := range p.Fallbacks() {
			got[f.Var] = f.Value
		}
		if got["SMS_WINDOW_START_MIN"] != tc.start || got["SMS_WINDOW_END_MIN"] != tc.end || len(got) != 2 {
			t.Fatalf("%s-%s recorded %+v, want both window variables with the values read", tc.start, tc.end, p.Fallbacks())
		}
	}
}
