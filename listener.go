package main

import (
	"net"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/server4"

	ll "github.com/sirupsen/logrus"
	"golang.org/x/net/ipv4"
)

// Listener is the core struct
type Listener struct {
	c *ipv4.PacketConn
}

// NewListener creates a new instance of DHCP listener
func NewListener() (*Listener, error) {
	s := net.UDPAddr{
		IP:   net.IPv4zero,
		Port: 67,
		Zone: "",
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

	return &Listener{c: c}, nil
}

// Listen starts listening for incoming DHCP requests
func (l *Listener) Listen() error {
	ll.Infof("Listen %s", l.c.LocalAddr())
	for {
		b := make([]byte, MaxDatagram)
		n, oob, peer, err := l.c.ReadFrom(b)
		if err != nil {
			ll.Errorf("Error reading from connection: %v", err)
			return err
		}
		go l.handleMsg(b[:n], oob, peer.(*net.UDPAddr))
	}
}

// handleMsg is triggered every time there is a DHCP request coming in. this is the main deal handling the reply
func (l *Listener) handleMsg(buf []byte, oob *ipv4.ControlMessage, _peer net.Addr) {
	ifi, err := net.InterfaceByIndex(oob.IfIndex)
	if err != nil {
		ll.Errorf("Error getting request interface: %v", err)
		return
	}

	req, err := dhcpv4.FromBytes(buf)
	if err != nil {
		ll.Errorf("Error parsing DHCPv4 request: %v", err)
		return
	}

	ll.Debugf("received %s on %v", req.MessageType(), ifi.Name)
	ll.Trace(req.Summary())

	if !(regex.Match([]byte(ifi.Name))) {
		ll.Warnf("DHCP request on Interface %v is not accepted, ignoring", ifi.Name)
		return
	}

	if ifi.Flags&net.FlagUp != net.FlagUp {
		ll.Warnf("DHCP request on a Interface %v, which is down. that's not right, skipping...", ifi.Name)
		return
	}

	if req.OpCode != dhcpv4.OpcodeBootRequest {
		ll.Warnf("Unsupported opcode %d. Only BootRequest (%d) is supported", req.OpCode, dhcpv4.OpcodeBootRequest)
		return
	}

	rts, err := getHostRoutesIpv4(ifi.Name)
	if err != nil {
		ll.Errorf("failed to get routes for Interface %v: %v", ifi.Name, err)
		return
	}
	ll.Debugf("Routes found for Interface %v: %v", ifi.Name, rts)

	// seems like we have no host routes, not providing DHCP
	if rts == nil {
		ll.Infof("seems like we have no host routes, not providing DHCP")
		return
	}

	// by default set the first IP in our return slice of routes
	pickedIP := rts[0].IP
	for _, ip := range rts {
		// however, check if the client requests a specific IP *and* still owns it, if so let 'em have it, even if private
		if req.RequestedIPAddress().Equal(ip.IP) {
			ll.Debugf("client requested IP: %v and still owns it. so sticking to that one", req.RequestedIPAddress())
			pickedIP = req.RequestedIPAddress()
			break
		}
		if req.ClientIPAddr.Equal(ip.IP) {
			ll.Debugf("client used IP: %v and still owns it. so sticking to that one", req.ClientIPAddr)
			pickedIP = req.ClientIPAddr
			break
		}

		// if first IP in rts slice is a privete IP, overrise it with this one.
		// doing this way will allow the last private IP to stick anyway in case there is no public IP assigned to a VM
		if pvtIPs.Contains(pickedIP) {
			ll.Debugf("first IP was private, overriding with %v for now", ip)
			pickedIP = ip.IP
		}
	}

	ll.Debugf("Picked IP: %v", pickedIP)

	// the default gateway handed out by DHCP is the .1 of whatever /24 subnet the client gets handed out.
	// we actually don't care at all what the gw IP is, its really just to make the client's tcp/ip stack happy
	gw := net.IPv4(pickedIP[0], pickedIP[1], pickedIP[2], 1)

	// mix DNS but mix em consistently so same IP gets the same order
	dns := mixDNS(pickedIP)

	// should I generate a dynamic hostname?
	hostname := *flagHostname
	domainname := *flagDomainname

	// find dynamic hostname if feature is enabled
	if *flagDynHost {
		hostname = getDynamicHostname(pickedIP)
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
			ll.Debugf("unable to get static hostname: %v", err)
		}
	}

	// lets go compile the response
	var mods []dhcpv4.Modifier
	//mods = append(mods, dhcpv4.WithBroadCast(false))
	//this should not be needed. only for dhcp relay which we don't use/do. needs to be tested
	//resp.GatewayIPAddr = gw
	mods = append(mods, dhcpv4.WithYourIP(pickedIP))
	mods = append(mods, dhcpv4.WithNetmask(net.CIDRMask(24, 32)))
	mods = append(mods, dhcpv4.WithServerIP(gw))
	mods = append(mods, dhcpv4.WithRouter(gw))
	mods = append(mods, dhcpv4.WithDNS(dns...))
	mods = append(mods, dhcpv4.WithOption(dhcpv4.OptIPAddressLeaseTime(*flagLeaseTime)))
	mods = append(mods, dhcpv4.WithOption(dhcpv4.OptHostName(hostname)))
	mods = append(mods, dhcpv4.WithOption(dhcpv4.OptDomainName(domainname)))
	mods = append(mods, dhcpv4.WithOption(dhcpv4.OptServerIdentifier(gw)))

	if *flagBootfile != "" {
		mods = append(mods, dhcpv4.WithOption(dhcpv4.OptBootFileName(*flagBootfile)))
	}
	if *flagTftpIP != "" {
		mods = append(mods, dhcpv4.WithOption(dhcpv4.OptTFTPServerName(*flagTftpIP)))
	}

	switch mt := req.MessageType(); mt {
	case dhcpv4.MessageTypeDiscover:
		mods = append(mods, dhcpv4.WithMessageType(dhcpv4.MessageTypeOffer))
	case dhcpv4.MessageTypeRequest:
		mods = append(mods, dhcpv4.WithMessageType(dhcpv4.MessageTypeAck))
	default:
		ll.Warnf("Unhandled message type: %v", mt)
		return
	}

	resp, err := dhcpv4.NewReplyFromRequest(req, mods...)
	if err != nil {
		ll.Errorf("Failed to compile reply: %v", err)
		return
	}

	var peer *net.UDPAddr
	//only needed if we wanna support dhcp relay, we don't need that
	//if !req.GatewayIPAddr.IsUnspecified() {
	//	// TODO: make RFC8357 compliant
	//	peer = &net.UDPAddr{IP: req.GatewayIPAddr, Port: dhcpv4.ServerPort}
	if resp.MessageType() == dhcpv4.MessageTypeNak {
		peer = &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
	} else if !req.ClientIPAddr.IsUnspecified() {
		peer = &net.UDPAddr{IP: req.ClientIPAddr, Port: dhcpv4.ClientPort}
	} else if req.IsBroadcast() {
		peer = &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
	} else {
		// FIXME: we're supposed to unicast to a specific *L2* address, and an L3
		// address that's not yet assigned.
		// I don't know how to do that with this API...
		//peer = &net.UDPAddr{IP: resp.YourIPAddr, Port: dhcpv4.ClientPort}
		ll.Traceln("Cannot handle non-broadcast-capable unspecified peers in an RFC-compliant way. Response will be broadcast")
		peer = &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
	}

	woob := &ipv4.ControlMessage{
		IfIndex: oob.IfIndex,
		//would be nice to set a public source IP but using simple packetConn I can only set a configured ip on the interface, which we have none, so letting the kernel pick
		//Src:     net.IPv4bcast,
	}

	ll.Infof(
		"%s to %s on %s with %s, lease %s, hostname %s.%s, dns %s",
		resp.MessageType(),
		peer.IP,
		ifi.Name,
		pickedIP,
		*flagLeaseTime,
		hostname,
		domainname,
		dns,
	)
	ll.Trace(resp.Summary())

	if _, err := l.c.WriteTo(resp.ToBytes(), woob, peer); err != nil {
		ll.Warnf("Write to connection %v failed: %v", peer, err)
	}
}
