package main

import (
	"fmt"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/ethernet"
	"gvisor.dev/gvisor/pkg/tcpip/link/loopback"
	"gvisor.dev/gvisor/pkg/tcpip/network/arp"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"

	"mazzy/mazarin/linksurface"
)

// nicID is the gvisor NIC identifier we attach our LinkEndpoint to.
// loopbackNICID is a second internal NIC bound to 127.0.0.0/8 so
// app-shepherds can talk to themselves without going through SLIRP.
const (
	nicID         tcpip.NICID = 1
	loopbackNICID tcpip.NICID = 2
)

// v4PrefixLen is the /24 prefix for the SLIRP subnet.
const v4PrefixLen = 24

// hostV4 / gwV4: hardcoded SLIRP defaults. DHCP and SLAAC are deferred
// (MAZ-33 covers v6).
var (
	hostV4 = tcpip.AddrFrom4([4]byte{10, 0, 2, 15})
	gwV4   = tcpip.AddrFrom4([4]byte{10, 0, 2, 2})
)

// buildStack constructs the tcpip.Stack, wraps the inner rawEndpoint
// with link/ethernet for L2 framing, attaches it as NIC nicID, and
// installs the static IPv4 address + default route.
func buildStack(dev linksurface.Device, alloc linksurface.Allocator) (*stack.Stack, error) {
	s := stack.New(stack.Options{
		NetworkProtocols: []stack.NetworkProtocolFactory{
			arp.NewProtocol,
			ipv4.NewProtocol,
		},
		TransportProtocols: []stack.TransportProtocolFactory{
			icmp.NewProtocol4,
			udp.NewProtocol,
			tcp.NewProtocol,
		},
	})

	// wireMTU includes the 14-byte ethernet header; link/ethernet's
	// wrapper subtracts it before reporting MTU to the IP layer.
	globalRawEP = &rawEndpoint{
		mtu:    wireMTU,
		mac:    macFromInt64(dev.GetEthernetAddr()),
		alloc:  alloc,
		txChan: globalTxChan,
	}

	if err := s.CreateNIC(nicID, ethernet.New(globalRawEP)); err != nil {
		return nil, fmt.Errorf("CreateNIC: %v", err)
	}

	// ARP is auto-handled by the NIC once an IPv4 address is bound;
	// explicit AddProtocolAddress for arp.ProtocolNumber would fail
	// with "operation not supported".
	if err := s.AddProtocolAddress(nicID, tcpip.ProtocolAddress{
		Protocol: ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address:   hostV4,
			PrefixLen: v4PrefixLen,
		},
	}, stack.AddressProperties{}); err != nil {
		return nil, fmt.Errorf("AddProtocolAddress(IPv4): %v", err)
	}

	if err := s.CreateNIC(loopbackNICID, loopback.New()); err != nil {
		return nil, fmt.Errorf("CreateNIC(loopback): %v", err)
	}
	if err := s.AddProtocolAddress(loopbackNICID, tcpip.ProtocolAddress{
		Protocol: ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address:   header.IPv4Loopback,
			PrefixLen: 8,
		},
	}, stack.AddressProperties{}); err != nil {
		return nil, fmt.Errorf("AddProtocolAddress(loopback): %v", err)
	}

	s.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4LoopbackSubnet, NIC: loopbackNICID},
		{Destination: header.IPv4EmptySubnet, Gateway: gwV4, NIC: nicID},
	})
	return s, nil
}

// macFromInt64 unpacks the BE-int64 MAC convention (low 6 bytes carry
// the MAC, high two are zero) into a tcpip.LinkAddress.
func macFromInt64(mac int64) tcpip.LinkAddress {
	b := [6]byte{
		byte(mac >> 40),
		byte(mac >> 32),
		byte(mac >> 24),
		byte(mac >> 16),
		byte(mac >> 8),
		byte(mac),
	}
	return tcpip.LinkAddress(b[:])
}
