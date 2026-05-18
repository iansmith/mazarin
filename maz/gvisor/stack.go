package main

import (
	"fmt"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/ethernet"
	"gvisor.dev/gvisor/pkg/tcpip/network/arp"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"

	"mazzy/mazarin/linksurface"
)

const (
	// nicID is the gvisor NIC identifier we attach our LinkEndpoint to.
	// One NIC per shepherd — multi-NIC isn't a thing in mazarin yet.
	nicID tcpip.NICID = 1

	// hostV4 / gwV4 / v4PrefixLen: hardcoded SLIRP defaults. DHCP and
	// SLAAC are deferred (MAZ-33 covers v6).
	hostV4      = "\x0a\x00\x02\x0f" // 10.0.2.15
	gwV4        = "\x0a\x00\x02\x02" // 10.0.2.2 (SLIRP gateway)
	v4PrefixLen = 24
)

// buildStack constructs the tcpip.Stack, wraps the inner rawEndpoint
// with link/ethernet for L2 framing, attaches it as NIC nicID, and
// installs the static IPv4 address + default route.
func buildStack(dev linksurface.Device, alloc linksurface.Allocator) error {
	s := stack.New(stack.Options{
		NetworkProtocols: []stack.NetworkProtocolFactory{
			arp.NewProtocol,
			ipv4.NewProtocol,
		},
		TransportProtocols: []stack.TransportProtocolFactory{
			icmp.NewProtocol4,
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
		return fmt.Errorf("CreateNIC: %v", err)
	}

	// ARP is auto-handled by the NIC once an IPv4 address is bound;
	// explicit AddProtocolAddress for arp.ProtocolNumber would fail
	// with "operation not supported".
	if err := s.AddProtocolAddress(nicID, tcpip.ProtocolAddress{
		Protocol: ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address:   tcpip.AddrFromSlice([]byte(hostV4)),
			PrefixLen: v4PrefixLen,
		},
	}, stack.AddressProperties{}); err != nil {
		return fmt.Errorf("AddProtocolAddress(IPv4): %v", err)
	}

	s.SetRouteTable([]tcpip.Route{
		{
			Destination: header.IPv4EmptySubnet,
			Gateway:     tcpip.AddrFromSlice([]byte(gwV4)),
			NIC:         nicID,
		},
	})
	return nil
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
