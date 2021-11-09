package main

import (
	"io"
	"net"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/server4"

	"golang.org/x/net/ipv4"
	ll "github.com/sirupsen/logrus"
)

type listener4 struct {
	*ipv4.PacketConn
	net.Interface
}

type listener interface {
	io.Closer
}

func listen4() (*listener4, error) {
	var err error
	l4 := listener4{}

	s := net.UDPAddr{
		IP:   net.IPv4zero,
		Port: 67,
		Zone: "",
	}

	udpConn, err := server4.NewIPv4UDPConn(s.Zone, &s)
	if err != nil {
		return nil, err
	}

	l4.PacketConn = ipv4.NewPacketConn(udpConn)

	// When not bound to an interface, we need the information in each
	// packet to know which interface it came on
	err = l4.SetControlMessage(ipv4.FlagInterface, true)
	if err != nil {
		return nil, err
	}

	return &l4, nil
}

func (l *listener4) Serve() error {
	ll.Infof("Listen %s", l.LocalAddr())
	for {
		b := *bufpool.Get().(*[]byte)
		b = b[:MaxDatagram] //Reslice to max capacity in case the buffer in pool was resliced smaller

		n, oob, peer, err := l.ReadFrom(b)
		if err != nil {
			ll.Errorf("Error reading from connection: %v", err)
			return err
		}
		go l.HandleMsg4(b[:n], oob, peer.(*net.UDPAddr))
	}
}

func (l *listener4) HandleMsg4(buf []byte, oob *ipv4.ControlMessage, _peer net.Addr) {
	var (
		resp *dhcpv4.DHCPv4
		err  error
	)

	ifi, err := net.InterfaceByIndex(oob.IfIndex)
	if err != nil {
		ll.Errorf("Error getting request interface: %v", err)
		return
	}

	req, err := dhcpv4.FromBytes(buf)
	bufpool.Put(&buf)
	if err != nil {
		ll.Errorf("Error parsing DHCPv4 request: %v", err)
		return
	}

	ll.Debugf("received request on %v", ifi.Name)
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

	resp, err = dhcpv4.NewReplyFromRequest(req)
	if err != nil {
		ll.Errorf("Failed to compile reply: %v", err)
		return
	}

	switch mt := req.MessageType(); mt {
	case dhcpv4.MessageTypeDiscover:
		resp.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeOffer))
	case dhcpv4.MessageTypeRequest:
		resp.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeAck))
	default:
		ll.Warnf("Unhandled message type: %v", mt)
		return
	}

	if resp == nil {
		ll.Warnln("Dropping request because response is nil")
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

	//this should not be needed. only for dhcp relay which we don't use/do. needs to be tested
	//resp.GatewayIPAddr = gw

	// should I generate a dynamic hostname?
	hostname := *flagHostname
	domainname := *flagDomainname


	// find dynamic hostname if feature is enabled
	if *flagDynHost {
		h, d, err := getDynamicHostname(pickedIP)
		if err == nil {
			hostname = h
			domainname = d
		}
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
			ll.Warnf("unable to set static hostname: %v", err)
		}
	}

	// lets go compile the response
	resp.YourIPAddr = pickedIP
	resp.UpdateOption(dhcpv4.OptRouter(gw))
	resp.UpdateOption(dhcpv4.OptSubnetMask(net.CIDRMask(24, 32)))
	resp.UpdateOption(dhcpv4.OptIPAddressLeaseTime(*flagLeaseTime))

	resp.UpdateOption(dhcpv4.OptHostName(hostname))
	resp.UpdateOption(dhcpv4.OptDomainName(domainname))
	resp.UpdateOption(dhcpv4.OptDNS(myDNS...))

	resp.UpdateOption(dhcpv4.OptBootFileName(*flagBootfile))
	resp.UpdateOption(dhcpv4.OptTFTPServerName(*flagTftpIP))

	var peer *net.UDPAddr
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
		ll.Debugln("Cannot handle non-broadcast-capable unspecified peers in an RFC-compliant way. Response will be broadcast")
		peer = &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
	}

	var woob *ipv4.ControlMessage
	//if peer.IP.Equal(net.IPv4bcast) || peer.IP.IsLinkLocalUnicast() {
	if peer.IP.Equal(net.IPv4bcast) {
		// Direct broadcasts and link-local to the interface the request was
		// received on. Other packets should use the normal routing table in
		// case of asymetric routing
		switch {
		//case l.Interface.Index != 0:
		//	woob = &ipv4.ControlMessage{IfIndex: l.Interface.Index}
		case oob != nil && oob.IfIndex != 0:
			woob = &ipv4.ControlMessage{IfIndex: oob.IfIndex}
		default:
			ll.Warnf("Did not receive detailed interface information from caller...")
		}
	}

	ll.Infof(
		"Responding to %v on %v with %v lease %v and hostname %v.%v",
		peer.IP,
		ifi.Name,
		pickedIP,
		*flagLeaseTime,
		hostname,
		domainname,
	)
	ll.Trace(resp.Summary())

	if _, err := l.WriteTo(resp.ToBytes(), woob, peer); err != nil {
		ll.Warnf("Write to connection %v failed: %v", peer, err)
	}
}
