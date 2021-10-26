package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"regexp"
	"sync"
	"time"

	ll "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

const (
	// MaxDatagram is the maximum length of message that can be received.
	MaxDatagram = 1 << 16
)

var (
	regex          *regexp.Regexp
	pvtIPs         *net.IPNet
	myDNS          listIP
	flagLogLevel   = flag.String("loglevel", "info", fmt.Sprintf("Log level. One of %v", getLogLevels()))
	flagLeaseTime  = flag.Duration("leasetime", (30 * time.Minute), "DHCP lease time.")
	flagTapRegex   = flag.String("regex", "tap.*_0", "regex to match interfaces.")
	flagPvtIPs     = flag.String("pvtcidr", "192.168.0.0/16", "private IP range. this IP CIDR will not be used for DHCP leases")
	flagDynHost    = flag.Bool("dynhost", false, "dynamic hostname generated from IP.domainname")
	flagHostname   = flag.String("hostname", "localhost", "hostname to be handed out in dhcp offeres")
	flagDomainname = flag.String("domainname", "localdomain", "domainname to be handed out in dhcp offeres")
	flagBootfile   = flag.String("bootfile", "", "boot file to offer in DHCP replies")
	flagTftpIP     = flag.String("tftp", "", "tftp srv to offer in DHCP replies")

	// XXX: performance-wise, Pool may or may not be good (see https://github.com/golang/go/issues/23199)
	// Interface is good for what we want. Maybe "just" trust the GC and we'll be fine ?
	bufpool = sync.Pool{New: func() interface{} { r := make([]byte, MaxDatagram); return &r }}
)

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

var logLevels = map[string]func(){
	"none":    func() { ll.SetOutput(ioutil.Discard) },
	"trace":   func() { ll.SetLevel(ll.TraceLevel) },
	"debug":   func() { ll.SetLevel(ll.DebugLevel) },
	"info":    func() { ll.SetLevel(ll.InfoLevel) },
	"warning": func() { ll.SetLevel(ll.WarnLevel) },
	"error":   func() { ll.SetLevel(ll.ErrorLevel) },
	"fatal":   func() { ll.SetLevel(ll.FatalLevel) },
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

func main() {
	flag.Var(&myDNS, "dns", "dns server to use in DHCP offer, option can be used multiple times for more than 1 server")
	flag.Parse()

	ll.SetFormatter(&ll.TextFormatter{
		FullTimestamp: true,
		PadLevelText:  true,
	})

	fn, ok := logLevels[*flagLogLevel]
	if !ok {
		ll.Fatalf("Invalid log level '%s'. Valid log levels are %v", *flagLogLevel, getLogLevels())
	}
	fn()

	ll.Infof("Setting log level to '%s'", ll.GetLevel())
	if *flagDynHost {
		ll.Infof("Using dynamic hostnames based on IP")
	} else {
		ll.Infof("Sending %s for hostname", *flagHostname)
	}

	ll.Infof("Sending %s for domainname", *flagDomainname)

	var err error
	regex, err = regexp.Compile(*flagTapRegex)
	if err != nil {
		ll.Fatalf("unable to parse interface regex: %v", err)
	}

	ll.Infof("Handling Interfaces matching '%s'", regex.String())

	_, pvtIPs, err = net.ParseCIDR(*flagPvtIPs)
	if err != nil {
		ll.Fatalf("unable to parse private IP range: %v", err)
	}
	ll.Infof("ignoring private IPs from %v", pvtIPs)

	if len(myDNS) == 0 {
		err := myDNS.Set("1.1.1.1")
		if err != nil {
			ll.Fatalln("failed to set default DNS server")
		}
		ll.Infof("no DNS provided, using defaults")
	}
	ll.Infof("using DNS %v", myDNS)

	// start server
	srv, err := Start()
	if err != nil {
		ll.Fatal(err)
	}
	if err := srv.Wait(); err != nil {
		ll.Info(err)
	}
}
