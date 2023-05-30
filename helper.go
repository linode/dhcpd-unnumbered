package main

import (
	"fmt"
	"net"
	"os"
	"path"
	"strings"

	"github.com/linode/dhcpd-unnumbered/options"
	ll "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

func getSourceIP() (net.IP, error) {
	lo, err := net.InterfaceByName("lo")
	if err != nil {
		return nil, err
	}
	loAddrs, err := lo.Addrs()
	if err != nil {
		return nil, err
	}
	var sIP net.IP
	for _, addr := range loAddrs {
		switch v := addr.(type) {
		case *net.IPNet:
			sIP = v.IP
		case *net.IPAddr:
			sIP = v.IP
		default:
			continue
		}

		if sIP.IsLoopback() || sIP.IsLinkLocalUnicast() || sIP.To4() == nil {
			continue
		}
		break
	}

	if sIP.To4() != nil {
		return sIP, nil
	}

	return nil, nil
}

// getDynamicHostname will generate hostname from IP and predefined domainname
func getDynamicHostname(ip net.IP) string {
	return strings.ReplaceAll(ip.String(), ".", "-")
}

// getHostnameOverride returns a hoostname (and if applicable) a domainname read from a static file based on path+ifName
func getHostnameOverride(ifName string) (string, string, error) {
	h, err := os.ReadFile(*flagHostnamePath + ifName)
	if err != nil {
		return "", "", err
	}
	s := strings.SplitN(strings.TrimSpace(string(h)), ".", 2)
	if len(s) > 1 {
		return s[0], s[1], nil
	}
	return s[0], "", nil
}

// getHostnameOverride returns override options read from a static file based on
// path+ifName.options
func getOptionsOverride(log *ll.Entry, ifName string) (*options.DHCP, error) {
	fullpath := path.Join(*flagHostnamePath, ifName+".options")
	return options.Load(log, fullpath)
}

// mixDNS sorts dns servers in a sudo-random way (the provided IP should always get back the same sequence of DNS)
func mixDNS(ip net.IP) []net.IP {
	l := len(myDNS)
	// just mod over last octet of IP as it provides the highest diversity without causing much complexity
	m := int(ip[len(ip)-1]) % l
	var mix []net.IP

	for i := 0; i < l; i++ {
		if i+m >= l {
			m = m - l
		}
		mix = append(mix, myDNS[i+m])
	}

	return mix
}

type listIP []net.IP

func (ip *listIP) String() string {
	var s string
	for _, i := range *ip {
		s = s + " " + i.String()
	}
	return s
}

func (ip *listIP) Set(value string) error {
	newIP := net.ParseIP(value)
	if newIP != nil {
		*ip = append(*ip, newIP)
		return nil
	}
	return fmt.Errorf("invalid ip: %v", value)
}

func getLogLevels() []string {
	var levels []string
	for k := range logLevels {
		levels = append(levels, k)
	}
	return levels
}

func getHostRoutesIpv4(ifName string) ([]*net.IPNet, error) {
	nlh, err := netlink.NewHandle()
	defer nlh.Delete()
	if err != nil {
		return nil, fmt.Errorf("unable to hook into netlink: %v", err)
	}

	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return nil, fmt.Errorf("unable to get link info: %v", err)
	}

	ro, err := nlh.RouteList(link, 4)
	if err != nil {
		return nil, fmt.Errorf("unable to get routes: %v", err)
	}
	var r []*net.IPNet
	for _, d := range ro {
		m, l := d.Dst.Mask.Size()
		if m == 32 && l == 32 {
			r = append(r, d.Dst)
		}
	}
	return r, nil
}
