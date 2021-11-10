package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"regexp"
	"time"

	ll "github.com/sirupsen/logrus"
)

const (
	// MaxDatagram is the maximum length of message that can be received.
	MaxDatagram = 1 << 16
)

var (
	regex  *regexp.Regexp
	pvtIPs *net.IPNet
	myDNS  listIP

	flagLogLevel  = flag.String("loglevel", "info", fmt.Sprintf("Log level. One of %v", getLogLevels()))
	flagLeaseTime = flag.Duration("leasetime", (30 * time.Minute), "DHCP lease time.")
	flagTapRegex  = flag.String("regex", "tap.*_0", "regex to match interfaces.")
	flagPvtIPs    = flag.String(
		"pvtcidr",
		"192.168.0.0/16",
		"private IP range. this IP CIDR will not be used for DHCP leases",
	)
	flagDynHost          = flag.Bool("dynamic-hostname", false, "dynamic hostname generated from IP.domainname")
	flagHostnameOverride = flag.Bool(
		"hostname-override",
		false,
		"read hostname override from hostname-pathpath/hostname.interfacename",
	)
	flagHostnamePath = flag.String(
		"hostname-path-prefix",
		"",
		"path/prefix where to find hostname override files. I will look in path+interfaceName for a name to override",
	)
	flagHostname   = flag.String("hostname", "localhost", "hostname to be handed out in dhcp offeres")
	flagDomainname = flag.String("domainname", "localdomain", "domainname to be handed out in dhcp offeres")
	flagBootfile   = flag.String("bootfile", "", "boot file to offer in DHCP replies")
	flagTftpIP     = flag.String("tftp", "", "tftp srv to offer in DHCP replies")
)

var logLevels = map[string]func(){
	"none":    func() { ll.SetOutput(ioutil.Discard) },
	"trace":   func() { ll.SetLevel(ll.TraceLevel) },
	"debug":   func() { ll.SetLevel(ll.DebugLevel) },
	"info":    func() { ll.SetLevel(ll.InfoLevel) },
	"warning": func() { ll.SetLevel(ll.WarnLevel) },
	"error":   func() { ll.SetLevel(ll.ErrorLevel) },
	"fatal":   func() { ll.SetLevel(ll.FatalLevel) },
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

	//ll.Infof("Sending %s for domainname", *flagDomainname)

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
