// sophie.maz — MAZ-43 boot-time smoke test for end-to-end HTTPS+JSON
// connectivity to api.anthropic.com via mazarin's network stack.
//
// At boot: reads /sophie/secrets/anthropic/claude-api-key.toml for the
// API key, /sophie/ssl/cacert.pem for the trust store, opens a TLS
// connection through netclient → adapter → tls.Client, posts one
// Messages API request, asserts "Washington" is in the response. Prints
// [sophie] PASS or [sophie] FAIL on a single line.
//
// Future architecture (post-reorg, see MAZ-43): sophie talks to a
// protocol-claude shepherd via uring instead of owning TLS+HTTPS+JSON
// directly. For now sophie is a fat client because the goal is proving
// the stack works end-to-end. See MAZ-43 ticket for details.
package main

import (
	"crypto/x509"
	"fmt"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"mazzy/mazarin/claude"
	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/netclient"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
	"mazzy/shared/netproto"
)

const tag = "[sophie] "

// sophieFsRing is the dedicated uring ring sophie uses to talk to fs.
// Per FS dedicated-response-ring rule: every shepherd that talks to fs
// allocates its own ring number to avoid head-of-line deadlock on
// ring 0. Ring 1 is the conventional first non-default ring.
const sophieFsRing = 1

// defaultModel is the Claude model used when the TOML config doesn't
// override it. Pinned to a current production model — update when
// rotating to a newer one.
const defaultModel = "claude-opus-4-5"

// prompt is the one-shot question sophie asks Claude. The PASS
// condition is that the answer contains "Washington".
const prompt = "What is the capital of the US state that contains Seattle?"

// apiKeyPath / caBundlePath are the two files sophie reads at startup.
const (
	apiKeyPath   = "/sophie/secrets/anthropic/claude-api-key.toml"
	caBundlePath = "/sophie/ssl/cacert.pem"
)

// config matches the TOML schema in claude-api-key.toml.
type config struct {
	APIKey     string `toml:"api_key"`
	Model      string `toml:"model"`
	EndpointIP string `toml:"endpoint_ip"` // dig api.anthropic.com +short to refresh
	TimeUnix   int64  `toml:"time_unix"`   // mazzy has no RTC yet; ops sets `date +%s`
}

func init() { mazhost.PinEntry(MazarinMain, nil) }

//go:noinline
func MazarinMain() {
	sys.UartWriteString(tag + "start\n")
	if err := run(); err != nil {
		sys.UartWriteString(tag + "FAIL: " + err.Error() + "\n")
	}
	// Stay alive so kernel cleanup doesn't trip on residual state. Same
	// pattern as xfertest's keep-alive after smoke checks.
	sys.SetReady(true)
	select {}
}

func run() error {
	// Wait for fs and net shepherds. sophie depends on both.
	if err := sys.WaitForShepherdReady("fs", 5); err != nil {
		return fmt.Errorf("fs not ready: %w", err)
	}
	if err := sys.WaitForShepherdReady("net", 5); err != nil {
		return fmt.Errorf("net not ready: %w", err)
	}

	fc, err := connectFS()
	if err != nil {
		return fmt.Errorf("fsclient setup: %w", err)
	}

	cfgBytes, err := fc.ReadFile(apiKeyPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", apiKeyPath, err)
	}
	var cfg config
	if err := toml.Unmarshal(cfgBytes, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", apiKeyPath, err)
	}
	if cfg.APIKey == "" {
		return fmt.Errorf("%s: api_key is empty", apiKeyPath)
	}
	if cfg.EndpointIP == "" {
		return fmt.Errorf("%s: endpoint_ip is empty (run `dig api.anthropic.com +short` and paste an IP)", apiKeyPath)
	}
	if cfg.TimeUnix == 0 {
		return fmt.Errorf("%s: time_unix is empty (mazzy has no RTC; paste `date +%%s` output for cert validation)", apiKeyPath)
	}
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	ip4, err := parseIPv4(cfg.EndpointIP)
	if err != nil {
		return fmt.Errorf("%s: endpoint_ip %q: %w", apiKeyPath, cfg.EndpointIP, err)
	}

	caBytes, err := fc.ReadFile(caBundlePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", caBundlePath, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return fmt.Errorf("%s: no usable CA certs parsed", caBundlePath)
	}

	client, err := claude.NewClient(cfg.APIKey, cfg.Model, pool)
	if err != nil {
		return fmt.Errorf("claude.NewClient: %w", err)
	}
	// mazzy doesn't have a real-time clock yet — time.Now() returns
	// seconds-since-boot starting at Unix epoch 0. crypto/tls's cert
	// validity check would reject every legitimate cert. Inject the
	// ops-supplied wall-clock time so validation works.
	wallTime := time.Unix(cfg.TimeUnix, 0)
	client.TLSConfig().Time = func() time.Time { return wallTime }

	nc, err := connectNet()
	if err != nil {
		return fmt.Errorf("netclient setup: %w", err)
	}

	dst := netproto.Addr{IP4: ip4, Port: 443}
	connID, _, err := nc.TCPConnect([4]byte{}, 0, dst)
	if err != nil {
		return fmt.Errorf("TCPConnect %d.%d.%d.%d:443: %w", ip4[0], ip4[1], ip4[2], ip4[3], err)
	}
	conn := newNetConn(nc, connID)
	defer conn.Close()

	text, err := client.Ask(conn, prompt)
	if err != nil {
		return fmt.Errorf("Ask: %w", err)
	}

	if !strings.Contains(text, "Washington") {
		// Surface a clipped preview so the failure says something useful.
		preview := text
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		sys.UartWriteString(tag + "FAIL: response missing Washington: " + preview + "\n")
		return nil
	}

	sys.UartWriteString(tag + "PASS response contains Washington (len=" + sys.Itoa(int64(len(text))) + " bytes)\n")
	return nil
}

// connectFS sets up a fsclient on sophie's dedicated ring.
func connectFS() (fsclient.FSClient, error) {
	if err := uring.Setup(sophieFsRing); err != nil {
		return nil, fmt.Errorf("uring.Setup(%d): %w", sophieFsRing, err)
	}
	fsSID := sys.MustGetShepherdByName("fs")
	fc := fsclient.New(fsSID)
	fc.SetRespRing(uint8(sophieFsRing))

	disp := uring.NewDispatcherWithRing(sophieFsRing)
	disp.On(ipc.ProtoFSIPCResp, fsclient.DecodeResp, fc.GetRespCh())
	disp.Start()

	if err := fc.Connect(); err != nil {
		return nil, fmt.Errorf("fc.Connect: %w", err)
	}
	return fc, nil
}

// connectNet sets up a netclient on the default uring ring.
func connectNet() (netclient.NetClient, error) {
	netSID, err := sys.GetShepherdByName("net")
	if err != nil {
		return nil, fmt.Errorf("GetShepherdByName(net): %w", err)
	}
	nc := netclient.New(netSID)
	disp := uring.NewDispatcher()
	disp.OnFunc(ipc.ProtoNetIPCResp, netclient.DecodeAny, nc.HandleResp)
	disp.Start()

	if err := nc.Connect(0, 0); err != nil {
		return nil, fmt.Errorf("nc.Connect: %w", err)
	}
	return nc, nil
}

// parseIPv4 parses a "1.2.3.4" string into a 4-byte array. Hand-rolled
// because net.ParseIP would drag in a chunk of stdlib for one trivial
// case (IPv4 dotted-quad only — no v6, no DNS).
func parseIPv4(s string) ([4]byte, error) {
	var out [4]byte
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return out, fmt.Errorf("want 4 octets, got %d", len(parts))
	}
	for i, p := range parts {
		var n int
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				return out, fmt.Errorf("octet %d %q is not numeric", i, p)
			}
			n = n*10 + int(ch-'0')
			if n > 255 {
				return out, fmt.Errorf("octet %d %q > 255", i, p)
			}
		}
		out[i] = byte(n)
	}
	return out, nil
}
