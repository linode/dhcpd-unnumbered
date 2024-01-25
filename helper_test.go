package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
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

func overrideFile(t *testing.T, ifName, hostname string) {
	*flagHostnamePath = "/tmp/"

	s := []byte(hostname)
	ioutil.WriteFile(*flagHostnamePath+ifName, s, 0644)

	t.Cleanup(func() {
		os.Remove(*flagHostnamePath + ifName)
	})
}

func TestGetHostnameOverride(t *testing.T) {
	tests := []struct {
		ifName     string
		wantHost   string
		wantDomain string
	}{
		{"tap.123456_0", "", ""},
		{"tap.321321_0", "test", "domain.com"},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("Test: %s", tc.ifName), func(t *testing.T) {

			overrideFile(t, tc.ifName, tc.wantHost+"."+tc.wantDomain)

			h, d, e := getHostnameOverride(tc.ifName)

			if h != tc.wantHost {
				t.Errorf("Failed ! got %s want %s", h, tc.wantHost)
			} else if d != tc.wantDomain {
				t.Errorf("Failed ! got %s want %s", d, tc.wantDomain)
			} else if e != nil {
				t.Errorf("Failed ! got %s want nil", e)
			} else {
				t.Logf("Success !")
			}
		})
	}
}

func TestGatewayFromIP(t *testing.T) {
	tests := []struct {
		input  string
		output string
	}{
		{"192.168.11.11/23", "192.168.10.1"},
		{"192.168.14.15/24", "192.168.14.1"},
		{"192.168.11.40/27", "192.168.11.33"},
	}

	for _, test := range tests {
		ip, ipnet, err := net.ParseCIDR(test.input)
		assert.Nil(t, err, "Failed to parse test data!")
		chosenIP := net.IPNet{
			IP:   ip,
			Mask: ipnet.Mask,
		}
		gw := gatewayFromIP(&chosenIP)

		assert.Equal(t, test.output, gw.String(), "Unexpected gateway")
	}
}
