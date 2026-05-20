package main

import (
	"bytes"
	"fmt"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

// echoPort is the classic inetd echo port — same number for both UDP
// and TCP; gvisor demuxes by transport protocol. QEMU SLIRP doesn't
// ship either echo so we run them both in-stack.
const echoPort uint16 = 7

// udpEchoPort is preserved for the existing UDP echo server's logging.
const udpEchoPort uint16 = echoPort

// runEchoTest sends a single ICMPv4 echo request to the SLIRP gateway
// (10.0.2.2) and waits for the reply. Phase B step 6 — exercises the
// full netstack → LinkEndpoint → net.elf → io_uring → SLIRP → io_uring
// → RecvChan → DeliverNetworkPacket loop. Run as a goroutine from
// MazarinMain after buildStack returns.
func runEchoTest(s *stack.Stack) {
	wq := &waiter.Queue{}
	ep, terr := s.NewEndpoint(icmp.ProtocolNumber4, header.IPv4ProtocolNumber, wq)
	if terr != nil {
		fmt.Printf("[gvisor/echo] NewEndpoint: %v\n", terr)
		return
	}
	defer ep.Close()

	waitEntry, ch := waiter.NewChannelEntry(waiter.ReadableEvents)
	wq.EventRegister(&waitEntry)
	defer wq.EventUnregister(&waitEntry)

	remote := tcpip.FullAddress{
		NIC:  nicID,
		Addr: gwV4,
	}
	if err := ep.Connect(remote); err != nil {
		fmt.Printf("[gvisor/echo] Connect(10.0.2.2): %v\n", err)
		return
	}

	// 8-byte ICMP header + 32-byte payload. gvisor overwrites Ident
	// with the endpoint's bound port and recomputes the checksum, so
	// we only have to set Type/Code/Sequence/payload.
	const payloadLen = 32
	pkt := make([]byte, header.ICMPv4MinimumSize+payloadLen)
	icmpHdr := header.ICMPv4(pkt)
	icmpHdr.SetType(header.ICMPv4Echo)
	icmpHdr.SetCode(0)
	icmpHdr.SetSequence(1)
	for i := 0; i < payloadLen; i++ {
		pkt[header.ICMPv4MinimumSize+i] = byte(i)
	}

	// Route resolution / ARP can return ErrWouldBlock on the first
	// Write. Retry a handful of times with a short backoff.
	var sent int64
	t0 := time.Now()
	for attempt := 0; attempt < 20; attempt++ {
		n, werr := ep.Write(bytes.NewReader(pkt), tcpip.WriteOptions{})
		if werr == nil {
			sent = n
			break
		}
		_, wouldBlock := werr.(*tcpip.ErrWouldBlock)
		_, hostUnreach := werr.(*tcpip.ErrHostUnreachable)
		if !wouldBlock && !hostUnreach {
			fmt.Printf("[gvisor/echo] Write: %v\n", werr)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	if sent == 0 {
		fmt.Println("[gvisor/echo] Write: gave up after ARP retries")
		return
	}
	fmt.Printf("[gvisor/echo] echo request sent (%d bytes) to 10.0.2.2\n", sent)

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		fmt.Println("[gvisor/echo] timeout waiting for reply")
		return
	}

	var buf bytes.Buffer
	res, rerr := ep.Read(&buf, tcpip.ReadOptions{NeedRemoteAddr: true})
	if rerr != nil {
		fmt.Printf("[gvisor/echo] Read: %v\n", rerr)
		return
	}
	latency := time.Since(t0)
	fmt.Printf("[gvisor/echo] reply: %d bytes from %v in %v\n",
		res.Count, res.RemoteAddr.Addr, latency)
}

// runTCPEchoServer is the in-stack TCP echo on echoPort. SLIRP doesn't
// ship a TCP echo daemon, so we run one in-stack for xfertest's
// active-connect coverage. Per accepted connection a goroutine reads
// bytes off the stream and writes them back; the goroutine exits on
// EOF or any other read error.
func runTCPEchoServer(s *stack.Stack) {
	wq := &waiter.Queue{}
	ep, terr := s.NewEndpoint(tcp.ProtocolNumber, header.IPv4ProtocolNumber, wq)
	if terr != nil {
		fmt.Printf("[gvisor/tcp-echo] NewEndpoint: %v\n", terr)
		return
	}
	defer ep.Close()
	if terr := ep.Bind(tcpip.FullAddress{Port: echoPort}); terr != nil {
		fmt.Printf("[gvisor/tcp-echo] Bind(:%d): %v\n", echoPort, terr)
		return
	}
	if terr := ep.Listen(16); terr != nil {
		fmt.Printf("[gvisor/tcp-echo] Listen: %v\n", terr)
		return
	}

	waitEntry, notifyCh := waiter.NewChannelEntry(waiter.ReadableEvents)
	wq.EventRegister(&waitEntry)
	defer wq.EventUnregister(&waitEntry)

	fmt.Printf("[gvisor/tcp-echo] listening on 0.0.0.0:%d\n", echoPort)
	for {
		for {
			var peer tcpip.FullAddress
			newEP, newWQ, terr := ep.Accept(&peer)
			if terr != nil {
				if _, wb := terr.(*tcpip.ErrWouldBlock); !wb {
					fmt.Printf("[gvisor/tcp-echo] Accept: %v\n", terr)
				}
				break
			}
			go tcpEchoServe(newEP, newWQ)
		}
		<-notifyCh
	}
}

// tcpEchoServe handles one accepted TCP connection: read → write back
// → repeat. EOF / connection close from the client tears down.
func tcpEchoServe(ep tcpip.Endpoint, wq *waiter.Queue) {
	defer ep.Close()
	waitEntry, notifyCh := waiter.NewChannelEntry(waiter.ReadableEvents | waiter.EventHUp | waiter.EventErr)
	wq.EventRegister(&waitEntry)
	defer wq.EventUnregister(&waitEntry)

	for {
		var buf bytes.Buffer
		res, rerr := ep.Read(&buf, tcpip.ReadOptions{})
		if rerr != nil {
			if _, wb := rerr.(*tcpip.ErrWouldBlock); wb {
				<-notifyCh
				continue
			}
			return
		}
		payload := buf.Bytes()[:res.Count]
		if _, werr := ep.Write(bytes.NewReader(payload), tcpip.WriteOptions{}); werr != nil {
			return
		}
	}
}

// runUDPEchoServer is the in-stack UDP echo on udpEchoPort. xfertest's
// stageUDPEcho is the round-trip test client.
func runUDPEchoServer(s *stack.Stack) {
	wq := &waiter.Queue{}
	ep, terr := s.NewEndpoint(udp.ProtocolNumber, header.IPv4ProtocolNumber, wq)
	if terr != nil {
		fmt.Printf("[gvisor/udp-echo] NewEndpoint: %v\n", terr)
		return
	}
	defer ep.Close()
	if terr := ep.Bind(tcpip.FullAddress{Port: udpEchoPort}); terr != nil {
		fmt.Printf("[gvisor/udp-echo] Bind(:%d): %v\n", udpEchoPort, terr)
		return
	}

	waitEntry, notifyCh := waiter.NewChannelEntry(waiter.ReadableEvents)
	wq.EventRegister(&waitEntry)
	defer wq.EventUnregister(&waitEntry)

	fmt.Printf("[gvisor/udp-echo] listening on 0.0.0.0:%d\n", udpEchoPort)
	for {
		<-notifyCh
		for {
			var buf bytes.Buffer
			res, rerr := ep.Read(&buf, tcpip.ReadOptions{NeedRemoteAddr: true})
			if rerr != nil {
				if _, ok := rerr.(*tcpip.ErrWouldBlock); !ok {
					fmt.Printf("[gvisor/udp-echo] Read: %v\n", rerr)
				}
				break
			}
			payload := buf.Bytes()[:res.Count]
			to := res.RemoteAddr
			if _, werr := ep.Write(bytes.NewReader(payload), tcpip.WriteOptions{To: &to}); werr != nil {
				fmt.Printf("[gvisor/udp-echo] Write to %v:%d: %v\n",
					to.Addr, to.Port, werr)
			}
		}
	}
}
