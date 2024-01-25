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
	"golang.org/x/sys/unix"
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

// Returns the table id of VRF `ifName`, or an error if ifName is not a VRF.
func getVRFTableIdx(ifName string) (int, error) {
	nlh, err := netlink.NewHandle()
	defer nlh.Delete()
	if err != nil {
		return 0, fmt.Errorf("unable to hook into netlink: %v", err)
	}

	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return 0, fmt.Errorf("unable to get link info: %v", err)
	}

	// We need a type assertion to access the Table ID
	vrf, ok := link.(*netlink.Vrf)
	if !ok {
		return 0, ErrNotVRF
	}

	return int(vrf.Table), nil
}

// Returns routes for the given interface in the given table.
func getTableRoutes(ifidx int, table int) ([]*net.IPNet, error) {
	nlh, err := netlink.NewHandle()
	defer nlh.Delete()
	if err != nil {
		return nil, fmt.Errorf("unable to hook into netlink: %v", err)
	}

	routeFilter := &netlink.Route{
		Table: table,
	}

	ro, err := nlh.RouteListFiltered(unix.AF_INET, routeFilter, netlink.RT_FILTER_TABLE)
	if err != nil {
		return nil, fmt.Errorf("unable to get routes: %v", err)
	}
	var r []*net.IPNet
	for _, d := range ro {
		ll.Debug("Route ", d.String())
		if d.LinkIndex != ifidx || d.Dst == nil {
			continue
		}

		m, l := d.Dst.Mask.Size()
		if m == 32 && l == 32 {
			r = append(r, d.Dst)
		}
	}
	return r, nil
}

// Determine the gateway based on IP and Netmask.
func gatewayFromIP(ipnet *net.IPNet) *net.IP {
	// Apply netmask to IP, then increment last octet by one
	gw := ipnet.IP.Mask(ipnet.Mask)
	gw[len(gw)-1] += 1

	return &gw
}
