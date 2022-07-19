package main

import (
	"net"
	"syscall"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/insomniacslk/dhcp/dhcpv4"
	ll "github.com/sirupsen/logrus"
)

// function to wrap dhcp response with appropriate ip and udp headers
// for clients that require a UDP checksum
func sendPacket(peer *net.UDPAddr, peerMAC net.HardwareAddr, ifi net.Interface, resp *dhcpv4.DHCPv4) error {

	ip := layers.IPv4{
		Version:  4,
		TTL:      64,
		SrcIP:    resp.ServerIPAddr,
		DstIP:    peer.IP,
		Protocol: layers.IPProtocolUDP,
		Flags:    layers.IPv4DontFragment,
	}
	udp := layers.UDP{
		SrcPort: dhcpv4.ServerPort,
		DstPort: dhcpv4.ClientPort,
	}

	err := udp.SetNetworkLayerForChecksum(&ip)
	if err != nil {
		ll.Errorf("err: %v", err)
		return err
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}

	pkt := gopacket.NewPacket(resp.ToBytes(), layers.LayerTypeDHCPv4, gopacket.NoCopy)
	dhcpLayer := pkt.Layer(layers.LayerTypeDHCPv4)
	dhcp, ok := dhcpLayer.(gopacket.SerializableLayer)
	if !ok {
		ll.Errorf("layer %s is not serializable", dhcpLayer.LayerType().String())
		return err
	}

	eth := layers.Ethernet{
		EthernetType: layers.EthernetTypeIPv4,
		SrcMAC:       ifi.HardwareAddr,
		DstMAC:       peerMAC,
	}

	err = gopacket.SerializeLayers(buf, opts, &eth, &ip, &udp, dhcp)
	if err != nil {
		ll.Errorf("err serialize layer: %v", err)
		return err
	}
	data := buf.Bytes()

	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, 0)
	if err != nil {
		ll.Errorf("sendPacket: cannot open socket: %v", err)
		return err
	}
	defer func() {
		err = syscall.Close(fd)
		if err != nil {
			ll.Errorf("sendPacket: cannot close socket: %v", err)
		}
	}()

	err = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	if err != nil {
		ll.Errorf("send packet: cannot set option for socket: %v", err)
		return err
	}

	var hwAddr [8]byte
	copy(hwAddr[0:6], resp.ClientHWAddr[0:6])

	ethAddr := syscall.SockaddrLinklayer{
		Protocol: 0,
		Ifindex:  ifi.Index,
		Halen:    6,
		Addr:     hwAddr, //not used
	}
	err = syscall.Sendto(fd, data, 0, &ethAddr)
	if err != nil {
		ll.Errorf("cannot send frame via socket: %v", err)
		return err
	}
	return nil
}
