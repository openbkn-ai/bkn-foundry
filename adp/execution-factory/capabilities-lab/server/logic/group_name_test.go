package logic

import "testing"

func TestDeriveAutoGroupName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://uapis.cn", "uapis_cn_group"},
		{"http://api.example.com/v1", "api_example_com_group"},
		{"http://ef-oss-mock:8080", "ef_oss_mock_group"},
		{"not-a-url", "default_http_group"},
	}

	for _, tt := range tests {
		if got := DeriveAutoGroupName(tt.input); got != tt.want {
			t.Fatalf("DeriveAutoGroupName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
