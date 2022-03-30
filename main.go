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

	flagLeaseTime = flag.Duration("leasetime", (30 * time.Minute), "DHCP lease time.")
	flagTapRegex  = flag.String("regex", "tap.*_0", "regex to match interfaces.")
	flagPvtIPs    = flag.String(
		"pvtcidr",
		"192.168.0.0/16",
		"private IP range. IPs in this range don't quallify for initial DHCP offeres, even if assigned to requesting tap",
	)
	flagDynHost          = flag.Bool("dynamic-hostname", false, "dynamic hostname generated from {IP/./-}.domainname")
	flagHostnameOverride = flag.Bool(
		"hostname-override",
		false,
		"read hostname from <override-file-prefix + receiving interface name>, i.e. /var/lib/dhcpd-unnumbered/hostname.tap.XXXX_0",
	)
	flagHostnamePath = flag.String(
		"override-file-prefix",
		"/var/lib/dhcpd-unnumbered/hostname.",
		"path and file-prefix where to find hostname override files. I will concat this string with interface name request was received on to find a hostname override, if the file is missing we fall back to either dynamic or static hostname as appropriate",
	)
	flagHostname = flag.String(
		"hostname",
		"localhost",
		"static hostname to be handed out in dhcp offeres, is ignored if dynamic-hostname is enabled",
	)
	flagDomainname = flag.String("domainname", "localdomain", "domainname to be handed out in dhcp offeres")
	flagBootfile   = flag.String("bootfile", "", "boot file to offer in DHCP replies")
	flagTftpIP     = flag.String("tftp", "", "tftp srv to offer in DHCP replies")

	logLevels = map[string]func(){
		"none":    func() { ll.SetOutput(ioutil.Discard) },
		"trace":   func() { ll.SetLevel(ll.TraceLevel) },
		"debug":   func() { ll.SetLevel(ll.DebugLevel) },
		"info":    func() { ll.SetLevel(ll.InfoLevel) },
		"warning": func() { ll.SetLevel(ll.WarnLevel) },
		"error":   func() { ll.SetLevel(ll.ErrorLevel) },
		"fatal":   func() { ll.SetLevel(ll.FatalLevel) },
	}
)

func main() {
	flagLogLevel := flag.String("loglevel", "info", fmt.Sprintf("Log level. One of %v", getLogLevels()))
	flag.Var(&myDNS, "dns", "dns server to use in DHCP offer, option can be used multiple times for more than 1 server")
	flag.Parse()

	ll.SetFormatter(&ll.TextFormatter{
		FullTimestamp: true,
		PadLevelText:  true,
	})

	loglevel, ok := logLevels[*flagLogLevel]
	if !ok {
		ll.Fatalf("Invalid log level '%s'. Valid log levels are %v", *flagLogLevel, getLogLevels())
	}
	loglevel()

	ll.Infof("Setting log level to '%s'", ll.GetLevel())

	if *flagDynHost {
		ll.Infof("Dynamic hostnames based on IP enabled")
	}

	if *flagHostnameOverride {
		ll.Infof("Hostname override enabled from %s", *flagHostnamePath)
	}

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

	sIP, err := getSourceIP()
	if err != nil {
		ll.Fatalf("unable to get source IP to be used: %v", err)
	}

	// start server
	s, err := NewListener()
	if err != nil {
		ll.Fatal(err)
	}
	s.SetSource(sIP)
	if err := s.Listen(); err != nil {
		ll.Fatalf("Unexpected server exit: %s", err)
	}
	ll.Info("closing...")
}
