package main

import "testing"

// TestValidatePublicBaseURL は PUBLIC_BASE_URL（#100 WEB-5）の設定ミスを起動時に
// 検出できることを確認する。不正な値のまま起動すると Discord がリンクボタンごと
// メッセージを拒否し、/price が使えなくなるため。
func TestValidatePublicBaseURL(t *testing.T) {
	tests := []struct {
		value   string
		wantErr bool
	}{
		{"", false},
		{"https://fx.example.run.app", false},
		{"https://fx.example.run.app/", false},
		{"http://localhost:8080", false},
		{"fx.example.run.app", true},
		{"ftp://fx.example.run.app", true},
		{"https://", true},
	}
	for _, tt := range tests {
		err := validatePublicBaseURL(tt.value)
		if (err != nil) != tt.wantErr {
			t.Errorf("validatePublicBaseURL(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
		}
	}
}
