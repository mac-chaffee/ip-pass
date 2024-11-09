package main

import (
	"testing"
)

func TestGetClientCIDR(t *testing.T) {
	tests := []struct {
		name          string
		xForwardedFor string
		depth         int
		want          string
		wantErr       bool
	}{
		{
			name:          "single IPv4",
			xForwardedFor: "203.0.113.111",
			depth:         1,
			want:          "203.0.113.0/24",
			wantErr:       false,
		},
		{
			name:          "multiple IPv4 addresses with depth 1",
			xForwardedFor: "203.0.113.111, 203.0.122., 203.0.113.133",
			depth:         1,
			want:          "203.0.113.0/24",
			wantErr:       false,
		},
		{
			name:          "multiple IPv4 addresses with depth 2",
			xForwardedFor: "203.0.113.111, 192.168.1.1, 10.0.0.1",
			depth:         2,
			want:          "192.168.1.0/24",
			wantErr:       false,
		},
		{
			name:          "single IPv6",
			xForwardedFor: "2001:0db8::123",
			depth:         1,
			want:          "2001:db8::/64",
			wantErr:       false,
		},
		{
			name:          "multiple mixed addresses",
			xForwardedFor: "2001:0db8::123, 203.0.113.111",
			depth:         1,
			want:          "203.0.113.0/24",
			wantErr:       false,
		},
		{
			name:          "empty XFF",
			xForwardedFor: "",
			depth:         1,
			want:          "",
			wantErr:       true,
		},
		{
			name:          "depth too large",
			xForwardedFor: "203.0.113.111",
			depth:         2,
			want:          "",
			wantErr:       true,
		},
		{
			name:          "invalid IP",
			xForwardedFor: "not-an-ip",
			depth:         1,
			want:          "",
			wantErr:       true,
		},
		{
			name:          "malformed XFF",
			xForwardedFor: "203.0.113.111,,203.0.113.222",
			depth:         1,
			want:          "203.0.113.0/24",
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getClientCIDR(tt.xForwardedFor, tt.depth)
			if (err != nil) != tt.wantErr {
				t.Errorf("getClientCIDR() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getClientCIDR() = %v, want %v", got, tt.want)
			}
		})
	}
}
