package options

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"

	ll "github.com/sirupsen/logrus"
)

// This struct defines thn options override file as it exists on disk.  Options
// in this file take precedence over command line arguments and/or built in
// settings. If a setting in this file is missing or empty, it count as not
// set. Likewise if it cannot be parsed (this will result in a warning being
// logged).
type dhcpJSON struct {
	IPv4       []string // Address/Prefix. If not specified, Prefix is implicitly /24
	Hostname   string
	Domainname string
	Gateway    string // Address
	PvtIPs     string // Address/Prefix
	Tftp       string
}

// This struct represents the parsed DHCP options as used internally.
type DHCP struct {
	// If empty, these aren't set
	IPv4 []*net.IPNet

	// If nil, these aren't set.
	Hostname   *string
	Domainname *string
	Gateway    *net.IP
	PvtIPs     *net.IPNet
	Tftp       *net.IP
}

// Load and parse OptionsJSON to Options. Returns an error if the file cannot be
// parsed, but individual items that fail to be parsed are simply discarded. A
// missing file is not considered an error.
func Load(log *ll.Entry, filepath string) (*DHCP, error) {
	fh, err := os.Open(filepath)
	if err != nil {
		// A missing file is expected so this isn't considered an
		// error. Filename is contained in the error we receive.
		ll.Infof("Failed to open options file: %v", err)
		return &DHCP{}, nil
	}
	defer fh.Close()

	bytes, err := io.ReadAll(fh)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %v", err)
	}

	return parse(log, bytes)
}

// Parse a string as a CIDR or as an IP with implicit /24 prefix.
func parseIP(log *ll.Entry, ipstr string) (*net.IPNet, error) {
	// Parse as CIDR
	ip, ipnet, err := net.ParseCIDR(ipstr)
	if err == nil {
		log.Infof("Parsed successfully as CIDR: %s", ipstr)
		// Use IPNet to store the actual IP and netmask
		ret := net.IPNet{
			IP:   ip,
			Mask: ipnet.Mask,
		}
		return &ret, nil
	}

	// Fall back to parsing as IP
	ip = net.ParseIP(ipstr)
	if ip == nil {
		return nil, fmt.Errorf("failed to parse %s as IP", ipstr)
	}

	ret := net.IPNet{
		IP:   ip,
		Mask: net.CIDRMask(24, 32),
	}
	return &ret, nil
}

func parse(log *ll.Entry, bytes []byte) (*DHCP, error) {
	options := &DHCP{}
	var onDisk dhcpJSON
	err := json.Unmarshal(bytes, &onDisk)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal: %v", err)
	}

	for _, ipstr := range onDisk.IPv4 {
		ipnet, err := parseIP(log, ipstr)
		if err == nil {
			options.IPv4 = append(options.IPv4, ipnet)
		} else {
			log.Warnf("Failed to parse IP=%s, it will be ignored: %v", ipstr, err)
		}
	}

	if onDisk.Hostname != "" {
		options.Hostname = &onDisk.Hostname
	}

	if onDisk.Domainname != "" {
		options.Domainname = &onDisk.Domainname
	}

	if onDisk.Gateway != "" {
		// ParseIP returns nil on parse failure!
		gw := net.ParseIP(onDisk.Gateway)
		if gw == nil {
			ll.Warnf("Failed to parse Gateway=%s, it will be ignored", onDisk.Gateway)
		} else {
			options.Gateway = &gw
		}
	}

	if onDisk.PvtIPs != "" {
		_, pvtIPs, err := net.ParseCIDR(onDisk.PvtIPs)
		if err != nil {
			ll.Warnf("Failed to parse PvtIPs=%s, it will be ignored", onDisk.PvtIPs)
		} else {
			options.PvtIPs = pvtIPs
		}
	}

	if onDisk.Tftp != "" {
		// ParseIP returns nil on parse failure!
		tftp := net.ParseIP(onDisk.Tftp)
		if tftp == nil {
			ll.Warnf("Failed to parse Tftp=%s, it will be ignored", onDisk.Tftp)
		} else {
			options.Tftp = &tftp
		}
	}

	return options, nil
}
