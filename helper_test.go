package main

import (
	"fmt"
	"net"
	"testing"
)

func IPsEqual(a, b []net.IP) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if !v.Equal(b[i]) {
			return false
		}
	}
	return true
}

func TestMixDNS(t *testing.T) {
	myDNS.Set("1.1.1.1")
	myDNS.Set("2.2.2.2")
	myDNS.Set("3.3.3.3")
	myDNS.Set("4.4.4.4")

	tests := []struct {
		input net.IP
		want  []net.IP
	}{
		{
			net.IPv4(1, 1, 1, 0),
			[]net.IP{net.IPv4(1, 1, 1, 1), net.IPv4(2, 2, 2, 2), net.IPv4(3, 3, 3, 3), net.IPv4(4, 4, 4, 4)},
		},
		{
			net.IPv4(1, 1, 1, 1),
			[]net.IP{net.IPv4(2, 2, 2, 2), net.IPv4(3, 3, 3, 3), net.IPv4(4, 4, 4, 4), net.IPv4(1, 1, 1, 1)},
		},
		{
			net.IPv4(3, 3, 3, 2),
			[]net.IP{net.IPv4(3, 3, 3, 3), net.IPv4(4, 4, 4, 4), net.IPv4(1, 1, 1, 1), net.IPv4(2, 2, 2, 2)},
		},
		{
			net.IPv4(3, 3, 3, 3),
			[]net.IP{net.IPv4(4, 4, 4, 4), net.IPv4(1, 1, 1, 1), net.IPv4(2, 2, 2, 2), net.IPv4(3, 3, 3, 3)},
		},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("Test: %s", tc.input), func(t *testing.T) {
			out := mixDNS(tc.input)
			if !IPsEqual(out, tc.want) {
				t.Errorf("Failed ! got %s want %s", out, tc.want)
			} else {
				t.Logf("Success !")
			}
		})
	}
}

func TestGetDynamicHostname(t *testing.T) {
	tests := []struct {
		input net.IP
		want  string
	}{
		{net.IPv4(1, 1, 1, 1), "1-1-1-1"},
		{net.IPv4(2, 2, 2, 2), "2-2-2-2"},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("Test: %s", tc.input), func(t *testing.T) {
			if h := getDynamicHostname(tc.input); h != tc.want {
				t.Errorf("Failed ! got %s want %s", h, tc.want)
			} else {
				t.Logf("Success !")
			}
		})
	}
}
