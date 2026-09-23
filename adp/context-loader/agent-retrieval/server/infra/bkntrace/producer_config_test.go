package bkntrace

import "testing"

func TestPositiveEnvInt(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  int
		bad   bool
	}{
		{name: "default", value: "", want: 0},
		{name: "positive", value: "4096", want: 4096},
		{name: "zero", value: "0", bad: true},
		{name: "negative", value: "-1", bad: true},
		{name: "malformed", value: "many", bad: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("BKN_TRACE_TEST_LIMIT", test.value)
			got, err := positiveEnvInt("BKN_TRACE_TEST_LIMIT")
			if test.bad {
				if err == nil {
					t.Fatalf("positiveEnvInt(%q) unexpectedly accepted %q", "BKN_TRACE_TEST_LIMIT", test.value)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("positiveEnvInt=%d, err=%v; want %d", got, err, test.want)
			}
		})
	}
}
