package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"regexp"
	"sync"
	"time"

	"github.com/linode/dhcpd-unnumbered/monitor"
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
	tftp   net.IP

	flagLeaseTime = flag.Duration("leasetime", (30 * time.Minute), "DHCP lease time.")
	flagTapRegex  = flag.String("regex", "tap.*_0", "regex to match interfaces.")
	flagVrfRegex  = flag.String("bind", "", "additionally bind VRF interfaces matching regex.")
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
	flagTftpIP := flag.String("tftp", "", "tftp srv to offer in DHCP replies")
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

	tftp = net.ParseIP(*flagTftpIP)
	if tftp != nil {
		ll.Infof("using %s as tftp", tftp)
	}

	_, pvtIPs, err = net.ParseCIDR(*flagPvtIPs)
	if err != nil {
		ll.Fatalf("unable to parse private IP range: %v", err)
	}
	ll.Infof("ignoring private IPs from %v", pvtIPs)

	if len(myDNS) == 0 {
		err := myDNS.Set("8.8.8.8")
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

	wg := sync.WaitGroup{}

	// Listen across interfaces with a single socket
	s, err := NewListener("")
	s.SetSource(sIP)
	wg.Add(1)
	go func() {
		if err := s.Listen(); err != nil {
			ll.Fatalf("Unexpected server exit: %s", err)
		}
		wg.Done()
	}()

	// Dynamically bind interfaces matching flagBindRegex if set
	if *flagVrfRegex != "" {
		ll.Infof("Will also bind VRFs matching %s", *flagVrfRegex)
		regex := regexp.MustCompile(*flagVrfRegex)
		linkch := make(chan monitor.Event, 5)
		mon := monitor.NewNetlinkMonitor(linkch, regex)

		// Start monitor
		go func() {
			err := mon.Listen()
			if err != nil {
				ll.Fatalf("Netlink monitor unexpected exit: %s", err)
			}
		}()

		wg.Add(1)

		// Watch for events and generate listeners
		go func() {
			defer wg.Done()
			listeners := make(map[string]*Listener)

			for {
				event, ok := <-linkch
				if !ok {
					ll.Info("Monitor channel closed")
					break
				}
				switch event.Type {
				case monitor.LinkUp:
					s, err := NewListener(event.Interface)
					if err != nil {
						if errors.Is(err, ErrNotVRF) {
							// Just in case the regex matches an interface
							// that's not a VRF, handle it gracefully.
							ll.Infof("Won't bind %s as it's not a VRF", event.Interface)
						} else {
							ll.Warningf("Failed to bind %s: %v", event.Interface, err)
						}
						// Add a sentinel to avoid warning when the interface disappears
						listeners[event.Interface] = nil
						continue
					}
					s.SetSource(sIP)
					listeners[event.Interface] = s
					go s.Listen()
				case monitor.LinkDown:
					s, ok := listeners[event.Interface]
					if !ok {
						// This should not be possible
						ll.Warningf("Interface %s without listener doing down", event.Interface)
						continue
					}
					delete(listeners, event.Interface)
					if s != nil {
						s.Close()
					}
				}
			}
			mon.Close()
		}()
	}

	wg.Wait()
	ll.Info("closing...")
}
