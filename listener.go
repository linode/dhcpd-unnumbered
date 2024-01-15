package main

import (
	"errors"
	"net"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/server4"
	"github.com/linode/dhcpd-unnumbered/options"

	ll "github.com/sirupsen/logrus"
	"golang.org/x/net/ipv4"
	"golang.org/x/sys/unix"
)

// This is returned if NewListener is called with a specified interface, that's
// not a VRF.
var ErrNotVRF = errors.New("Not a VRF interface")

// Listener is the core struct
type Listener struct {
	c   *ipv4.PacketConn
	sIP net.IP
	log *ll.Entry

	// Table of the VRF, otherwise the main table
	routeTable int
}

// NewListener creates a new instance of DHCP listener. If intf is a concrete
// interface, it must be a VRF.
func NewListener(intf string) (*Listener, error) {
	s := net.UDPAddr{
		IP:   net.IPv4zero,
		Port: 67,
		Zone: intf,
	}

	// The default is the main table
	vrfTable := unix.RT_TABLE_MAIN

	if intf != "" {
		var err error
		vrfTable, err = getVRFTableIdx(intf)
		if err != nil {
			return nil, err
		}
	}

	udpConn, err := server4.NewIPv4UDPConn(s.Zone, &s)
	if err != nil {
		return nil, err
	}

	c := ipv4.NewPacketConn(udpConn)

	// When not bound to an interface, we need the information in each
	// packet to know which interface it came on
	err = c.SetControlMessage(ipv4.FlagInterface, true)
	if err != nil {
		return nil, err
	}

	// Create a sub logger that attaches the interface to each message
	logIntf := intf
	if logIntf == "" {
		logIntf = "NONE"
	}
	log := ll.NewEntry(ll.StandardLogger()).WithFields(ll.Fields{"interface": logIntf})

	return &Listener{
		c:          c,
		log:        log,
		routeTable: vrfTable,
	}, nil
}

// SetSource sets the DHCP server IP and Identified in the offer
func (l *Listener) SetSource(ip net.IP) {
	l.sIP = ip
	l.log.Infof("Sending from %s", l.sIP)
}

// Listen starts listening for incoming DHCP requests
func (l *Listener) Listen() error {
	l.log.Infof("Listen %s", l.c.LocalAddr())
	for {
		b := make([]byte, MaxDatagram)
		n, oob, peer, err := l.c.ReadFrom(b)
		if err != nil {
			// NOTE: this error will also be logged if the socket is closed when
			// the VRF disappears (which is expected).
			l.log.Errorf("Error reading from connection: %v (this error is expected if a VRF was torn down)", err)
			return err
		}
		go l.handleMsg(b[:n], oob, peer.(*net.UDPAddr))
	}
}

func (l *Listener) Close() error {
	l.log.Info("Closing Listener")
	return l.c.Close()
}

// handleMsg is triggered every time there is a DHCP request coming in. this is the main deal handling the reply
func (l *Listener) handleMsg(buf []byte, oob *ipv4.ControlMessage, _peer net.Addr) {
	ifi, err := net.InterfaceByIndex(oob.IfIndex)
	if err != nil {
		l.log.Errorf("Error getting request interface: %v", err)
		return
	}
	l.log.Debugf("Received on interface %+v", ifi)

	req, err := dhcpv4.FromBytes(buf)
	if err != nil {
		l.log.Errorf("Error parsing DHCPv4 request: %v", err)
		return
	}

	l.log.Debugf("received %s on %v", req.MessageType(), ifi.Name)
	l.log.Trace(req.Summary())

	if !(regex.Match([]byte(ifi.Name))) {
		l.log.Debugf("DHCP request on Interface %v is not accepted, ignoring", ifi.Name)
		return
	}

	if ifi.Flags&net.FlagUp != net.FlagUp {
		l.log.Debugf("DHCP request on a Interface %v, which is down. that's not right, skipping...", ifi.Name)
		return
	}

	if req.OpCode != dhcpv4.OpcodeBootRequest {
		l.log.Warnf("Unsupported opcode %d. Only BootRequest (%d) is supported", req.OpCode, dhcpv4.OpcodeBootRequest)
		return
	}

	// Load override options from file if it exists. Otherwise, fall back to
	// existing behavior of generating options internally, updating this struct
	// while doing so.
	options := &options.DHCP{}
	if *flagHostnameOverride {
		opt, err := getOptionsOverride(l.log, ifi.Name)
		if err != nil {
			l.log.Warnf("Failed to read options file: %v", err)
		} else {
			l.log.Infof("Override options read from file")
			options = opt
		}
	}

	if options.PvtIPs == nil {
		options.PvtIPs = pvtIPs
	}

	if len(options.IPv4) == 0 {
		l.log.Debugf("Now reading routes from table %d", l.routeTable)
		rts, err := getTableRoutes(oob.IfIndex, l.routeTable)

		if err != nil {
			l.log.Errorf("failed to get routes for Interface %v from table %d: %v", ifi.Name, l.routeTable, err)
			return
		}

		for _, ip := range rts {
			// Range is implicitly /24.
			ipn := net.IPNet{
				IP:   ip.IP,
				Mask: net.CIDRMask(24, 32),
			}
			options.IPv4 = append(options.IPv4, &ipn)
		}
	}
	l.log.Debugf("Routes found for Interface %v: %v", ifi.Name, options.IPv4)

	// seems like we have no host routes, not providing DHCP
	if len(options.IPv4) == 0 {
		l.log.Infof("seems like we have no host routes or override IPs, not providing DHCP")
		return
	}

	// by default set the first IP in our return slice of routes
	pickedIP := options.IPv4[0]
	for _, ipr := range options.IPv4 {
		// however, check if the client requests a specific IP *and* still owns it, if so let 'em have it, even if private
		if req.RequestedIPAddress().Equal(ipr.IP) {
			l.log.Debugf("client requested IP: %v and still owns it. so sticking to that one", req.RequestedIPAddress())
			pickedIP = ipr
			break
		}
		if req.ClientIPAddr.Equal(ipr.IP) {
			l.log.Debugf("client used IP: %v and still owns it. so sticking to that one", req.ClientIPAddr)
			pickedIP = ipr
			break
		}

		// if first IP in rts slice is a privete IP, override it with this one.
		// doing this way will allow the last private IP to stick anyway in case there is no public IP assigned to a VM
		if options.PvtIPs.Contains(pickedIP.IP) {
			l.log.Debugf("first IP was private, overriding with %v for now", ipr)
			pickedIP = ipr
		}
	}

	l.log.Debugf("Picked IP: %v", pickedIP)

	// the default gateway handed out by DHCP is the .1 of whatever /24 subnet the client gets handed out.
	// we actually don't care at all what the gw IP is, its really just to make the client's tcp/ip stack happy
	if options.Gateway == nil {
		gw := net.IPv4(pickedIP.IP[0], pickedIP.IP[1], pickedIP.IP[2], 1)
		options.Gateway = &gw
	}

	// source IP to be sending from
	sIP := l.sIP
	if sIP == nil {
		sIP = *options.Gateway
	}

	// mix DNS but mix em consistently so same IP gets the same order
	dns := mixDNS(pickedIP.IP)

	// should I generate a dynamic hostname?
	hostname := *flagHostname
	domainname := *flagDomainname

	// find dynamic hostname if feature is enabled
	if *flagDynHost {
		hostname = getDynamicHostname(pickedIP.IP)
	}

	// static hostname in a file (if exists) will supersede the dynamic hostname
	if *flagHostnameOverride {
		h, d, err := getHostnameOverride(ifi.Name)
		if err == nil {
			hostname = h
			if d != "" {
				domainname = d
			}
		} else {
			l.log.Debugf("unable to get static hostname: %v", err)
		}
	}

	// Options file takes priority over other hostname settings
	if options.Hostname == nil {
		options.Hostname = &hostname
	}

	if options.Domainname == nil {
		options.Domainname = &domainname
	}

	// lets go compile the response
	var mods []dhcpv4.Modifier
	//mods = append(mods, dhcpv4.WithBroadCast(false))
	//this should not be needed. only for dhcp relay which we don't use/do. needs to be tested
	//resp.GatewayIPAddr = gw
	mods = append(mods, dhcpv4.WithServerIP(*options.Gateway))
	mods = append(mods, dhcpv4.WithYourIP(pickedIP.IP))
	mods = append(mods, dhcpv4.WithNetmask(pickedIP.Mask))
	mods = append(mods, dhcpv4.WithRouter(*options.Gateway))
	mods = append(mods, dhcpv4.WithDNS(dns...))
	mods = append(mods, dhcpv4.WithOption(dhcpv4.OptIPAddressLeaseTime(*flagLeaseTime)))
	mods = append(mods, dhcpv4.WithOption(dhcpv4.OptHostName(*options.Hostname)))
	mods = append(mods, dhcpv4.WithOption(dhcpv4.OptDomainName(*options.Domainname)))
	mods = append(mods, dhcpv4.WithOption(dhcpv4.OptServerIdentifier(sIP)))

	if *flagBootfile != "" {
		mods = append(mods, dhcpv4.WithOption(dhcpv4.OptBootFileName(*flagBootfile)))
	}

	if options.Tftp == nil && tftp != nil {
		options.Tftp = &tftp
	}

	if options.Tftp != nil {
		mods = append(mods, dhcpv4.WithOption(dhcpv4.OptTFTPServerName(options.Tftp.String()))) // this is Option 66
	}

	switch mt := req.MessageType(); mt {
	case dhcpv4.MessageTypeDiscover:
		mods = append(mods, dhcpv4.WithMessageType(dhcpv4.MessageTypeOffer))
	case dhcpv4.MessageTypeRequest:
		mods = append(mods, dhcpv4.WithMessageType(dhcpv4.MessageTypeAck))
	default:
		l.log.Warnf("Unhandled message type: %v", mt)
		return
	}

	resp, err := dhcpv4.NewReplyFromRequest(req, mods...)
	if err != nil {
		l.log.Errorf("Failed to compile reply: %v", err)
		return
	}

	var peer *net.UDPAddr
	var peerMAC *net.HardwareAddr
	//only needed if we wanna support dhcp relay, we don't need that
	//if !req.GatewayIPAddr.IsUnspecified() {
	//	TODO: make RFC8357 compliant
	//	peer = &net.UDPAddr{IP: req.GatewayIPAddr, Port: dhcpv4.ServerPort}
	if resp.MessageType() == dhcpv4.MessageTypeNak {
		peer = &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
		peerMAC = &net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	} else if !req.ClientIPAddr.IsUnspecified() {
		peer = &net.UDPAddr{IP: req.ClientIPAddr, Port: dhcpv4.ClientPort}
		peerMAC = &req.ClientHWAddr
	} else if req.IsBroadcast() && req.Flags == 1 {
		peer = &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
		peerMAC = &net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	} else if req.Flags == 0 {
		peer = &net.UDPAddr{IP: pickedIP.IP, Port: dhcpv4.ClientPort}
		peerMAC = &req.ClientHWAddr
	} else {
		ll.Traceln("Cannot handle non-broadcast-capable unspecified peers in an RFC-compliant way. Response will be broadcast")
		peer = &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
		peerMAC = &net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	}

	ll.Infof(
		"%s to %s on %s with %v, lease %s, hostname %s.%s, tftp %s:%s",
		resp.MessageType(),
		peer.IP,
		ifi.Name,
		pickedIP,
		*flagLeaseTime,
		*options.Hostname,
		*options.Domainname,
		// This can be nil, so let the logger deference
		options.Tftp,
		*flagBootfile,
	)
	ll.Trace(resp.Summary())

	if err := sendPacket(peer, *peerMAC, *ifi, resp); err != nil {
		ll.Errorf("Write to connection %v failed: %v", peer, err)
	}
}
