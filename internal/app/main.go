package app

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	webassets "github.com/RomanovCaesar/m-ui/web"
	// 时区数据库随二进制打包，这样 -trimpath 出来的 exe 在没装 Go / 没有 zoneinfo 的机器上
	// 也能 time.LoadLocation("Asia/Shanghai")。
	_ "time/tzdata"
)

var webFS = webassets.FS

const (
	defaultListen    = "127.0.0.1:2053"
	defaultCoreAPI   = "127.0.0.1:9093"
	defaultCorePort  = 12080
	defaultConfigDir = "data"
)

var appVersion = "dev"

// 面板行为设置的默认值。除了 expireDiff / trafficDiff 之外都和 3x-ui 一致；
// 这两个阈值 3x-ui 默认是 0（不提醒），m-ui 一直有"即将到期 / 流量将尽"的橙色角标，
// 所以给成 7 天 / 1 GB，保持原有观感。
const (
	defaultRemarkModel   = "-ie"
	defaultSessionMaxAge = 360
	defaultPageSize      = 25
	defaultExpireDiff    = 7
	defaultTrafficDiff   = 1
	defaultTimeLocation  = "Local"
	defaultLanguage      = "zh-CN"
	minSessionMaxAge     = 60
	maxPageSize          = 500
)

type Settings struct {
	Username                   string          `json:"username"`
	Password                   string          `json:"password"`
	CorePath                   string          `json:"corePath"`
	PanelListen                string          `json:"panelListen"`
	PanelDomain                string          `json:"panelDomain"`
	PanelPath                  string          `json:"panelPath"`
	SubscriptionPath           string          `json:"subscriptionPath"`
	CrossPanelSubscriptionPath string          `json:"crossPanelSubscriptionPath"`
	SubscriptionPort           int             `json:"subscriptionPort,omitempty"`
	ClashPath                  string          `json:"clashPath"`
	SubscriptionClients        map[string]bool `json:"subscriptionClients,omitempty"`
	PanelCertFile              string          `json:"panelCertFile,omitempty"`
	PanelKeyFile               string          `json:"panelKeyFile,omitempty"`
	APIAddress                 string          `json:"apiAddress"`
	APISecret                  string          `json:"apiSecret"`
	MixedPort                  int             `json:"mixedPort"`
	AllowLAN                   bool            `json:"allowLan"`
	Mode                       string          `json:"mode"`
	LogLevel                   string          `json:"logLevel"`
	// 面板自身的行为设置，字段名和语义都照 3x-ui 的 AllSetting 来，方便对照。
	RemarkModel                 string `json:"remarkModel"`
	SessionMaxAge               int    `json:"sessionMaxAge"`
	PageSize                    int    `json:"pageSize"`
	ExpireDiff                  int    `json:"expireDiff"`
	TrafficDiff                 int    `json:"trafficDiff"`
	ExternalTrafficInformEnable bool   `json:"externalTrafficInformEnable"`
	ExternalTrafficInformURI    string `json:"externalTrafficInformURI"`
	TimeLocation                string `json:"timeLocation"`
	Language                    string `json:"language"`
}

type Inbound struct {
	SyncOrigin           *InboundSyncOrigin     `json:"syncOrigin,omitempty"`
	ID                   string                 `json:"id"`
	Name                 string                 `json:"name"`
	Type                 string                 `json:"type"`
	Listen               string                 `json:"listen"`
	Port                 int                    `json:"port"`
	Enabled              bool                   `json:"enabled"`
	UDP                  bool                   `json:"udp"`
	Username             string                 `json:"username"`
	Password             string                 `json:"password"`
	UUID                 string                 `json:"uuid"`
	Cipher               string                 `json:"cipher"`
	Network              string                 `json:"network"`
	WSPath               string                 `json:"wsPath"`
	Host                 string                 `json:"host"`
	TLS                  bool                   `json:"tls"`
	Certificate          string                 `json:"certificate"`
	PrivateKey           string                 `json:"privateKey"`
	CertificateMode      string                 `json:"certificateMode,omitempty"`
	Token                string                 `json:"token"`
	Notes                string                 `json:"notes"`
	Clients              []Client               `json:"clients,omitempty"`
	Traffic              TrafficStats           `json:"traffic"`
	CreatedAt            string                 `json:"createdAt,omitempty"`
	Total                uint64                 `json:"total"`
	ExpiryTime           int64                  `json:"expiryTime"`
	TrafficReset         string                 `json:"trafficReset,omitempty"`
	LastTrafficReset     string                 `json:"lastTrafficReset,omitempty"`
	Rule                 string                 `json:"rule,omitempty"`
	Proxy                string                 `json:"proxy,omitempty"`
	RoutingMark          int                    `json:"routingMark,omitempty"`
	GRPCServiceName      string                 `json:"grpcServiceName,omitempty"`
	Decryption           string                 `json:"decryption,omitempty"`
	Encryption           string                 `json:"encryption,omitempty"`
	VLESSAuth            string                 `json:"vlessAuthentication,omitempty"`
	ClientAuthType       string                 `json:"clientAuthType,omitempty"`
	ClientAuthCert       string                 `json:"clientAuthCert,omitempty"`
	ECHKey               string                 `json:"echKey,omitempty"`
	ECHServerName        string                 `json:"echServerName,omitempty"`
	TLSServerName        string                 `json:"tlsServerName,omitempty"`
	AllowInsecure        bool                   `json:"allowInsecure,omitempty"`
	Reality              RealitySettings        `json:"reality"`
	Mux                  MuxSettings            `json:"mux"`
	ShadowTLS            ShadowTLSSettings      `json:"shadowTls"`
	RestTLS              RestTLSSettings        `json:"restTls"`
	JLS                  JLSSettings            `json:"jls"`
	TrojanSS             TrojanSSSettings       `json:"trojanSS"`
	TLSMirror            TLSMirrorSettings      `json:"tlsMirror"`
	XHTTP                XHTTPSettings          `json:"xhttp"`
	Hysteria             HysteriaSettings       `json:"hysteria"`
	TUIC                 TUICSettings           `json:"tuic"`
	AnyTLS               AnyTLSSettings         `json:"anyTls"`
	Mieru                MieruSettings          `json:"mieru"`
	Sudoku               SudokuSettings         `json:"sudoku"`
	Hysteria2Realm       Hysteria2RealmSettings `json:"hysteria2Realm"`
	ShadowQuic           ShadowQuicSettings     `json:"shadowQuic"`
	TrustTunnel          TrustTunnelSettings    `json:"trustTunnel"`
	Snell                SnellSettings          `json:"snell"`
	SimpleObfs           SimpleObfsSettings     `json:"simpleObfs"`
	KcpTun               KcpTunSettings         `json:"kcpTun"`
	MKCP                 MKCPSettings           `json:"mkcp"`
	Mekya                MekyaSettings          `json:"mekya"`
	SimpleAuthEnabled    bool                   `json:"simpleAuthEnabled,omitempty"`
	SimpleAuthConfigured bool                   `json:"simpleAuthConfigured,omitempty"`
}

type RealitySettings struct {
	Enabled                               bool   `json:"enabled"`
	Dest                                  string `json:"dest"`
	PrivateKey                            string `json:"privateKey"`
	PublicKey                             string `json:"publicKey"`
	ShortIDs                              string `json:"shortIds"`
	ServerNames                           string `json:"serverNames"`
	MaxTimeDifference                     int    `json:"maxTimeDifference"`
	Proxy                                 string `json:"proxy"`
	Fingerprint                           string `json:"fingerprint,omitempty"`
	SpiderX                               string `json:"spiderX,omitempty"`
	LimitFallbackUploadAfterBytes         uint64 `json:"limitFallbackUploadAfterBytes,omitempty"`
	LimitFallbackUploadBytesPerSec        uint64 `json:"limitFallbackUploadBytesPerSec,omitempty"`
	LimitFallbackUploadBurstBytesPerSec   uint64 `json:"limitFallbackUploadBurstBytesPerSec,omitempty"`
	LimitFallbackDownloadAfterBytes       uint64 `json:"limitFallbackDownloadAfterBytes,omitempty"`
	LimitFallbackDownloadBytesPerSec      uint64 `json:"limitFallbackDownloadBytesPerSec,omitempty"`
	LimitFallbackDownloadBurstBytesPerSec uint64 `json:"limitFallbackDownloadBurstBytesPerSec,omitempty"`
}
type JLSSettings struct {
	Enabled   bool   `json:"enabled,omitempty"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
	SNI       string `json:"sni,omitempty"`
	Dest      string `json:"dest,omitempty"`
	ALPN      string `json:"alpn,omitempty"`
	Proxy     string `json:"proxy,omitempty"`
	RateLimit uint64 `json:"rateLimit,omitempty"`
}
type TrojanSSSettings struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Method   string `json:"method,omitempty"`
	Password string `json:"password,omitempty"`
}
type MuxSettings struct {
	Padding       bool   `json:"padding"`
	BrutalEnabled bool   `json:"brutalEnabled"`
	Up            string `json:"up"`
	Down          string `json:"down"`
}
type ShadowTLSSettings struct {
	Enabled  bool   `json:"enabled"`
	Version  int    `json:"version"`
	Password string `json:"password"`
	Dest     string `json:"dest"`
}
type RestTLSSettings struct {
	Enabled      bool   `json:"enabled"`
	Dest         string `json:"dest"`
	Password     string `json:"password"`
	Script       string `json:"script"`
	MinRecordLen int    `json:"minRecordLen"`
	RateLimit    uint64 `json:"rateLimit"`
	Proxy        string `json:"proxy"`
}

// TLSMirrorSettings drives the VMess `tlsmirror-config` block. tlsmirror is a
// security mode, not a transport: listener/sing_vmess/server.go collects every
// configured wrapper into securityModes and refuses to start when more than one
// is present, so it is mutually exclusive with a certificate, Reality, ShadowTLS,
// RestTLS and JLS. Unlike those last three it does not set tcpOnlySecurityMode,
// so WebSocket, gRPC, mKCP and Mekya all remain available underneath it.
//
// PrimaryKey is what switches the mode on (securityModes only counts a non-empty
// key) and must be standard base64 that decodes to exactly 32 bytes, which is
// what transport/tlsmirror.DecodePrimaryKey accepts.
//
// Dest is mandatory despite the kernel's omitempty tag: listener/tlsmirror hands
// every accepted connection to inner.HandleTcp(tunnel, Dest, Proxy), so a value
// without a port fails net.SplitHostPort and every connection dies. It is the
// real TLS server whose traffic is mirrored, i.e. the same kind of host:443 pair
// a Reality target uses.
//
// ExplicitNonceCipherSuites is an opt-in list of TLS 1.2 cipher-suite IDs whose
// records carry an explicit nonce; transport/tlsmirror publishes a recommended
// 48-entry set and an empty list means "treat none of them that way".
//
// ConnectionEnrolment only ever needs to exist: transport/tlsmirror/server.go
// nil-checks it and reads nothing from it, while its two members
// (primary-ingress-outbound / primary-egress-outbound) are consumed exclusively
// by the client half in transport/tlsmirror/{client,enrollment}.go. The panel
// therefore renders an empty mapping and does not expose them, the same way it
// leaves out hysteria2's dead max-idle-time.
type TLSMirrorSettings struct {
	Enabled                     bool   `json:"enabled"`
	PrimaryKey                  string `json:"primaryKey,omitempty"`
	Dest                        string `json:"dest,omitempty"`
	Proxy                       string `json:"proxy,omitempty"`
	ExplicitNonceCipherSuites   string `json:"explicitNonceCipherSuites,omitempty"`
	DeferBaseNanoseconds        uint64 `json:"deferBaseNanoseconds,omitempty"`
	DeferRandomNanoseconds      uint64 `json:"deferRandomNanoseconds,omitempty"`
	TransportLayerPadding       bool   `json:"transportLayerPadding,omitempty"`
	ConnectionEnrolment         bool   `json:"connectionEnrolment,omitempty"`
	SequenceWatermarkingEnabled bool   `json:"sequenceWatermarking,omitempty"`
}
type XHTTPSettings struct {
	Enabled              bool   `json:"enabled"`
	Path                 string `json:"path"`
	Host                 string `json:"host"`
	Mode                 string `json:"mode"`
	XPaddingBytes        string `json:"xPaddingBytes,omitempty"`
	XPaddingObfsMode     bool   `json:"xPaddingObfsMode,omitempty"`
	XPaddingKey          string `json:"xPaddingKey,omitempty"`
	XPaddingHeader       string `json:"xPaddingHeader,omitempty"`
	XPaddingPlacement    string `json:"xPaddingPlacement,omitempty"`
	XPaddingMethod       string `json:"xPaddingMethod,omitempty"`
	UplinkHTTPMethod     string `json:"uplinkHttpMethod,omitempty"`
	SessionPlacement     string `json:"sessionPlacement,omitempty"`
	SessionKey           string `json:"sessionKey,omitempty"`
	SeqPlacement         string `json:"seqPlacement,omitempty"`
	SeqKey               string `json:"seqKey,omitempty"`
	UplinkDataPlacement  string `json:"uplinkDataPlacement,omitempty"`
	UplinkDataKey        string `json:"uplinkDataKey,omitempty"`
	UplinkChunkSize      string `json:"uplinkChunkSize,omitempty"`
	NoSSEHeader          bool   `json:"noSSEHeader"`
	ScStreamUpServerSecs string `json:"scStreamUpServerSecs,omitempty"`
	ScMaxBufferedPosts   string `json:"scMaxBufferedPosts,omitempty"`
	ScMaxEachPostBytes   string `json:"scMaxEachPostBytes,omitempty"`
}
type MKCPSettings struct {
	Enabled          bool   `json:"enabled"`
	MTU              uint32 `json:"mtu,omitempty"`
	TTI              uint32 `json:"tti,omitempty"`
	UplinkCapacity   uint32 `json:"uplinkCapacity,omitempty"`
	DownlinkCapacity uint32 `json:"downlinkCapacity,omitempty"`
	Congestion       bool   `json:"congestion,omitempty"`
	WriteBuffer      uint32 `json:"writeBuffer,omitempty"`
	ReadBuffer       uint32 `json:"readBuffer,omitempty"`
	Seed             string `json:"seed,omitempty"`
	Header           string `json:"header,omitempty"`
}
type MekyaSettings struct {
	Enabled                        bool         `json:"enabled"`
	URL                            string       `json:"url,omitempty"`
	H2PoolSize                     int          `json:"h2PoolSize,omitempty"`
	MaxWriteDelay                  int          `json:"maxWriteDelay,omitempty"`
	MaxRequestSize                 int          `json:"maxRequestSize,omitempty"`
	PollingIntervalInitial         int          `json:"pollingIntervalInitial,omitempty"`
	MaxWriteSize                   int          `json:"maxWriteSize,omitempty"`
	MaxWriteDurationMs             int          `json:"maxWriteDurationMs,omitempty"`
	MaxSimultaneousWriteConnection int          `json:"maxSimultaneousWriteConnection,omitempty"`
	PacketWritingBuffer            int          `json:"packetWritingBuffer,omitempty"`
	KCP                            MKCPSettings `json:"kcp"`
}

// Hysteria2 listeners always run TLS 1.3 with BBR congestion control, so the
// only tuning knobs Mihomo reads are the obfuscator, the bandwidth hints and the
// QUIC window sizes. MaxIdleTime is deliberately absent: the listener option
// exists in Mihomo 1.19.30 but sing_hysteria2 never reads it.
type HysteriaSettings struct {
	Obfs                           string               `json:"obfs"`
	ObfsPassword                   string               `json:"obfsPassword"`
	ObfsMinPacketSize              int                  `json:"obfsMinPacketSize,omitempty"`
	ObfsMaxPacketSize              int                  `json:"obfsMaxPacketSize,omitempty"`
	Up                             string               `json:"up"`
	Down                           string               `json:"down"`
	IgnoreClientBandwidth          bool                 `json:"ignoreClientBandwidth"`
	Masquerade                     string               `json:"masquerade"`
	ALPN                           string               `json:"alpn"`
	UdpMTU                         int                  `json:"udpMtu"`
	CWND                           int                  `json:"cwnd,omitempty"`
	BBRProfile                     string               `json:"bbrProfile,omitempty"`
	InitialStreamReceiveWindow     uint64               `json:"initialStreamReceiveWindow,omitempty"`
	MaxStreamReceiveWindow         uint64               `json:"maxStreamReceiveWindow,omitempty"`
	InitialConnectionReceiveWindow uint64               `json:"initialConnectionReceiveWindow,omitempty"`
	MaxConnectionReceiveWindow     uint64               `json:"maxConnectionReceiveWindow,omitempty"`
	Realm                          HysteriaRealmOptions `json:"realm"`
}

// HysteriaRealmOptions drives the hysteria2 listener's `realm-opts` block, which
// makes the listener a *client* of a rendezvous API — the mirror image of the
// `hysteria2-realm` listener type this panel already exposes: sing_hysteria2
// builds a realm.Options with an http.Client that dials through the tunnel and
// hands it to hysteria2.NewService, so the server publishes itself (and its
// STUN-discovered addresses) under RealmID instead of relying on a fixed public
// address.
//
// Enable is the only gate (`if config.RealmOpts.Enable`), and everything else is
// zero-value safe: listener/parse.go hands hysteria2 a bare &IN.Hysteria2Option{},
// so unlike hysteria2-realm's own options there is nothing pre-filled that an
// omitted key would silently restore.
//
// ServerURL / Token / RealmID are required in practice: the realm client lives in
// github.com/metacubex/sing-quic and cannot be inspected here, so the panel only
// enforces what the URL itself has to look like and leaves the rest to the core.
// The remaining keys are the TLS settings applied to ServerURL through
// ca.GetTLSConfig: SNI overrides the handshake name, NameCertVerify only retargets
// the certificate's DNSName check, Fingerprint pins the certificate by SHA-256
// (browser names are explicitly rejected by ca.NewFingerprintVerifier) and
// Certificate/PrivateKey form a client certificate for mTLS against the API.
type HysteriaRealmOptions struct {
	Enabled        bool   `json:"enabled"`
	ServerURL      string `json:"serverUrl,omitempty"`
	Token          string `json:"token,omitempty"`
	RealmID        string `json:"realmId,omitempty"`
	STUNServers    string `json:"stunServers,omitempty"`
	SNI            string `json:"sni,omitempty"`
	SkipCertVerify bool   `json:"skipCertVerify,omitempty"`
	NameCertVerify string `json:"nameCertVerify,omitempty"`
	Fingerprint    string `json:"fingerprint,omitempty"`
	Certificate    string `json:"certificate,omitempty"`
	PrivateKey     string `json:"privateKey,omitempty"`
	ALPN           string `json:"alpn,omitempty"`
	Proxy          string `json:"proxy,omitempty"`
}
type TUICSettings struct {
	CongestionController  string `json:"congestionController"`
	MaxIdleTime           int    `json:"maxIdleTime"`
	AuthenticationTimeout int    `json:"authenticationTimeout"`
	ALPN                  string `json:"alpn"`
	MaxUDPRelayPacketSize int    `json:"maxUdpRelayPacketSize"`
	CWND                  int    `json:"cwnd"`
	BBRProfile            string `json:"bbrProfile"`
}
type AnyTLSSettings struct {
	PaddingScheme string `json:"paddingScheme"`
}

// Mieru listeners have no TLS layer at all: Transport picks whether the socket
// itself is TCP or UDP (Mihomo hands the same ListenConfig to either the stream
// or the packet listener factory), so it is not a "also relay UDP" switch like
// the shadowsocks/socks `udp` key. TrafficPattern is an opaque base64 protobuf
// blob produced by mieru's own tooling; the kernel decodes it with
// mierutp.Decode + Validate, which the panel cannot replicate, so m-ui only
// checks that it is well-formed base64 and lets Mihomo reject bad content.
type MieruSettings struct {
	Transport           string `json:"transport"`
	TrafficPattern      string `json:"trafficPattern"`
	UserHintIsMandatory bool   `json:"userHintIsMandatory"`
}

// Sudoku listeners authenticate with a single shared Key (see singleSecretType).
// Padding is a tri-state in Mihomo (*int, defaults 10/30), so PaddingEnabled
// gates whether m-ui writes the pair at all — otherwise a deliberate 0 could not
// be told apart from "unset". CustomTables holds one or more 8-symbol layouts;
// a single entry is written as `custom-table`, several as `custom-tables`,
// because a non-empty `custom-tables` makes the kernel ignore `custom-table`.
type SudokuSettings struct {
	AEADMethod         string   `json:"aeadMethod"`
	TableType          string   `json:"tableType"`
	PaddingEnabled     bool     `json:"paddingEnabled"`
	PaddingMin         int      `json:"paddingMin"`
	PaddingMax         int      `json:"paddingMax"`
	HandshakeTimeout   int      `json:"handshakeTimeout"`
	EnablePureDownlink bool     `json:"enablePureDownlink"`
	CustomTables       []string `json:"customTables,omitempty"`
	DisableHTTPMask    bool     `json:"disableHttpMask"`
	HTTPMaskMode       string   `json:"httpMaskMode"`
	PathRoot           string   `json:"pathRoot"`
	Fallback           string   `json:"fallback"`
}

// Snell listeners carry a single pre-shared key. ObfsHost is written into the
// listener config for round-trip fidelity even though Mihomo 1.19.30 only reads
// it on the client side.
type SnellSettings struct {
	Version  int    `json:"version"`
	ObfsMode string `json:"obfsMode"`
	ObfsHost string `json:"obfsHost"`
}
type SimpleObfsSettings struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
}

// KcpTunSettings mirrors the `kcp-tun` block of Mihomo's shadowsocks listener,
// i.e. the embedded xtaci/kcptun server. Enabling it rewires the listener:
// listener/sing_shadowsocks/server.go forces `udp` on, hands the packet socket
// to kcptun and then `continue`s past the TCP listener, so the port speaks KCP
// over UDP only and every TCP-side wrapper (simple-obfs, Shadow-TLS, RestTLS,
// JLS) becomes dead weight.
//
// Conn, AutoExpire and ScavengeTTL are deliberately missing: those three only
// drive the kcptun *client's* session pool (transport/kcptun/client.go), the
// server reads neither of them.
type KcpTunSettings struct {
	Enabled      bool   `json:"enabled"`
	Key          string `json:"key,omitempty"`
	Crypt        string `json:"crypt,omitempty"`
	Mode         string `json:"mode,omitempty"`
	MTU          int    `json:"mtu,omitempty"`
	SndWnd       int    `json:"sndWnd,omitempty"`
	RcvWnd       int    `json:"rcvWnd,omitempty"`
	DataShard    int    `json:"dataShard,omitempty"`
	ParityShard  int    `json:"parityShard,omitempty"`
	DSCP         int    `json:"dscp,omitempty"`
	RateLimit    int    `json:"rateLimit,omitempty"`
	NoComp       bool   `json:"noComp,omitempty"`
	AckNodelay   bool   `json:"ackNodelay,omitempty"`
	NoDelay      int    `json:"noDelay,omitempty"`
	Interval     int    `json:"interval,omitempty"`
	Resend       int    `json:"resend,omitempty"`
	NoCongestion int    `json:"noCongestion,omitempty"`
	SockBuf      int    `json:"sockBuf,omitempty"`
	SmuxVer      int    `json:"smuxVer,omitempty"`
	SmuxBuf      int    `json:"smuxBuf,omitempty"`
	StreamBuf    int    `json:"streamBuf,omitempty"`
	FrameSize    int    `json:"frameSize,omitempty"`
	KeepAlive    int    `json:"keepAlive,omitempty"`
}

// Hysteria2RealmSettings drives the `hysteria2-realm` listener, which is not a
// proxy at all: listener/hysteria2_realm/server.go never touches the tunnel it is
// handed and only serves the rendezvous HTTP API (POST/DELETE /v1/{id}, the SSE
// event stream and the hole-punching endpoints) that hysteria2 servers use to
// publish a realm. Hence no users, no clients and no traffic accounting, and the
// port only ever speaks TCP.
//
// Token is mandatory: checkRealmToken is `expected != "" && bearer(r) == expected`,
// so a blank token rejects every request and the listener would be dead weight.
// MaxRealms / MaxRealmsPerIP are "0 = unlimited" and are always written, because
// leaving them out would silently restore Mihomo's 65536 / 4 defaults.
// RealmNamePattern goes straight into regexp.Compile and a pattern that does not
// compile aborts listener startup. It is always written too: parse.go pre-fills
// Mihomo's own pattern, so an omitted key would keep restricting realm names
// while the panel shows an empty box — an explicit "" compiles to a regexp that
// matches everything, which is what a cleared field means.
//
// The listener's `alpn` key is deliberately not exposed: it is declared and
// defaulted to h2/http-1.1, but New() never copies it into tlsConfig.NextProtos,
// so nothing in Mihomo ever reads it.
type Hysteria2RealmSettings struct {
	Token              string `json:"token"`
	MaxRealms          int    `json:"maxRealms"`
	MaxRealmsPerIP     int    `json:"maxRealmsPerIp"`
	TrustedProxyHeader string `json:"trustedProxyHeader,omitempty"`
	RealmNamePattern   string `json:"realmNamePattern,omitempty"`
}

// ShadowQuicSettings drives the `shadowquic` listener: a QUIC-only (UDP) entry
// whose TLS handshake is authenticated by JLS instead of a certificate, which is
// why it declares no certificate keys at all — listener/shadowquic/server.go
// self-signs a throwaway P-256 pair with ca.NewRandomTLSKeyPair.
//
// JLSAddr is the upstream the JLS handshake is mirrored against; it is required
// (New() returns "shadowquic: jls-upstream.addr is required") and ends up in
// socks5.ParseAddr, so it must carry a port. An empty JLSSNI falls back to the
// host part of JLSAddr, which is "" for a bare IP.
//
// ZeroRTT is always written: listener/parse.go pre-fills the option with true, so
// an omitted key would silently turn 0-RTT back on.
type ShadowQuicSettings struct {
	JLSAddr               string `json:"jlsAddr"`
	JLSSNI                string `json:"jlsSni,omitempty"`
	JLSProxy              string `json:"jlsProxy,omitempty"`
	JLSRateLimit          uint64 `json:"jlsRateLimit,omitempty"`
	ALPN                  string `json:"alpn,omitempty"`
	QUICVersions          string `json:"quicVersions,omitempty"`
	ZeroRTT               bool   `json:"zeroRtt"`
	CongestionController  string `json:"congestionController,omitempty"`
	CWND                  int    `json:"cwnd,omitempty"`
	BBRProfile            string `json:"bbrProfile,omitempty"`
	Up                    string `json:"up,omitempty"`
	Down                  string `json:"down,omitempty"`
	IgnoreClientBandwidth bool   `json:"ignoreClientBandwidth,omitempty"`
	MaxIdleTime           int    `json:"maxIdleTime,omitempty"`
	MaxDatagramFrameSize  int    `json:"maxDatagramFrameSize,omitempty"`
	RecvWindowConn        int    `json:"recvWindowConn,omitempty"`
	RecvWindow            int    `json:"recvWindow,omitempty"`
	DisableMTUDiscovery   bool   `json:"disableMtuDiscovery,omitempty"`
}

// TrustTunnelSettings drives the `trusttunnel` listener: sing-trusttunnel's
// HTTP CONNECT service behind TLS, authenticated with a Basic
// Proxy-Authorization header. A certificate is mandatory (the server refuses to
// start with "disallow using TrustTunnel without certificates config") and so are
// users, because service.ServeHTTP answers 407 whenever verify() misses.
//
// Network is a list rather than the usual `udp` bool: entries are lowercased and
// prefix-matched, `tcp*` builds the TLS listener and `udp*` the QUIC one, and both
// can run on the same port at once. A list matching neither creates no socket at
// all, silently, which is why the panel validates it. The congestion trio only
// reaches the QUIC side.
type TrustTunnelSettings struct {
	Network              string `json:"network,omitempty"`
	CongestionController string `json:"congestionController,omitempty"`
	CWND                 int    `json:"cwnd,omitempty"`
	BBRProfile           string `json:"bbrProfile,omitempty"`
}

type Client struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Username        string       `json:"username"`
	Password        string       `json:"password"`
	UUID            string       `json:"uuid"`
	Flow            string       `json:"flow,omitempty"`
	AlterID         int          `json:"alterId,omitempty"`
	Enabled         bool         `json:"enabled"`
	Total           uint64       `json:"total"`
	ExpiryTime      int64        `json:"expiryTime"`
	Traffic         TrafficStats `json:"traffic"`
	LastOnline      string       `json:"lastOnline,omitempty"`
	FirstOnline     string       `json:"firstOnline,omitempty"`
	ExpireAfterDays int          `json:"expireAfterDays,omitempty"`
	CreatedAt       string       `json:"createdAt,omitempty"`
	Notes           string       `json:"notes,omitempty"`
}

type TrafficStats struct {
	Up          uint64 `json:"up"`
	Down        uint64 `json:"down"`
	AllTimeUp   uint64 `json:"allTimeUp"`
	AllTimeDown uint64 `json:"allTimeDown"`
}

type TrafficTotals struct {
	TrafficStats
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type State struct {
	Settings           Settings                `json:"settings"`
	Inbounds           []Inbound               `json:"inbounds"`
	Outbounds          []MihomoOutbound        `json:"outbounds,omitempty"`
	RoutingRules       []MihomoRoutingRule     `json:"routingRules"`
	MihomoBasics       *MihomoBasics           `json:"mihomoBasics,omitempty"`
	OutboundTraffic    map[string]TrafficStats `json:"outboundTraffic,omitempty"`
	WARP               *WarpAccount            `json:"warp,omitempty"`
	SubscriptionTokens map[string]string       `json:"subscriptionTokens,omitempty"`
	Traffic            TrafficTotals           `json:"traffic"`
	UpdatedAt          string                  `json:"updatedAt"`
}

type CoreManager struct {
	mu                 sync.Mutex
	warpMu             sync.Mutex
	warpClient         *warpClient
	cmd                *exec.Cmd
	started            time.Time
	panelStarted       time.Time
	logs               []string
	state              State
	dataDir            string
	versionCache       string
	versionCorePath    string
	connectionStats    map[string]connectionSample
	coreUploadSample   uint64
	coreDownloadSample uint64
	lastTrafficSave    time.Time
	enforcedClients    map[string]bool
	// External Traffic Inform 攒下的增量，按 3x-ui 的节奏批量上报。
	externalTrafficInbounds map[string]externalInboundTraffic
	externalTrafficClients  map[string]externalClientTraffic
	lastTrafficInform       time.Time
	lastTrafficInformLog    time.Time
}

type App struct {
	inboundSync inboundSyncState
	manager     *CoreManager
	peers       *PeerNetwork
	cross       *CrossSubscriptionManager
	session     string
	// 会话有效期由 Settings.SessionMaxAge 决定。零值表示"不过期"，这样
	// 直接构造 App{manager: …, session: "token"} 的测试不用管这个字段。
	sessionMu      sync.Mutex
	sessionExpires time.Time
	// Restart Panel 按钮往这里发一下信号，main() 收到后重建 http.Server。
	// 缓冲 1，且用 select/default 发送，所以 nil 或者没人收都不会阻塞。
	restart chan struct{}
}

type apiResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}

// Run starts the m-ui command-line entry point or web panel. buildVersion is
// supplied by the tiny root command, whose version variable can be set with
// Go's -X linker option without coupling release tooling to an internal path.
func Run(buildVersion string) {
	if value := strings.TrimSpace(buildVersion); value != "" {
		appVersion = value
	}
	if handled, err := runCLI(os.Args[1:]); handled {
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	root, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	dataDir := filepath.Join(root, defaultConfigDir)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatal(err)
	}
	manager := &CoreManager{dataDir: dataDir, panelStarted: time.Now(), connectionStats: map[string]connectionSample{}, enforcedClients: map[string]bool{}}
	if err := manager.load(); err != nil {
		log.Printf("load state: %v", err)
	}
	// Regenerate the managed YAML on startup so format/order changes and
	// migrated metadata are reflected even when an older config.yaml exists.
	if err := manager.writeConfig(); err != nil {
		log.Printf("write config: %v", err)
	}
	go manager.monitorTraffic()
	var session string
	if err := randomToken(&session); err != nil {
		log.Fatal(err)
	}
	app := &App{manager: manager, session: session, restart: make(chan struct{}, 1)}
	peerContext, stopPeers := context.WithCancel(context.Background())
	defer stopPeers()
	if peers, err := newPeerNetwork(dataDir); err != nil {
		log.Printf("Multi-control unavailable: %v", err)
	} else {
		app.peers = peers
		go peers.run(peerContext)
	}
	if cross, err := newCrossSubscriptionManager(dataDir); err != nil {
		log.Printf("Cross-panel subscriptions unavailable: %v", err)
	} else {
		app.cross = cross
	}
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM)
	// 每一轮都从当前设置重建 http.Server，所以点了 Restart Panel 之后新的监听地址、
	// 新的 TLS 证书都会立刻生效，不用手动重启进程。
	for {
		settings := manager.settingsSnapshot()
		panelServer := &http.Server{Addr: settings.PanelListen, Handler: app.panelRoutes(settings.SubscriptionPort == 0), ReadHeaderTimeout: 10 * time.Second}
		panelCert, panelKey := strings.TrimSpace(settings.PanelCertFile), strings.TrimSpace(settings.PanelKeyFile)
		panelConfigured, panelTLS := panelTLSStatus(settings)
		if panelConfigured && !panelTLS {
			log.Printf("panel TLS certificate is configured but unavailable or invalid, falling back to HTTP")
		}
		scheme := "http"
		if panelTLS {
			scheme = "https"
		}
		log.Printf("m-ui %s listening at %s://%s%s", appVersion, scheme, panelServer.Addr, manager.panelPath())
		servers := []*http.Server{panelServer}
		if settings.SubscriptionPort != 0 {
			subscriptionServer := &http.Server{Addr: subscriptionListenAddress(settings), Handler: app.subscriptionRoutes(), ReadHeaderTimeout: 10 * time.Second}
			servers = append(servers, subscriptionServer)
			log.Printf("subscription service listening at %s://%s%s", scheme, subscriptionServer.Addr, normalizeSubscriptionPath(settings.SubscriptionPath))
		}
		errCh := make(chan error, len(servers))
		for _, server := range servers {
			go func(server *http.Server) {
				if panelTLS {
					errCh <- server.ListenAndServeTLS(panelCert, panelKey)
					return
				}
				errCh <- server.ListenAndServe()
			}(server)
		}
		shutdownServers := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			for _, server := range servers {
				_ = server.Shutdown(ctx)
			}
		}
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				shutdownServers()
				log.Fatal(err)
			}
			return
		case <-app.restart:
			shutdownServers()
			log.Printf("panel restarting")
		case <-signalCh:
			_ = manager.stopCore()
			shutdownServers()
			return
		}
	}
}

// settingsSnapshot 复制一份当前设置，供 main 的重启循环在别的 goroutine 正在改设置时安全读取。
func (m *CoreManager) settingsSnapshot() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.Settings
}

// signalRestart 通知 main 重建服务器。缓冲 1 + default 分支，所以 restart 为 nil
// （测试里直接构造的 App）或者已经排了一次重启都不会阻塞。
func (a *App) signalRestart() {
	select {
	case a.restart <- struct{}{}:
	default:
	}
}

// normalizePanelPath canonicalises the panel's URI prefix to the "/segment/"
// spelling that the router, the redirects and the session cookie all expect.
// An empty value means the panel sits at the web root, which is exactly the
// setup the dashboard warns about.
func normalizePanelPath(path string) string {
	segments := make([]string, 0, 4)
	for _, segment := range strings.Split(strings.TrimSpace(path), "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	if len(segments) == 0 {
		return "/"
	}
	return "/" + strings.Join(segments, "/") + "/"
}

// validatePanelPath keeps the prefix inside the unreserved URI characters.
// Anything else would have to be percent-encoded, and the panel's own links
// would then no longer match the prefix the router compares against.
func validatePanelPath(path string) error {
	if path == "/" {
		return nil
	}
	for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("面板 URI 路径不能包含 . 或 .. 片段")
		}
		for _, letter := range segment {
			switch {
			case letter >= 'a' && letter <= 'z', letter >= 'A' && letter <= 'Z', letter >= '0' && letter <= '9':
			case letter == '-', letter == '_', letter == '.', letter == '~':
			default:
				return fmt.Errorf("面板 URI 路径只能包含字母、数字和 - _ . ~ 与 /")
			}
		}
	}
	return nil
}

// panelPath reads the current prefix. Handlers call this per request instead of
// capturing it at startup so that saving a new path takes effect immediately.
func (m *CoreManager) panelPath() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return normalizePanelPath(m.state.Settings.PanelPath)
}

func normalizeSubscriptionPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return ""
	}
	return "/" + strings.Trim(strings.ReplaceAll(path, "\\", "/"), "/")
}

func normalizeClashPath(path string) string {
	path = normalizeSubscriptionPath(path)
	if path == "" {
		return "/clash"
	}
	return path
}

func validateSubscriptionPaths(subscriptionPath, clashPath string) error {
	for label, path := range map[string]string{"订阅路径": subscriptionPath, "Clash 路径": clashPath} {
		if path == "" {
			continue
		}
		for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
			if segment == "." || segment == ".." || segment == "" {
				return fmt.Errorf("%s无效", label)
			}
			for _, r := range segment {
				if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' || r == '~') {
					return fmt.Errorf("%s只能包含字母、数字和 - _ . ~", label)
				}
			}
		}
	}
	if subscriptionPath != "" {
		first := strings.Split(strings.TrimPrefix(subscriptionPath, "/"), "/")[0]
		if containsString([]string{"api", "login", "static"}, first) {
			return fmt.Errorf("订阅路径不能使用面板保留路径 /%s", first)
		}
	}
	if subscriptionPath != "" && subscriptionPath == clashPath {
		return fmt.Errorf("订阅路径和 Clash 路径不能相同")
	}
	return nil
}

// withPanelPath serves the whole panel under Settings.PanelPath. A request that
// does not carry the prefix gets a bare 404 — never a redirect to the real
// path, which would hand the secret straight to whatever is scanning the port.
func (a *App) withPanelPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := a.manager.panelPath()
		if base == "/" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == strings.TrimSuffix(base, "/") {
			http.Redirect(w, r, base, http.StatusFound)
			return
		}
		if !strings.HasPrefix(r.URL.Path, base) {
			http.NotFound(w, r)
			return
		}
		trimmed := r.Clone(r.Context())
		trimmed.URL.Path = "/" + strings.TrimPrefix(r.URL.Path, base)
		// RawPath must be dropped, not trimmed: a percent-encoded request would
		// otherwise keep an escaping that no longer matches the shortened Path.
		trimmed.URL.RawPath = ""
		next.ServeHTTP(w, trimmed)
	})
}

// Subscription URLs are public token-authenticated endpoints and deliberately
// live outside PanelPath, matching 3x-ui's independent subscription path.
func (a *App) withSubscriptions(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/language" {
			a.handleLanguage(w, r)
			return
		}
		if a.handleSubscriptionRequest(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) routes() http.Handler {
	return a.panelRoutes(true)
}

// panelRoutes keeps subscriptions on the panel listener only while no dedicated
// subscription port is configured. This preserves the legacy fallback without
// exposing the panel itself on the public subscription socket.
func (a *App) panelRoutes(serveSubscriptions bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/login", a.handleLoginPage)
	mux.HandleFunc("/static/", a.handleStatic)
	mux.HandleFunc("/api/auth/login", a.handleLogin)
	mux.HandleFunc("/api/language", a.handleLanguage)
	mux.HandleFunc("/api/auth/logout", a.handleLogout)
	mux.HandleFunc("/api/auth/credentials", a.auth(a.handleCredentials))
	mux.HandleFunc("/api/panel/restart", a.auth(a.handlePanelRestart))
	mux.HandleFunc("/api/state", a.auth(a.handleState))
	mux.HandleFunc("/api/config", a.auth(a.handleConfig))
	mux.HandleFunc("/api/multi-control", a.auth(a.handleMultiControl))
	mux.HandleFunc("/api/multi-control/", a.auth(a.handleMultiControl))
	mux.HandleFunc("/api/mihomo/settings", a.auth(a.handleMihomoSettings))
	mux.HandleFunc("/api/mihomo/outbound/parse", a.auth(a.handleMihomoOutboundYAML))
	mux.HandleFunc("/api/mihomo/outbound/delay", a.auth(a.handleMihomoOutboundDelay))
	mux.HandleFunc("/api/mihomo/warp", a.auth(a.handleWarp))
	mux.HandleFunc("/api/mihomo/warp/", a.auth(a.handleWarp))
	mux.HandleFunc("/api/raw-config", a.auth(a.handleRawConfig))
	mux.HandleFunc("/api/inbounds", a.auth(a.handleInbounds))
	mux.HandleFunc("/api/server/cpuHistory/", a.auth(a.handleCPUHistory))
	mux.HandleFunc("/api/inbound-sync", a.auth(a.handleInboundSync))
	mux.HandleFunc("/api/clients", a.auth(a.handleClients))
	mux.HandleFunc("/api/subscriptions/token", a.auth(a.handleSubscriptionToken))
	mux.HandleFunc("/api/traffic/reset", a.auth(a.handleTrafficReset))
	mux.HandleFunc("/api/tools/uuid", a.auth(a.handleNewUUID))
	mux.HandleFunc("/api/tools/subscription-path", a.auth(a.handleNewSubscriptionPath))
	mux.HandleFunc("/api/tools/cross-panel-subscription-path", a.auth(a.handleNewCrossPanelSubscriptionPath))
	mux.HandleFunc("/api/cross-subscriptions", a.auth(a.handleCrossSubscriptions))
	mux.HandleFunc("/api/cross-subscriptions/", a.auth(a.handleCrossSubscriptions))
	mux.HandleFunc("/api/tools/reality-keypair", a.auth(a.handleRealityKeyPair))
	mux.HandleFunc("/api/tools/vless-encryption", a.auth(a.handleVLESSEncryption))
	mux.HandleFunc("/api/tools/ech-keypair", a.auth(a.handleECHKeyPair))
	mux.HandleFunc("/api/core/start", a.auth(a.handleCoreStart))
	mux.HandleFunc("/api/core/stop", a.auth(a.handleCoreStop))
	mux.HandleFunc("/api/core/restart", a.auth(a.handleCoreRestart))
	mux.HandleFunc("/api/core/test", a.auth(a.handleCoreTest))
	mux.HandleFunc("/api/core/connections", a.auth(a.handleCoreConnections))
	mux.HandleFunc("/api/core/logs", a.auth(a.handleCoreLogs))
	mux.HandleFunc("/api/core/releases", a.auth(a.handleCoreReleases))
	mux.HandleFunc("/api/core/install", a.auth(a.handleCoreInstall))
	mux.HandleFunc("/api/geofiles/update", a.auth(a.handleGeofileUpdate))
	mux.HandleFunc("/api/config/download", a.auth(a.handleConfigDownload))
	mux.HandleFunc("/api/logs/download", a.auth(a.handleLogsDownload))
	mux.HandleFunc("/api/backup", a.auth(a.handleBackup))
	handler := a.withPanelPath(mux)
	if serveSubscriptions {
		return logging(a.withPeerNetwork(a.withSubscriptions(handler)))
	}
	return logging(a.withPeerNetwork(handler))
}

func (a *App) subscriptionRoutes() http.Handler {
	return logging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/language" {
			a.handleLanguage(w, r)
			return
		}
		if !a.handleSubscriptionRequest(w, r) {
			http.NotFound(w, r)
		}
	}))
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !a.isLoggedIn(r) {
		http.Redirect(w, r, a.manager.panelPath()+"login", http.StatusFound)
		return
	}
	a.serveLocalizedPage(w, "web/index.html")
}

func (a *App) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if a.isLoggedIn(r) {
		http.Redirect(w, r, a.manager.panelPath(), http.StatusFound)
		return
	}
	a.serveLocalizedPage(w, "web/login.html")
}

func (a *App) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	if name == "" || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	contentType := "text/plain; charset=utf-8"
	if strings.HasSuffix(name, ".css") {
		contentType = "text/css; charset=utf-8"
	} else if strings.HasSuffix(name, ".js") {
		contentType = "text/javascript; charset=utf-8"
	}
	a.serveEmbedded(w, "web/"+name, contentType)
}

// serveLocalizedPage 和 serveEmbedded 一样吐 embed 里的页面，只是顺手把面板设置里的
// Language 替换进 <html data-default-language>：页面是静态资源，没有模板引擎，而
// localStorage 还空着的浏览器只能靠这个属性决定首屏语言。
func (a *App) serveLocalizedPage(w http.ResponseWriter, name string) {
	page, err := webFS.ReadFile(name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	page = []byte(strings.Replace(string(page), `data-default-language="zh-CN"`, `data-default-language="`+a.lang()+`"`, 1))
	a.writeAsset(w, page, "text/html; charset=utf-8")
}

func (a *App) serveEmbedded(w http.ResponseWriter, name, contentType string) {
	data, err := webFS.ReadFile(name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	a.writeAsset(w, data, contentType)
}

func (a *App) writeAsset(w http.ResponseWriter, data []byte, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	var input struct{ Username, Password string }
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: "invalid request"})
		return
	}
	a.manager.mu.Lock()
	valid := input.Username == a.manager.state.Settings.Username && verifyPassword(a.manager.state.Settings.Password, input.Password)
	a.manager.mu.Unlock()
	if !valid {
		writeJSON(w, http.StatusUnauthorized, apiResponse{Message: a.tr("账号或密码错误")})
		return
	}
	// Scoping the cookie to the panel prefix keeps it out of every other path on
	// the port, and makes changing the prefix invalidate the browser's session.
	base := a.manager.panelPath()
	minutes := a.sessionMaxAgeMinutes()
	token := a.refreshSession(time.Duration(minutes) * time.Minute)
	http.SetCookie(w, &http.Cookie{Name: "mui_session", Value: token, Path: base, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: minutes * 60})
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: map[string]string{"redirect": base}})
}

// handleCredentials 对应 Authentication 页签：先验旧账号密码，再整体换掉。
func (a *App) handleCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	var input struct {
		OldUsername string `json:"oldUsername"`
		OldPassword string `json:"oldPassword"`
		NewUsername string `json:"newUsername"`
		NewPassword string `json:"newPassword"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: "invalid request"})
		return
	}
	input.NewUsername = strings.TrimSpace(input.NewUsername)
	if input.NewUsername == "" || input.NewPassword == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("新账号和新密码不能为空")})
		return
	}
	if err := a.manager.updateCredentials(input.OldUsername, input.OldPassword, input.NewUsername, input.NewPassword); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
		return
	}
	// 改完就换令牌，所有已登录的浏览器（包括当前这个）都得重新登录。
	if err := a.rotateSession(); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{Message: a.tr(err.Error())})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "mui_session", Value: "", Path: a.manager.panelPath(), MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("账号信息已更新，请重新登录")})
}

// handlePanelRestart 对应 Restart Panel 按钮：先把响应写完再让 main 重建服务器，
// 否则浏览器只会看到连接被掐断，看不到提示。
func (a *App) handlePanelRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	go func() {
		time.Sleep(400 * time.Millisecond)
		a.signalRestart()
	}()
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("面板正在重启")})
}

func (m *CoreManager) updateCredentials(oldUsername, oldPassword, newUsername, newPassword string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if oldUsername != m.state.Settings.Username || !verifyPassword(m.state.Settings.Password, oldPassword) {
		return fmt.Errorf("当前账号或密码不正确")
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	m.state.Settings.Username = newUsername
	m.state.Settings.Password = hash
	return m.saveLocked()
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "mui_session", Value: "", Path: a.manager.panelPath(), MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

// handleLanguage is intentionally public: both the login page and token-based
// subscription page can change the panel-wide language before authentication.
// It only updates this one low-risk preference; the normal settings endpoint
// remains authenticated and continues to require an explicit Save.
func (a *App) handleLanguage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	var input struct {
		Language string `json:"language"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: "invalid language request"})
		return
	}
	if err := a.manager.updateLanguage(input.Language); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: map[string]string{"language": a.manager.lang()}})
}

func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	a.manager.mu.Lock()
	state := a.manager.state
	basics := effectiveMihomoBasics(state)
	state.MihomoBasics = &basics
	state.Settings.Password = ""
	state.WARP = nil
	running := a.manager.runningLocked()
	started := a.manager.started
	logs := append([]string(nil), a.manager.logs...)
	a.manager.mu.Unlock()
	stats := collectSystemStats(a.manager.dataDir)
	appendCPUSample(time.Now(), stats.CPUPercent)
	if !started.IsZero() && running {
		stats.CoreUptime = uint64(time.Since(started).Seconds())
	}
	stats.PanelUptime = uint64(time.Since(a.manager.panelStarted).Seconds())
	panelTLSConfigured, panelTLSValid := panelTLSStatus(state.Settings)
	data := map[string]any{
		"version": appVersion, "state": state, "running": running, "startedAt": started,
		"logs": logs, "platform": runtime.GOOS, "coreVersion": a.manager.coreVersion(), "system": stats,
		"panelTLS": map[string]bool{"configured": panelTLSConfigured, "valid": panelTLSValid},
	}
	if running {
		if metrics, err := a.manager.coreGet("/traffic"); err == nil {
			data["traffic"] = metrics
		}
		if connections, err := a.manager.coreGet("/connections"); err == nil {
			data["connections"] = connections
			applyConnectionStats(&stats, connections)
			data["system"] = stats
		}
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: data})
}

// handleCPUHistory 对照 3x-ui 的 /panel/api/server/cpuHistory/:bucket：
// 桶宽只允许 2/30/60/120/180/300 秒（前端 2m/30m/1h/2h/3h/5h），最多返回 60 个平均点。
func (a *App) handleCPUHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	suffix := r.URL.Path
	if index := strings.LastIndexByte(suffix, '/'); index >= 0 {
		suffix = suffix[index+1:]
	}
	bucket, err := strconv.Atoi(suffix)
	if err != nil || !map[int]bool{2: true, 30: true, 60: true, 120: true, 180: true, 300: true}[bucket] {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: "invalid bucket"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: aggregateCPUSHistory(bucket, 60)})
}

func panelTLSStatus(settings Settings) (configured, valid bool) {
	cert, key := strings.TrimSpace(settings.PanelCertFile), strings.TrimSpace(settings.PanelKeyFile)
	configured = cert != "" && key != ""
	if !configured {
		return false, false
	}
	if _, err := os.Stat(cert); err != nil {
		return true, false
	}
	if _, err := os.Stat(key); err != nil {
		return true, false
	}
	if _, err := tls.LoadX509KeyPair(cert, key); err != nil {
		return true, false
	}
	return true, true
}

func (a *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.manager.mu.Lock()
		settings := a.manager.state.Settings
		settings.Password = ""
		a.manager.mu.Unlock()
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: settings})
	case http.MethodPut:
		var input Settings
		if err := decodeJSON(r, &input); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: "invalid settings"})
			return
		}
		if err := a.manager.updateSettings(input); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("设置已保存")})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
	}
}

func (a *App) handleRawConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg, err := a.manager.actualConfig()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, apiResponse{Message: a.tr(err.Error())})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: map[string]string{"config": string(cfg)}})
		return
	}
	if r.Method == http.MethodPost {
		if err := a.manager.writeConfig(); err != nil {
			writeJSON(w, http.StatusInternalServerError, apiResponse{Message: a.tr(err.Error())})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("配置文件已生成")})
		return
	}
	writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
}

func (a *App) handleInbounds(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.manager.mu.Lock()
		items := append([]Inbound(nil), a.manager.state.Inbounds...)
		a.manager.mu.Unlock()
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: items})
	case http.MethodPost:
		var input Inbound
		if err := decodeJSON(r, &input); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: "invalid inbound"})
			return
		}
		if err := a.manager.saveInbound(input); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("入口已保存")})
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if err := a.manager.deleteInbound(id); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("入口已删除")})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
	}
}

func (a *App) handleCoreStart(w http.ResponseWriter, r *http.Request) {
	a.coreAction(w, r, "start")
}
func (a *App) handleCoreStop(w http.ResponseWriter, r *http.Request) {
	a.coreAction(w, r, "stop")
}
func (a *App) handleCoreRestart(w http.ResponseWriter, r *http.Request) {
	a.coreAction(w, r, "restart")
}

func (a *App) handleCoreTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	if err := a.manager.validateConfig(); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("Mihomo 配置测试通过")})
}

func (a *App) handleCoreConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	path := "/connections"
	if id := r.URL.Query().Get("id"); id != "" {
		path += "/" + url.PathEscape(id)
	}
	if _, err := a.manager.coreRequest(http.MethodDelete, path); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("连接已关闭")})
}

func (a *App) coreAction(w http.ResponseWriter, r *http.Request, action string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	var err error
	switch action {
	case "start":
		err = a.manager.startCore()
	case "stop":
		err = a.manager.stopCore()
	case "restart":
		err = a.manager.restartCore()
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("核心状态已更新")})
}

func (a *App) handleCoreLogs(w http.ResponseWriter, r *http.Request) {
	a.manager.mu.Lock()
	logs := append([]string(nil), a.manager.logs...)
	a.manager.mu.Unlock()
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: logs})
}

func (a *App) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.isLoggedIn(r) {
			writeJSON(w, http.StatusUnauthorized, apiResponse{Message: "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (a *App) isLoggedIn(r *http.Request) bool {
	cookie, err := r.Cookie("mui_session")
	if err != nil || cookie.Value == "" {
		return false
	}
	token, expires := a.currentSession()
	if cookie.Value != token {
		return false
	}
	// 过期时间为零值表示"不过期"，直接构造 App 的测试就是这种情况。
	return expires.IsZero() || time.Now().Before(expires)
}

// currentSession 读一份会话令牌和过期时间。
func (a *App) currentSession() (string, time.Time) {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	return a.session, a.sessionExpires
}

// refreshSession 把服务端的会话过期时间推到 now+d，并返回当前令牌。
func (a *App) refreshSession(d time.Duration) string {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	a.sessionExpires = time.Now().Add(d)
	return a.session
}

// rotateSession 换一个新令牌，让所有已登录的浏览器立刻掉线。
func (a *App) rotateSession() error {
	var token string
	if err := randomToken(&token); err != nil {
		return err
	}
	a.sessionMu.Lock()
	a.session = token
	a.sessionExpires = time.Time{}
	a.sessionMu.Unlock()
	return nil
}

// sessionMaxAgeMinutes 读当前的会话时长设置，异常值回落到默认的 360 分钟。
func (a *App) sessionMaxAgeMinutes() int {
	minutes := a.manager.settingsSnapshot().SessionMaxAge
	if minutes < minSessionMaxAge {
		return defaultSessionMaxAge
	}
	return minutes
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if r.URL.Path != "/api/state" {
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}

func (m *CoreManager) load() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	path := filepath.Join(m.dataDir, "state.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		m.state = defaultState()
		subscriptionPath, pathErr := randomSubscriptionPath()
		if pathErr != nil {
			return fmt.Errorf("generate initial subscription path: %w", pathErr)
		}
		m.state.Settings.SubscriptionPath = subscriptionPath
		crossPath, pathErr := randomCrossPanelSubscriptionPath()
		if pathErr != nil {
			return fmt.Errorf("generate initial cross-panel subscription path: %w", pathErr)
		}
		m.state.Settings.CrossPanelSubscriptionPath = crossPath
		return m.saveLocked()
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &m.state); err != nil {
		return err
	}
	if m.state.Settings.PanelListen == "" {
		m.state.Settings = defaultState().Settings
	}
	settingsMigrated := false
	// States written before the panel had a URI prefix carry no panelPath at all.
	m.state.Settings.PanelPath = normalizePanelPath(m.state.Settings.PanelPath)
	m.state.Settings.SubscriptionPath = normalizeSubscriptionPath(m.state.Settings.SubscriptionPath)
	if m.state.Settings.SubscriptionPath == "" {
		subscriptionPath, pathErr := randomSubscriptionPath()
		if pathErr != nil {
			return fmt.Errorf("generate subscription path: %w", pathErr)
		}
		m.state.Settings.SubscriptionPath = subscriptionPath
		settingsMigrated = true
	}
	if m.state.Settings.ClashPath == "" {
		m.state.Settings.ClashPath = "/clash"
		settingsMigrated = true
	}
	m.state.Settings.ClashPath = normalizeClashPath(m.state.Settings.ClashPath)
	if m.state.Settings.CrossPanelSubscriptionPath == "" {
		crossPath, pathErr := randomCrossPanelSubscriptionPath()
		if pathErr != nil {
			return fmt.Errorf("generate cross-panel subscription path: %w", pathErr)
		}
		m.state.Settings.CrossPanelSubscriptionPath = crossPath
		settingsMigrated = true
	}
	if m.state.SubscriptionTokens == nil {
		m.state.SubscriptionTokens = map[string]string{}
		settingsMigrated = true
	}
	if m.state.RoutingRules == nil {
		m.state.RoutingRules = defaultMihomoRoutingRules()
		settingsMigrated = true
	}
	// 面板行为设置是后加的：老 state.json 里这些字段全是零值，而 remarkModel 合法值至少
	// 是一个分隔符（清空选择也存 "-"），所以空串就是"还没迁移过"的标记。
	if m.state.Settings.RemarkModel == "" {
		fallback := defaultState().Settings
		m.state.Settings.RemarkModel = fallback.RemarkModel
		m.state.Settings.SessionMaxAge = fallback.SessionMaxAge
		m.state.Settings.PageSize = fallback.PageSize
		m.state.Settings.ExpireDiff = fallback.ExpireDiff
		m.state.Settings.TrafficDiff = fallback.TrafficDiff
		m.state.Settings.TimeLocation = fallback.TimeLocation
		m.state.Settings.Language = fallback.Language
		settingsMigrated = true
	}
	migrated := m.migrateStateLocked() || settingsMigrated
	beforeSubscriptionTokens := len(m.state.SubscriptionTokens)
	if err := m.ensureSubscriptionTokensLocked(allStateClients(m.state.Inbounds)); err != nil {
		return err
	}
	if len(m.state.SubscriptionTokens) != beforeSubscriptionTokens {
		migrated = true
	}
	beforeSubscriptionUsers := len(m.state.SubscriptionTokens)
	m.pruneSubscriptionTokensLocked(m.state.Inbounds)
	if len(m.state.SubscriptionTokens) != beforeSubscriptionUsers {
		migrated = true
	}
	m.initializeEnforcedClientsLocked()
	if !strings.HasPrefix(m.state.Settings.Password, "pbkdf2_sha256$") {
		hash, hashErr := hashPassword(m.state.Settings.Password)
		if hashErr != nil {
			return hashErr
		}
		m.state.Settings.Password = hash
		return m.saveLocked()
	}
	if migrated {
		return m.saveLocked()
	}
	return nil
}

func defaultState() State {
	root, _ := os.Getwd()
	core := filepath.Join(filepath.Dir(root), "mihomo")
	if runtime.GOOS == "windows" {
		core += ".exe"
	}
	password, _ := hashPassword("admin")
	return State{
		Settings:           Settings{Username: "admin", Password: password, CorePath: core, PanelListen: defaultListen, PanelPath: "/", ClashPath: "/clash", APIAddress: defaultCoreAPI, APISecret: "m-ui-local", MixedPort: defaultCorePort, AllowLAN: true, Mode: "rule", LogLevel: "info", RemarkModel: defaultRemarkModel, SessionMaxAge: defaultSessionMaxAge, PageSize: defaultPageSize, ExpireDiff: defaultExpireDiff, TrafficDiff: defaultTrafficDiff, TimeLocation: defaultTimeLocation, Language: defaultLanguage},
		RoutingRules:       defaultMihomoRoutingRules(),
		SubscriptionTokens: map[string]string{},
		Inbounds:           []Inbound{{ID: "default-mixed", Name: "Mixed 主入口", Type: "mixed", Listen: "0.0.0.0", Port: defaultCorePort, Enabled: true, UDP: true, Notes: "HTTP + SOCKS5 混合入口", CreatedAt: time.Now().Format(time.RFC3339)}},
		UpdatedAt:          time.Now().Format(time.RFC3339),
	}
}

func (m *CoreManager) saveLocked() error {
	m.state.UpdatedAt = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(m.dataDir, "state.json.tmp")
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(m.dataDir, "state.json"))
}

func (m *CoreManager) updateSettings(input Settings) error {
	input.PanelListen = strings.TrimSpace(input.PanelListen)
	input.APIAddress = strings.TrimSpace(input.APIAddress)
	if input.PanelListen == "" {
		input.PanelListen = defaultListen
	}
	if input.APIAddress == "" {
		input.APIAddress = defaultCoreAPI
	}
	if err := validateListenAddress(input.PanelListen, "面板监听地址"); err != nil {
		return err
	}
	if err := validateListenAddress(input.APIAddress, "Mihomo API 地址"); err != nil {
		return err
	}
	input.PanelCertFile = strings.TrimSpace(input.PanelCertFile)
	input.PanelKeyFile = strings.TrimSpace(input.PanelKeyFile)
	input.PanelDomain = strings.TrimSpace(input.PanelDomain)
	input.PanelPath = normalizePanelPath(input.PanelPath)
	if err := validatePanelPath(input.PanelPath); err != nil {
		return err
	}
	if err := validatePanelDomain(input.PanelDomain); err != nil {
		return err
	}
	input.SubscriptionPath = normalizeSubscriptionPath(input.SubscriptionPath)
	if strings.TrimSpace(input.CrossPanelSubscriptionPath) != "" {
		input.CrossPanelSubscriptionPath = normalizeSubscriptionPath(input.CrossPanelSubscriptionPath)
		if err := validateCrossPanelSubscriptionPath(input); err != nil {
			return err
		}
	}
	input.ClashPath = normalizeClashPath(input.ClashPath)
	normalizeSubscriptionClientToggles(&input)
	if err := validateSubscriptionPaths(input.SubscriptionPath, input.ClashPath); err != nil {
		return err
	}
	if err := validateSubscriptionServicePort(input, nil); err != nil {
		return err
	}
	if input.MixedPort < 1 || input.MixedPort > 65535 {
		return fmt.Errorf("代理端口必须在 1-65535 之间")
	}
	if input.Mode != "rule" && input.Mode != "global" && input.Mode != "direct" {
		return fmt.Errorf("运行模式无效")
	}
	if (strings.TrimSpace(input.PanelCertFile) == "") != (strings.TrimSpace(input.PanelKeyFile) == "") {
		return fmt.Errorf("面板 TLS 证书和私钥必须同时配置")
	}
	if strings.TrimSpace(input.PanelCertFile) != "" {
		if _, err := tls.LoadX509KeyPair(strings.TrimSpace(input.PanelCertFile), strings.TrimSpace(input.PanelKeyFile)); err != nil {
			return fmt.Errorf("面板 TLS 证书无效: %w", err)
		}
	}
	remarkModel, err := normalizeRemarkModel(input.RemarkModel)
	if err != nil {
		return err
	}
	input.RemarkModel = remarkModel
	// 数值字段留空时前端会送 0，一律当"用默认值"处理，只有明确填了不合法的数字才报错。
	if input.SessionMaxAge == 0 {
		input.SessionMaxAge = defaultSessionMaxAge
	}
	if input.SessionMaxAge < minSessionMaxAge {
		return fmt.Errorf("会话有效期不能小于 %d 分钟", minSessionMaxAge)
	}
	if input.PageSize < 0 || input.PageSize > maxPageSize {
		return fmt.Errorf("分页大小必须在 0-%d 之间", maxPageSize)
	}
	if input.ExpireDiff < 0 || input.TrafficDiff < 0 {
		return fmt.Errorf("到期和流量提醒阈值不能为负数")
	}
	input.ExternalTrafficInformURI = strings.TrimSpace(input.ExternalTrafficInformURI)
	if input.ExternalTrafficInformEnable {
		if !strings.HasPrefix(input.ExternalTrafficInformURI, "http://") && !strings.HasPrefix(input.ExternalTrafficInformURI, "https://") {
			return fmt.Errorf("流量上报地址必须以 http:// 或 https:// 开头")
		}
	}
	input.TimeLocation = strings.TrimSpace(input.TimeLocation)
	if input.TimeLocation == "" {
		input.TimeLocation = defaultTimeLocation
	}
	if _, err := resolveTimeLocation(input.TimeLocation); err != nil {
		return fmt.Errorf("时区无效: %w", err)
	}
	input.Language = strings.TrimSpace(input.Language)
	if input.Language == "" {
		input.Language = defaultLanguage
	}
	if !containsString(supportedLanguages, input.Language) {
		return fmt.Errorf("界面语言无效")
	}
	m.mu.Lock()
	if err := validateSubscriptionServicePort(input, m.state.Inbounds); err != nil {
		m.mu.Unlock()
		return err
	}
	// Older clients do not send this field. Preserve its saved value instead of
	// rotating the cross-panel URL whenever another panel setting is updated.
	if strings.TrimSpace(input.CrossPanelSubscriptionPath) == "" {
		input.CrossPanelSubscriptionPath = m.state.Settings.CrossPanelSubscriptionPath
		if input.CrossPanelSubscriptionPath == "" {
			input.CrossPanelSubscriptionPath, err = randomCrossPanelSubscriptionPath()
			if err != nil {
				m.mu.Unlock()
				return err
			}
		}
	}
	if err := validateCrossPanelSubscriptionPath(input); err != nil {
		m.mu.Unlock()
		return err
	}
	// 账号密码搬到 Authentication 页签单独提交了，General 页签保存时不带这两个字段，
	// 所以留空一律理解成"保持现有的"，而不是报错。
	if input.Username == "" {
		input.Username = m.state.Settings.Username
	}
	if input.Password == "" {
		input.Password = m.state.Settings.Password
	} else {
		password, err := hashPassword(input.Password)
		if err != nil {
			m.mu.Unlock()
			return err
		}
		input.Password = password
	}
	m.state.Settings = input
	err = m.saveLocked()
	m.mu.Unlock()
	return err
}

// 3x-ui 的备注分隔符清单，顺序也保持一致（下拉框按这个顺序渲染）。
var remarkSeparators = []string{" ", "-", "_", "@", ":", "~", "|", ",", ".", "/"}

var supportedLanguages = []string{"zh-CN", "en"}

// normalizeRemarkModel 校验备注模板串：首字符是分隔符，后面跟着去重后的标签字符。
// m-ui 只做 Inbound(i) + Email(e)，没有 3x-ui 的 Other(o)。
func normalizeRemarkModel(model string) (string, error) {
	if model == "" {
		return defaultRemarkModel, nil
	}
	runes := []rune(model)
	separator := string(runes[0])
	if !containsString(remarkSeparators, separator) {
		return "", fmt.Errorf("备注分隔符无效")
	}
	tokens := ""
	for _, token := range runes[1:] {
		if token != 'i' && token != 'e' {
			return "", fmt.Errorf("备注模板只支持 Inbound 和 Email 两个标签")
		}
		if !strings.ContainsRune(tokens, token) {
			tokens += string(token)
		}
	}
	return separator + tokens, nil
}

// resolveTimeLocation 把设置里的时区名解析成 *time.Location，空值和 "Local" 都用本机时区。
func resolveTimeLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "Local" {
		return time.Local, nil
	}
	return time.LoadLocation(name)
}

// validatePanelDomain 只做形状检查：域名用来拼访问链接和申请证书，不能带协议、端口或路径。
func validatePanelDomain(domain string) error {
	if domain == "" {
		return nil
	}
	if strings.ContainsAny(domain, " /\\:?#@") {
		return fmt.Errorf("面板域名只能填主机名，不要带协议、端口或路径")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$`).MatchString(domain) {
		return fmt.Errorf("面板域名格式无效")
	}
	return nil
}

// validateListenAddress 拦住少写端口或填了域名的监听地址。写成 "0.0.0.0" 时
// http.Server 下次启动会直接报 missing port in address 退出，填域名则大概率
// bind 不上，两种情况都只能手改 state.json 才能把面板救回来，所以必须提前拦下。
func validateListenAddress(address, label string) error {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return fmt.Errorf("%s必须写成 IP:端口", label)
	}
	port, err := strconv.Atoi(strings.TrimSpace(portText))
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%s的端口必须在 1-65535 之间", label)
	}
	host = strings.TrimSpace(host)
	if host == "" || host == "localhost" || net.ParseIP(host) != nil {
		return nil
	}
	return fmt.Errorf("%s只能填 IP，留空表示监听全部网卡", label)
}

// normalizeListenAddress collapses every spelling of "all interfaces" to the
// empty string so wildcard listeners compare equal to each other.
func normalizeListenAddress(listen string) string {
	listen = strings.TrimSpace(listen)
	if containsString([]string{"", "*", "0.0.0.0", "::", "[::]", "::0", "[::0]"}, listen) {
		return ""
	}
	return listen
}

// listenAddressesOverlap reports whether two listeners on the same port would
// compete for the socket. A wildcard covers every interface, so it collides with
// any specific address.
func listenAddressesOverlap(a, b string) bool {
	a, b = normalizeListenAddress(a), normalizeListenAddress(b)
	return a == b || a == "" || b == ""
}

// hostPortFromAddress splits the "host:port" spelling used by
// Settings.PanelListen and Settings.APIAddress. A bare port keeps the wildcard
// host so it collides with every inbound on that port, matching how Go resolves
// ":2053". A port of 0 means "nothing to compare against".
func hostPortFromAddress(address string) (string, int) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", 0
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		host, portText = "", address
	}
	port, err := strconv.Atoi(strings.TrimSpace(portText))
	if err != nil || port < 1 || port > 65535 {
		return "", 0
	}
	return strings.TrimSpace(host), port
}

// subscriptionListenAddress reuses the panel's bind interface and replaces
// only its port. A zero subscription port is the legacy same-listener fallback.
func subscriptionListenAddress(settings Settings) string {
	if settings.SubscriptionPort == 0 {
		return strings.TrimSpace(settings.PanelListen)
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(settings.PanelListen))
	if err != nil {
		return ""
	}
	return net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(settings.SubscriptionPort))
}

func validateSubscriptionServicePort(settings Settings, inbounds []Inbound) error {
	port := settings.SubscriptionPort
	if port < 0 || port > 65535 {
		return fmt.Errorf("订阅服务端口必须留空或在 1-65535 之间")
	}
	if port == 0 {
		return nil
	}
	address := subscriptionListenAddress(settings)
	if err := validateListenAddress(address, "订阅服务监听地址"); err != nil {
		return err
	}
	listen, _ := hostPortFromAddress(address)
	for _, reserved := range []struct {
		address string
		label   string
	}{{settings.PanelListen, "面板监听地址"}, {settings.APIAddress, "Mihomo API 地址"}} {
		host, reservedPort := hostPortFromAddress(reserved.address)
		if reservedPort == port && listenAddressesOverlap(host, listen) {
			return fmt.Errorf("订阅服务端口 %d 已被%s %s占用", port, reserved.label, strings.TrimSpace(reserved.address))
		}
	}
	for _, inbound := range inbounds {
		if inbound.Port == port && listenAddressesOverlap(inbound.Listen, listen) {
			return fmt.Errorf("订阅服务端口 %d 已被入口 %q 使用", port, inbound.Name)
		}
	}
	return nil
}

// reservedPanelPortConflict reports the label of the panel-owned socket an
// inbound would fight over. Mihomo only logs "listen err" for such a listener
// and keeps running, and `mihomo -t` does not catch it at all, so the inbound
// would silently never accept traffic — and an inbound that steals PanelListen
// locks the operator out of the panel after the next restart.
func reservedPanelPortConflict(settings Settings, listen string, port int) string {
	reservedAddresses := []struct {
		address string
		label   string
	}{{settings.PanelListen, "面板监听地址"}, {settings.APIAddress, "Mihomo API 地址"}}
	if settings.SubscriptionPort != 0 {
		reservedAddresses = append(reservedAddresses, struct {
			address string
			label   string
		}{subscriptionListenAddress(settings), "订阅服务监听地址"})
	}
	for _, reserved := range reservedAddresses {
		host, reservedPort := hostPortFromAddress(reserved.address)
		if reservedPort == 0 || reservedPort != port || !listenAddressesOverlap(host, listen) {
			continue
		}
		return fmt.Sprintf("%s %s", reserved.label, strings.TrimSpace(reserved.address))
	}
	return ""
}

// mergeClientCounters keeps the daemon's own bookkeeping when an inbound is
// saved from a panel snapshot that may be seconds old: traffic counters and the
// online timestamps belong to the server, everything else to the request. This
// mirrors what saveClient already does for the per-client endpoint.
func mergeClientCounters(existing, incoming []Client) []Client {
	if len(incoming) == 0 {
		return incoming
	}
	previous := make(map[string]Client, len(existing))
	for _, client := range existing {
		if client.ID != "" {
			previous[client.ID] = client
		}
	}
	for index := range incoming {
		stored, ok := previous[incoming[index].ID]
		if !ok {
			continue
		}
		incoming[index].Traffic = stored.Traffic
		incoming[index].LastOnline = stored.LastOnline
		incoming[index].FirstOnline = stored.FirstOnline
		if incoming[index].CreatedAt == "" {
			incoming[index].CreatedAt = stored.CreatedAt
		}
	}
	return incoming
}

func (m *CoreManager) saveInbound(input Inbound) error {
	if input.Name == "" {
		return fmt.Errorf("入口名称不能为空")
	}
	if input.Type == "" {
		return fmt.Errorf("入口类型不能为空")
	}
	if input.Listen == "" {
		input.Listen = "0.0.0.0"
	}
	if input.Port < 1 || input.Port > 65535 {
		return fmt.Errorf("端口必须在 1-65535 之间")
	}
	if input.TrafficReset == "" {
		input.TrafficReset = "never"
	}
	normalizeSingleSecretInbound(&input)
	normalizeMuxBrutal(&input)
	if err := normalizeInboundSecrets(&input); err != nil {
		return err
	}
	if err := validateInbound(input); err != nil {
		return err
	}
	if input.ID == "" {
		if err := randomToken(&input.ID); err != nil {
			return err
		}
		input.ID = input.ID[:12]
	}
	if input.CreatedAt == "" {
		input.CreatedAt = time.Now().Format(time.RFC3339)
	}
	for index := range input.Clients {
		client := &input.Clients[index]
		if client.ID == "" {
			if err := randomToken(&client.ID); err != nil {
				return err
			}
			client.ID = client.ID[:12]
		}
		if client.Name == "" {
			client.Name = valueOr(client.Username, fmt.Sprintf("client-%d", index+1))
		}
		if client.CreatedAt == "" {
			client.CreatedAt = time.Now().Format(time.RFC3339)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.state.Inbounds {
		if existing.ID == input.ID {
			continue
		}
		// parseListeners keys listeners by name, so a duplicate is not a partial
		// failure: Mihomo rejects the whole config with "listener %s is the
		// duplicate name" and nothing binds at all.
		if existing.Name == input.Name {
			return fmt.Errorf("入口名称 %q 已被占用，Mihomo 会因 listener 重名拒绝整份配置", input.Name)
		}
		if existing.Port == input.Port && listenAddressesOverlap(existing.Listen, input.Listen) {
			return fmt.Errorf("端口 %d 已被入口 %q 使用", input.Port, existing.Name)
		}
	}
	if conflict := reservedPanelPortConflict(m.state.Settings, input.Listen, input.Port); conflict != "" {
		return fmt.Errorf("端口 %d 已被%s占用", input.Port, conflict)
	}
	updated := false
	for i := range m.state.Inbounds {
		if m.state.Inbounds[i].ID == input.ID {
			input.SyncOrigin = m.state.Inbounds[i].SyncOrigin
			input.Traffic = m.state.Inbounds[i].Traffic
			if input.Clients == nil {
				input.Clients = m.state.Inbounds[i].Clients
			} else {
				input.Clients = mergeClientCounters(m.state.Inbounds[i].Clients, input.Clients)
			}
			m.state.Inbounds[i] = input
			updated = true
			break
		}
	}
	if !updated {
		m.state.Inbounds = append(m.state.Inbounds, input)
	}
	if err := m.ensureSubscriptionTokensLocked(input.Clients); err != nil {
		return err
	}
	m.pruneSubscriptionTokensLocked(m.state.Inbounds)
	return m.saveLocked()
}

func validateInbound(inbound Inbound) error {
	if inbound.CertificateMode != "" && inbound.CertificateMode != "file" && inbound.CertificateMode != "content" {
		return fmt.Errorf("证书模式无效: %s", inbound.CertificateMode)
	}
	if inbound.Type == "shadowsocks" {
		cipher := strings.ToLower(strings.TrimSpace(inbound.Cipher))
		if !containsString([]string{"aes-128-gcm", "aes-256-gcm", "chacha20-poly1305", "chacha20-ietf-poly1305", "xchacha20-ietf-poly1305", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305"}, cipher) {
			return fmt.Errorf("Shadowsocks 加密方式不受 Mihomo 支持: %s", inbound.Cipher)
		}
		if len(inbound.Clients) != 1 {
			return fmt.Errorf("Shadowsocks 仅支持一个客户端")
		}
		client := inbound.Clients[0]
		if !client.Enabled || strings.TrimSpace(client.Password) == "" {
			return fmt.Errorf("Shadowsocks 至少需要一个启用且有密码的客户端")
		}
	}
	if err := validateKcpTun(inbound); err != nil {
		return err
	}
	if inbound.Type == "snell" {
		if len(inbound.Clients) != 1 {
			return fmt.Errorf("Snell 仅支持一个客户端")
		}
		client := inbound.Clients[0]
		if !client.Enabled || strings.TrimSpace(client.Password) == "" {
			return fmt.Errorf("Snell 至少需要一个启用且填写了 PSK 的客户端")
		}
		version := snellVersion(inbound)
		if version < 1 || version > 5 {
			return fmt.Errorf("Snell 版本必须在 1-5 之间: %d", version)
		}
		mode := strings.ToLower(strings.TrimSpace(inbound.Snell.ObfsMode))
		if !containsString([]string{"", "http", "tls"}, mode) {
			return fmt.Errorf("Snell Obfs Mode 不受 Mihomo 支持: %s", inbound.Snell.ObfsMode)
		}
		if inbound.UDP && version < 3 {
			return fmt.Errorf("Snell v%d 不支持 UDP，请改用 v3 及以上版本", version)
		}
		securityModes := 0
		for _, enabled := range []bool{inbound.ShadowTLS.Enabled, inbound.RestTLS.Enabled, inbound.JLS.Enabled} {
			if enabled {
				securityModes++
			}
		}
		if securityModes > 1 {
			return fmt.Errorf("Snell 的 ShadowTLS / RestTLS / JLS 互斥，只能启用一种")
		}
		// The Mihomo client exposes obfs and the TLS wrappers through a single
		// obfs-opts.mode switch, so a listener enabling both is unreachable.
		if mode != "" && securityModes > 0 {
			return fmt.Errorf("Snell 的 Obfs 与 ShadowTLS / RestTLS / JLS 互斥，只能启用一种")
		}
	}
	if inbound.Type == "mixed" || inbound.Type == "http" || inbound.Type == "socks" {
		authEnabled := inbound.SimpleAuthEnabled || (!inbound.SimpleAuthConfigured && len(inbound.Clients) > 0)
		if authEnabled && len(enabledClients(inbound)) == 0 {
			return fmt.Errorf("已启用密码认证时，至少需要一条用户名和密码")
		}
		if authEnabled {
			for _, client := range inbound.Clients {
				if client.Enabled && (strings.TrimSpace(client.Username) == "" || client.Password == "") {
					return fmt.Errorf("用户名和密码不能为空")
				}
			}
		}
	}
	if (inbound.Type == "hysteria2" || inbound.Type == "tuic") && (inbound.Certificate == "" || inbound.PrivateKey == "") {
		return fmt.Errorf("%s 入口必须配置证书和私钥", inbound.Type)
	}
	if inbound.Type == "hysteria2" {
		if err := validateHysteria2(inbound.Hysteria); err != nil {
			return err
		}
	}
	if inbound.Type == "tuic" {
		if err := validateTUIC(inbound.TUIC); err != nil {
			return err
		}
	}
	if inbound.Type == "anytls" {
		if err := validateAnyTLS(inbound.AnyTLS); err != nil {
			return err
		}
	}
	if inbound.Type == "mieru" {
		if err := validateMieru(inbound); err != nil {
			return err
		}
	}
	if inbound.Type == "sudoku" {
		if err := validateSudoku(inbound); err != nil {
			return err
		}
	}
	if inbound.Type == "hysteria2-realm" {
		if err := validateHysteria2Realm(inbound); err != nil {
			return err
		}
	}
	if inbound.Type == "shadowquic" {
		if err := validateShadowQuic(inbound); err != nil {
			return err
		}
	}
	if inbound.Type == "trusttunnel" {
		if err := validateTrustTunnel(inbound); err != nil {
			return err
		}
	}
	// Neither listener wraps its stream in anything: mieru rolls its own
	// obfuscated transport and sudoku its own table-based one, so none of the TLS
	// keys, Reality, ShadowTLS/RestTLS/JLS or allow-insecure are declared and
	// Mihomo would silently drop them.
	if inbound.Type == "mieru" || inbound.Type == "sudoku" {
		if inbound.TLS || inbound.Certificate != "" || inbound.PrivateKey != "" {
			return fmt.Errorf("%s 入口不使用 TLS，也不需要证书", inbound.Type)
		}
		if inbound.Reality.Enabled || inbound.ShadowTLS.Enabled || inbound.RestTLS.Enabled || inbound.JLS.Enabled || inbound.TrojanSS.Enabled {
			return fmt.Errorf("%s 入口不支持 Reality / ShadowTLS / RestTLS / JLS 等传输层伪装", inbound.Type)
		}
		if inbound.AllowInsecure {
			return fmt.Errorf("%s 入口没有 allow-insecure 选项", inbound.Type)
		}
	}
	hasCertificate := inbound.Certificate != "" && inbound.PrivateKey != ""
	if inbound.TLS && !hasCertificate {
		return fmt.Errorf("TLS 入口需要同时配置证书和私钥")
	}
	if inbound.ClientAuthType != "" && !containsString([]string{"request", "require-any", "verify-if-given", "require-and-verify"}, inbound.ClientAuthType) {
		return fmt.Errorf("mTLS Client Auth Type 无效: %s", inbound.ClientAuthType)
	}
	if (inbound.ClientAuthType == "verify-if-given" || inbound.ClientAuthType == "require-and-verify") && inbound.ClientAuthCert == "" {
		return fmt.Errorf("mTLS %s 必须配置客户端 CA 证书", inbound.ClientAuthType)
	}
	if inbound.ECHKey != "" && !hasCertificate {
		return fmt.Errorf("ECH 需要同时配置证书和私钥")
	}
	// TLSServerName is panel-only: Mihomo listeners present a certificate and
	// never negotiate an SNI, so it only ever fills the sni= parameter of a
	// generated share link, which is why a bare hostname is required here.
	if name := strings.TrimSpace(inbound.TLSServerName); name != "" && strings.ContainsAny(name, " \t/@:?#&") {
		return fmt.Errorf("SNI 只能填写域名，不能包含空格或 / @ : ? # & 等字符: %s", inbound.TLSServerName)
	}
	hasSecureTransport := hasCertificate || inbound.ShadowTLS.Enabled || inbound.RestTLS.Enabled || inbound.JLS.Enabled || inbound.TrojanSS.Enabled || inbound.Reality.Enabled || inbound.TLSMirror.Enabled || inbound.AllowInsecure
	if inbound.Type == "anytls" && !hasSecureTransport {
		return fmt.Errorf("AnyTLS 必须配置证书、ShadowTLS、RestTLS，或明确启用 Allow Insecure")
	}
	if inbound.Type == "trojan" && !hasSecureTransport {
		return fmt.Errorf("Trojan 必须配置证书、Reality、ShadowTLS、RestTLS、JLS、Trojan SS，或明确启用 Allow Insecure")
	}
	if inbound.JLS.Enabled {
		// jls-config is declared by Mihomo's anytls, shadowsocks, snell, trojan,
		// vless and vmess listeners, and each of those servers actually builds a
		// JLS handshake from it; the remaining types ignore the key entirely.
		if !containsString([]string{"anytls", "shadowsocks", "snell", "trojan", "vless", "vmess"}, inbound.Type) {
			return fmt.Errorf("JLS 不支持 %s 入口", inbound.Type)
		}
		if inbound.JLS.Dest == "" || !strings.Contains(inbound.JLS.Dest, ":") {
			return fmt.Errorf("JLS 必须配置有效的目标地址，例如 example.com:443")
		}
		if inbound.JLS.Username == "" || inbound.JLS.Password == "" {
			return fmt.Errorf("JLS 必须配置用户名和密码")
		}
	}
	if inbound.TrojanSS.Enabled {
		if inbound.Type != "trojan" {
			return fmt.Errorf("Trojan SS 当前仅支持 Trojan 入口")
		}
		if !containsString([]string{"aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305"}, strings.ToLower(inbound.TrojanSS.Method)) {
			return fmt.Errorf("Trojan SS 加密方式无效: %s", inbound.TrojanSS.Method)
		}
		if inbound.TrojanSS.Password == "" {
			return fmt.Errorf("Trojan SS 必须配置密码")
		}
	}
	if inbound.TLSMirror.Enabled {
		if err := validateTLSMirror(inbound); err != nil {
			return err
		}
	}
	if inbound.Reality.Enabled && (inbound.Reality.Dest == "" || inbound.Reality.PrivateKey == "") {
		return fmt.Errorf("Reality 必须配置目标地址和私钥")
	}
	if inbound.Reality.Enabled {
		for _, shortID := range splitList(inbound.Reality.ShortIDs) {
			if len(shortID) > 16 || len(shortID)%2 != 0 {
				return fmt.Errorf("Reality Short ID %q 必须是长度不超过 16 的偶数位十六进制字符串", shortID)
			}
			if _, err := hex.DecodeString(shortID); err != nil {
				return fmt.Errorf("Reality Short ID %q 不是有效的十六进制字符串", shortID)
			}
		}
	}
	if inbound.Type == "vless" && inbound.XHTTP.Enabled {
		if inbound.XHTTP.Mode != "" && !containsString([]string{"auto", "packet-up", "stream-up", "stream-one"}, inbound.XHTTP.Mode) {
			return fmt.Errorf("XHTTP Mode 无效: %s", inbound.XHTTP.Mode)
		}
		if inbound.XHTTP.XPaddingPlacement != "" && !containsString([]string{"queryInHeader", "cookie", "header", "query"}, inbound.XHTTP.XPaddingPlacement) {
			return fmt.Errorf("XHTTP Padding Placement 无效: %s", inbound.XHTTP.XPaddingPlacement)
		}
		if inbound.XHTTP.XPaddingMethod != "" && !containsString([]string{"repeat-x", "tokenish"}, inbound.XHTTP.XPaddingMethod) {
			return fmt.Errorf("XHTTP Padding Method 无效: %s", inbound.XHTTP.XPaddingMethod)
		}
		if inbound.XHTTP.UplinkHTTPMethod != "" && !containsString([]string{"POST", "PUT", "PATCH", "DELETE"}, strings.ToUpper(inbound.XHTTP.UplinkHTTPMethod)) {
			return fmt.Errorf("XHTTP Uplink HTTP Method 无效: %s", inbound.XHTTP.UplinkHTTPMethod)
		}
		if inbound.XHTTP.SessionPlacement != "" && !containsString([]string{"path", "query", "cookie", "header"}, inbound.XHTTP.SessionPlacement) {
			return fmt.Errorf("XHTTP Session Placement 无效: %s", inbound.XHTTP.SessionPlacement)
		}
		if inbound.XHTTP.SeqPlacement != "" && !containsString([]string{"path", "query", "cookie", "header"}, inbound.XHTTP.SeqPlacement) {
			return fmt.Errorf("XHTTP Sequence Placement 无效: %s", inbound.XHTTP.SeqPlacement)
		}
		if inbound.XHTTP.UplinkDataPlacement != "" && !containsString([]string{"body", "cookie", "header"}, inbound.XHTTP.UplinkDataPlacement) {
			return fmt.Errorf("XHTTP Uplink Data Placement 无效: %s", inbound.XHTTP.UplinkDataPlacement)
		}
	}
	if inbound.Type == "vmess" {
		if inbound.MKCP.Enabled && inbound.Mekya.Enabled {
			return fmt.Errorf("VMess 的 mKCP 与 Mekya 不能同时启用")
		}
		if inbound.Mekya.Enabled && (inbound.WSPath != "" || inbound.GRPCServiceName != "") {
			return fmt.Errorf("VMess Mekya 不能与 WebSocket 或 gRPC 同时启用")
		}
		if inbound.MKCP.Enabled && (inbound.WSPath != "" || inbound.GRPCServiceName != "") {
			return fmt.Errorf("VMess mKCP 不能与 WebSocket 或 gRPC 同时启用")
		}
		if inbound.MKCP.Header != "" && !containsString([]string{"none", "srtp", "utp", "wechat-video", "dtls", "wireguard"}, inbound.MKCP.Header) {
			return fmt.Errorf("VMess mKCP Header 无效: %s", inbound.MKCP.Header)
		}
		if inbound.Mekya.KCP.Header != "" && !containsString([]string{"none", "srtp", "utp", "wechat-video", "dtls", "wireguard"}, inbound.Mekya.KCP.Header) {
			return fmt.Errorf("VMess Mekya KCP Header 无效: %s", inbound.Mekya.KCP.Header)
		}
		if inbound.Mekya.Enabled {
			mekyaURL := strings.TrimSpace(inbound.Mekya.URL)
			if mekyaURL == "" {
				return fmt.Errorf("VMess Mekya 必须配置 URL")
			}
			parsed, err := url.Parse(mekyaURL)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
				return fmt.Errorf("VMess Mekya URL 必须是完整的 HTTPS URL")
			}
		}
	}
	for _, client := range inbound.Clients {
		if !client.Enabled {
			continue
		}
		if (inbound.Type == "vmess" || inbound.Type == "vless" || inbound.Type == "tuic") && client.UUID == "" {
			return fmt.Errorf("客户端 %s 缺少 UUID", client.Name)
		}
		if inbound.Type != "vless" && inbound.Type != "vmess" && client.Password == "" {
			return fmt.Errorf("客户端 %s 缺少密码", client.Name)
		}
	}
	return validateClientIdentities(inbound)
}

// validateClientIdentities rejects clients that would share an identity inside
// one inbound. The rendered `users:` block is a map keyed by name for
// hysteria2/anytls/mieru/... and by UUID for tuic, so a duplicate does not fail
// loudly — it silently swallows one of the clients, and traffic attribution for
// the list-shaped listeners (trojan/shadowquic/trusttunnel) becomes ambiguous.
// Disabled clients are checked too: they are one toggle away from colliding.
func validateClientIdentities(inbound Inbound) error {
	if len(inbound.Clients) < 2 {
		return nil
	}
	names := map[string]bool{}
	uuids := map[string]bool{}
	usesUUID := containsString([]string{"vmess", "vless", "tuic"}, inbound.Type)
	for _, client := range inbound.Clients {
		if name := valueOr(client.Username, client.Name); name != "" {
			if names[name] {
				return fmt.Errorf("客户端名称 %q 重复，同一入口内必须唯一", name)
			}
			names[name] = true
		}
		if !usesUUID || client.UUID == "" {
			continue
		}
		if uuids[client.UUID] {
			return fmt.Errorf("客户端 UUID %s 重复，同一入口内必须唯一", client.UUID)
		}
		uuids[client.UUID] = true
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// snellVersion mirrors Mihomo's listener default (4), which differs from the
// outbound default (1); m-ui therefore always states the version explicitly.
func snellVersion(inbound Inbound) int {
	if inbound.Snell.Version == 0 {
		return 4
	}
	return inbound.Snell.Version
}

func (m *CoreManager) deleteInbound(id string) error {
	if id == "" {
		return fmt.Errorf("缺少入口 ID")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.state.Inbounds {
		if m.state.Inbounds[i].ID == id {
			m.state.Inbounds = append(m.state.Inbounds[:i], m.state.Inbounds[i+1:]...)
			m.pruneSubscriptionTokensLocked(m.state.Inbounds)
			return m.saveLocked()
		}
	}
	return fmt.Errorf("入口不存在")
}

func (m *CoreManager) renderConfig() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return renderStateConfig(m.state)
}

func renderStateConfig(state State) ([]byte, error) {
	outbounds, err := normalizeMihomoOutbounds(state.Outbounds)
	if err != nil {
		return nil, err
	}
	routingRules, err := normalizeMihomoRoutingRules(state.RoutingRules, outbounds)
	if err != nil {
		return nil, err
	}
	basics, err := normalizeMihomoBasics(effectiveMihomoBasics(state))
	if err != nil {
		return nil, err
	}
	proxies, proxyGroups, compiledRules, err := compileMihomoBasics(basics, outbounds, routingRules)
	if err != nil {
		return nil, err
	}
	config := map[string]any{
		"allow-lan":                state.Settings.AllowLAN,
		"bind-address":             "*",
		"mode":                     state.Settings.Mode,
		"log-level":                state.Settings.LogLevel,
		"ipv6":                     basics.IPv6,
		"tcp-concurrent":           basics.TCPConcurrent,
		"unified-delay":            basics.UnifiedDelay,
		"external-controller":      state.Settings.APIAddress,
		"secret":                   state.Settings.APISecret,
		"external-controller-cors": map[string]any{"allow-origins": []string{"*"}, "allow-private-network": true},
		"profile":                  map[string]any{"store-selected": true},
		"proxies":                  proxies,
		"proxy-groups":             proxyGroups,
		"rules":                    compiledRules,
	}
	listeners := make([]map[string]any, 0, len(state.Inbounds))
	rendered := make(map[string]bool, len(state.Inbounds))
	now := time.Now().UnixMilli()
	for _, inbound := range state.Inbounds {
		if !inbound.Enabled || (inbound.ExpiryTime > 0 && inbound.ExpiryTime <= now) || (inbound.Total > 0 && inbound.Traffic.Up+inbound.Traffic.Down >= inbound.Total) {
			continue
		}
		if singleSecretType(inbound.Type) && len(enabledClients(inbound)) == 0 {
			continue
		}
		listener, err := inboundConfig(inbound)
		if err != nil {
			return nil, fmt.Errorf("入口 %s: %w", inbound.Name, err)
		}
		if basics.DirectIPVersion != "dual" && listener["proxy"] == "DIRECT" {
			listener["proxy"] = basicsDirectName
		}
		// Legacy states can still hold a duplicate name from before saveInbound
		// rejected them. Mihomo would answer with "listener %s is the duplicate
		// name" and refuse the entire config, so fail here with something the
		// operator can act on instead of writing a file that kills the core.
		if rendered[inbound.Name] {
			return nil, fmt.Errorf("入口名称 %q 重复，Mihomo 会拒绝整份配置，请先改名", inbound.Name)
		}
		rendered[inbound.Name] = true
		listeners = append(listeners, listener)
	}
	config["listeners"] = listeners
	data, err := marshalYAML(config)
	if err != nil {
		return nil, err
	}
	return appendManagedMetadata(data, state.Inbounds), nil
}

func appendManagedMetadata(config []byte, inbounds []Inbound) []byte {
	var metadata strings.Builder
	for _, inbound := range inbounds {
		if inbound.Type != "vless" {
			continue
		}
		if inbound.Encryption != "" && inbound.Encryption != "none" {
			fmt.Fprintf(&metadata, "# vless-client-encryption %q: %q\n", inbound.Name, inbound.Encryption)
		}
		if inbound.Reality.PublicKey != "" {
			fmt.Fprintf(&metadata, "# reality-public-key %q: %q\n", inbound.Name, inbound.Reality.PublicKey)
		}
	}
	if metadata.Len() == 0 {
		return config
	}
	result := append([]byte(nil), config...)
	result = append(result, []byte("\n# m-ui managed client metadata; comments are ignored by Mihomo\n")...)
	result = append(result, []byte(metadata.String())...)
	return result
}

// inboundLoadsCertificate reports whether Mihomo's listener for this type loads
// the certificate / mTLS / ECH keys. hysteria2, tuic and trusttunnel always do
// (they have no `tls` switch and refuse to start without a pair), hysteria2-realm
// only when one is supplied — it serves the rendezvous API over plain HTTP
// otherwise. shadowquic is deliberately absent: JLS authenticates its handshake
// and the listener self-signs a throwaway key pair, so it declares no TLS keys.
func inboundLoadsCertificate(i Inbound) bool {
	return i.TLS || containsString([]string{"hysteria2", "hysteria2-realm", "tuic", "trusttunnel"}, i.Type)
}

func inboundConfig(i Inbound) (map[string]any, error) {
	allowed := map[string]bool{"mixed": true, "socks": true, "http": true, "redir": true, "tproxy": true, "shadowsocks": true, "snell": true, "vmess": true, "vless": true, "trojan": true, "hysteria2": true, "hysteria2-realm": true, "tuic": true, "shadowquic": true, "anytls": true, "mieru": true, "sudoku": true, "trusttunnel": true}
	if !allowed[i.Type] {
		return nil, fmt.Errorf("不支持的类型 %q", i.Type)
	}
	result := map[string]any{"name": i.Name, "type": i.Type, "listen": i.Listen, "port": i.Port}
	if i.Rule != "" {
		result["rule"] = i.Rule
	}
	if i.Proxy != "" {
		result["proxy"] = i.Proxy
	}
	if i.RoutingMark != 0 {
		result["routing-mark"] = i.RoutingMark
	}
	if i.UDP && (i.Type == "mixed" || i.Type == "socks" || i.Type == "shadowsocks" || i.Type == "snell") {
		result["udp"] = true
	}
	clients := enabledClients(i)
	switch i.Type {
	case "mixed", "socks", "http":
		authEnabled := i.SimpleAuthEnabled || (!i.SimpleAuthConfigured && len(clients) > 0)
		if authEnabled {
			users := make([]map[string]any, 0, len(clients))
			for _, client := range clients {
				users = append(users, map[string]any{"username": valueOr(client.Username, client.Name), "password": client.Password})
			}
			if len(users) > 0 {
				result["users"] = users
			} else if i.Username != "" && i.Password != "" {
				result["users"] = []map[string]any{{"username": i.Username, "password": i.Password}}
			} else {
				result["users"] = []map[string]any{}
			}
		} else if i.Username != "" && i.Password != "" {
			result["users"] = []map[string]any{{"username": i.Username, "password": i.Password}}
		} else {
			result["users"] = []map[string]any{}
		}
	case "shadowsocks":
		if len(i.Clients) > 1 {
			return nil, fmt.Errorf("Shadowsocks 仅支持一个客户端")
		}
		password := ""
		if len(clients) == 1 {
			password = strings.TrimSpace(clients[0].Password)
		}
		result["password"] = password
		result["cipher"] = valueOr(i.Cipher, "2022-blake3-aes-256-gcm")
		if i.SimpleObfs.Enabled {
			result["simple-obfs"] = map[string]any{"enable": true, "mode": valueOr(i.SimpleObfs.Mode, "http")}
		}
		if i.KcpTun.Enabled {
			result["kcp-tun"] = kcpTunConfigMap(i.KcpTun)
		}
	case "snell":
		if len(i.Clients) > 1 {
			return nil, fmt.Errorf("Snell 仅支持一个客户端")
		}
		psk := ""
		if len(clients) == 1 {
			psk = strings.TrimSpace(clients[0].Password)
		}
		result["psk"] = psk
		result["version"] = snellVersion(i)
		if mode := strings.ToLower(strings.TrimSpace(i.Snell.ObfsMode)); mode != "" {
			obfs := map[string]any{"mode": mode}
			if host := strings.TrimSpace(i.Snell.ObfsHost); host != "" {
				obfs["host"] = host
			}
			result["obfs-opts"] = obfs
		}
	case "vmess":
		users := make([]map[string]any, 0, len(clients))
		for _, client := range clients {
			users = append(users, map[string]any{"username": valueOr(client.Username, client.Name), "uuid": client.UUID, "alterId": client.AlterID})
		}
		if len(users) == 0 {
			users = append(users, map[string]any{"username": valueOr(i.Username, "m-ui"), "uuid": valueOr(i.UUID, "00000000-0000-0000-0000-000000000001"), "alterId": 0})
		}
		result["users"] = users
	case "vless":
		users := make([]map[string]any, 0, len(clients))
		for _, client := range clients {
			users = append(users, map[string]any{"username": valueOr(client.Username, client.Name), "uuid": client.UUID, "flow": client.Flow})
		}
		if len(users) == 0 {
			users = append(users, map[string]any{"username": valueOr(i.Username, "m-ui"), "uuid": valueOr(i.UUID, "00000000-0000-0000-0000-000000000001")})
		}
		result["users"] = users
	case "trojan":
		result["users"] = clientUserList(clients, valueOr(i.Username, "m-ui"), i.Password)
	case "hysteria2":
		result["users"] = clientMap(clients, valueOr(i.Username, "m-ui"), i.Password)
	case "hysteria2-realm":
		// No users at all: the rendezvous API authenticates with a bearer token and
		// the listener never even looks at the tunnel it is handed.
		applyHysteria2RealmConfig(result, i.Hysteria2Realm)
	case "tuic":
		users := map[string]any{}
		for _, client := range clients {
			users[valueOr(client.UUID, valueOr(client.Username, client.Name))] = client.Password
		}
		if len(users) == 0 {
			users[valueOr(i.UUID, valueOr(i.Username, "m-ui"))] = i.Password
		}
		result["users"] = users
	case "shadowquic":
		result["users"] = clientUserList(clients, valueOr(i.Username, "m-ui"), i.Password)
		applyShadowQuicConfig(result, i.ShadowQuic)
	case "anytls":
		result["users"] = clientMap(clients, valueOr(i.Username, "m-ui"), i.Password)
	case "mieru":
		// Mieru's transport is a hard TCP/UDP enum that also decides whether the
		// socket is a stream or a packet listener, so it is always written.
		result["transport"] = strings.ToUpper(strings.TrimSpace(i.Mieru.Transport))
		result["users"] = clientMap(clients, valueOr(i.Username, "m-ui"), i.Password)
		if pattern := strings.TrimSpace(i.Mieru.TrafficPattern); pattern != "" {
			result["traffic-pattern"] = pattern
		}
		if i.Mieru.UserHintIsMandatory {
			result["user-hint-is-mandatory"] = true
		}
	case "sudoku":
		if len(i.Clients) > 1 {
			return nil, fmt.Errorf("Sudoku 仅支持一个客户端")
		}
		key := ""
		if len(clients) == 1 {
			key = strings.TrimSpace(clients[0].Password)
		}
		result["key"] = key
		applySudokuConfig(result, i.Sudoku)
	case "trusttunnel":
		result["users"] = clientUserList(clients, valueOr(i.Username, "m-ui"), i.Password)
		applyTrustTunnelConfig(result, i.TrustTunnel)
	}
	if inboundLoadsCertificate(i) && i.Certificate != "" && i.PrivateKey != "" {
		result["certificate"] = i.Certificate
		result["private-key"] = i.PrivateKey
	}
	if i.WSPath != "" && (i.Type == "vmess" || i.Type == "vless" || i.Type == "trojan") {
		result["ws-path"] = i.WSPath
	}
	if i.GRPCServiceName != "" && (i.Type == "vmess" || i.Type == "vless" || i.Type == "trojan") {
		result["grpc-service-name"] = i.GRPCServiceName
	}
	if i.Type == "vless" && i.Decryption != "" {
		result["decryption"] = i.Decryption
	}
	if inboundLoadsCertificate(i) && i.ClientAuthType != "" {
		result["client-auth-type"] = i.ClientAuthType
	}
	if inboundLoadsCertificate(i) && i.ClientAuthCert != "" {
		result["client-auth-cert"] = i.ClientAuthCert
	}
	if inboundLoadsCertificate(i) && i.ECHKey != "" {
		result["ech-key"] = i.ECHKey
	}
	// Only Mihomo's anytls, trojan and vless listeners declare allow-insecure;
	// on hysteria2/tuic the key does not exist, so there AllowInsecure stays
	// panel-side and only feeds the insecure= parameter of share links.
	if i.AllowInsecure && containsString([]string{"anytls", "trojan", "vless"}, i.Type) {
		result["allow-insecure"] = true
	}
	if i.Reality.Enabled {
		result["reality-config"] = realityConfigMap(i.Reality)
	}
	// mux-option is declared by Mihomo's hysteria2, shadowquic, shadowsocks,
	// sudoku, trojan, tuic, vless and vmess listeners; anytls, mieru, snell,
	// trusttunnel and the plain socks/http family build their sing handler without
	// a MuxOption and ignore the key.
	if (i.Mux.Padding || i.Mux.BrutalEnabled) && containsString([]string{"hysteria2", "shadowquic", "shadowsocks", "sudoku", "trojan", "tuic", "vless", "vmess"}, i.Type) {
		result["mux-option"] = map[string]any{"padding": i.Mux.Padding, "brutal": map[string]any{"enabled": i.Mux.BrutalEnabled, "up": i.Mux.Up, "down": i.Mux.Down}}
	}
	if i.ShadowTLS.Enabled {
		result["shadow-tls"] = map[string]any{"enable": true, "version": i.ShadowTLS.Version, "password": i.ShadowTLS.Password, "handshake": map[string]any{"dest": i.ShadowTLS.Dest}}
	}
	if i.RestTLS.Enabled {
		result["res-tls"] = map[string]any{"enable": true, "dest": i.RestTLS.Dest, "password": i.RestTLS.Password, "restls-script": i.RestTLS.Script, "min-record-len": i.RestTLS.MinRecordLen, "rate-limit": i.RestTLS.RateLimit, "proxy": i.RestTLS.Proxy}
	}
	if i.JLS.Enabled {
		users := []map[string]any{{"username": i.JLS.Username, "password": i.JLS.Password}}
		if i.JLS.Username == "" && i.JLS.Password == "" {
			users = nil
		}
		jls := map[string]any{"enable": true, "dest": i.JLS.Dest}
		if len(users) > 0 {
			jls["users"] = users
		}
		if i.JLS.SNI != "" {
			jls["sni"] = i.JLS.SNI
		}
		if alpn := splitList(i.JLS.ALPN); len(alpn) > 0 {
			jls["alpn"] = alpn
		}
		if i.JLS.Proxy != "" {
			jls["proxy"] = i.JLS.Proxy
		}
		if i.JLS.RateLimit != 0 {
			jls["rate-limit"] = i.JLS.RateLimit
		}
		result["jls-config"] = jls
	}
	if i.TrojanSS.Enabled {
		result["ss-option"] = map[string]any{"enabled": true, "method": valueOr(i.TrojanSS.Method, "aes-128-gcm"), "password": i.TrojanSS.Password}
	}
	if i.XHTTP.Enabled && i.Type == "vless" {
		applyXHTTPConfig(result, i.XHTTP)
	}
	if i.Type == "hysteria2" {
		applyHysteriaConfig(result, i.Hysteria)
	}
	if i.Type == "tuic" {
		applyTUICConfig(result, i.TUIC)
	}
	if i.Type == "anytls" {
		if scheme := normalizePaddingScheme(i.AnyTLS.PaddingScheme); scheme != "" {
			result["padding-scheme"] = scheme
		}
	}
	if i.Type == "vmess" {
		// tlsmirror is a security mode rather than a transport: it never sets
		// tcpOnlySecurityMode, so it happily coexists with WebSocket, gRPC, mKCP and
		// Mekya underneath.
		if i.TLSMirror.Enabled {
			result["tlsmirror-config"] = tlsMirrorConfigMap(i.TLSMirror)
		}
		if i.MKCP.Enabled {
			config := mkcpConfigMap(i.MKCP)
			config["enable"] = true
			result["mkcp-config"] = config
		}
		if i.Mekya.Enabled {
			config := map[string]any{"enable": true}
			if i.Mekya.URL != "" {
				config["url"] = i.Mekya.URL
			}
			if i.Mekya.H2PoolSize != 0 {
				config["h2-pool-size"] = i.Mekya.H2PoolSize
			}
			if i.Mekya.MaxWriteDelay != 0 {
				config["max-write-delay"] = i.Mekya.MaxWriteDelay
			}
			if i.Mekya.MaxRequestSize != 0 {
				config["max-request-size"] = i.Mekya.MaxRequestSize
			}
			if i.Mekya.PollingIntervalInitial != 0 {
				config["polling-interval-initial"] = i.Mekya.PollingIntervalInitial
			}
			if i.Mekya.MaxWriteSize != 0 {
				config["max-write-size"] = i.Mekya.MaxWriteSize
			}
			if i.Mekya.MaxWriteDurationMs != 0 {
				config["max-write-duration-ms"] = i.Mekya.MaxWriteDurationMs
			}
			if i.Mekya.MaxSimultaneousWriteConnection != 0 {
				config["max-simultaneous-write-connection"] = i.Mekya.MaxSimultaneousWriteConnection
			}
			if i.Mekya.PacketWritingBuffer != 0 {
				config["packet-writing-buffer"] = i.Mekya.PacketWritingBuffer
			}
			if kcp := mkcpConfigMap(i.Mekya.KCP); len(kcp) > 0 {
				config["kcp"] = kcp
			}
			result["mekya-config"] = config
		}
	}
	if i.TLS && (i.Certificate == "" || i.PrivateKey == "") {
		return nil, fmt.Errorf("TLS 入口需要同时配置证书和私钥")
	}
	return result, nil
}

func realityConfigMap(settings RealitySettings) map[string]any {
	result := map[string]any{
		"dest":         settings.Dest,
		"private-key":  settings.PrivateKey,
		"short-id":     splitList(settings.ShortIDs),
		"server-names": splitList(settings.ServerNames),
	}
	if settings.MaxTimeDifference != 0 {
		result["max-time-difference"] = settings.MaxTimeDifference
	}
	if settings.Proxy != "" {
		result["proxy"] = settings.Proxy
	}
	if fallback := realityFallbackMap(settings.LimitFallbackUploadAfterBytes, settings.LimitFallbackUploadBytesPerSec, settings.LimitFallbackUploadBurstBytesPerSec); len(fallback) > 0 {
		result["limit-fallback-upload"] = fallback
	}
	if fallback := realityFallbackMap(settings.LimitFallbackDownloadAfterBytes, settings.LimitFallbackDownloadBytesPerSec, settings.LimitFallbackDownloadBurstBytesPerSec); len(fallback) > 0 {
		result["limit-fallback-download"] = fallback
	}
	return result
}

func realityFallbackMap(afterBytes, bytesPerSec, burstBytesPerSec uint64) map[string]any {
	result := map[string]any{}
	if afterBytes != 0 {
		result["after-bytes"] = afterBytes
	}
	if bytesPerSec != 0 {
		result["bytes-per-sec"] = bytesPerSec
	}
	if burstBytesPerSec != 0 {
		result["burst-bytes-per-sec"] = burstBytesPerSec
	}
	return result
}

// kcpTunConfigMap renders the shadowsocks `kcp-tun` block. Key, crypt and mode
// are always written out: the kernel silently falls back to "it's a secrect",
// aes and fast, and all three have to be repeated verbatim on the client, so the
// panel keeps them in the config instead of leaving them implicit. The manual
// mode is the only one where nodelay/interval/resend/nc survive FillDefaults,
// hence they are pinned there and dropped everywhere else.
func kcpTunConfigMap(settings KcpTunSettings) map[string]any {
	mode := strings.ToLower(strings.TrimSpace(valueOr(strings.TrimSpace(settings.Mode), kcpTunDefaultMode)))
	config := map[string]any{
		"enable": true,
		"key":    strings.TrimSpace(settings.Key),
		"crypt":  strings.ToLower(valueOr(strings.TrimSpace(settings.Crypt), kcpTunDefaultCrypt)),
		"mode":   mode,
	}
	for key, value := range map[string]int{
		"mtu": settings.MTU, "sndwnd": settings.SndWnd, "rcvwnd": settings.RcvWnd,
		"datashard": settings.DataShard, "parityshard": settings.ParityShard, "dscp": settings.DSCP,
		"ratelimit": settings.RateLimit, "sockbuf": settings.SockBuf, "smuxver": settings.SmuxVer,
		"smuxbuf": settings.SmuxBuf, "streambuf": settings.StreamBuf, "framesize": settings.FrameSize,
		"keepalive": settings.KeepAlive,
	} {
		if value != 0 {
			config[key] = value
		}
	}
	if settings.NoComp {
		config["nocomp"] = true
	}
	if settings.AckNodelay {
		config["acknodelay"] = true
	}
	if mode == kcpTunManualMode {
		config["nodelay"] = settings.NoDelay
		config["interval"] = settings.Interval
		config["resend"] = settings.Resend
		config["nc"] = settings.NoCongestion
	}
	return config
}

func mkcpConfigMap(settings MKCPSettings) map[string]any {
	result := map[string]any{}
	if settings.MTU != 0 {
		result["mtu"] = settings.MTU
	}
	if settings.TTI != 0 {
		result["tti"] = settings.TTI
	}
	if settings.UplinkCapacity != 0 {
		result["uplink-capacity"] = settings.UplinkCapacity
	}
	if settings.DownlinkCapacity != 0 {
		result["downlink-capacity"] = settings.DownlinkCapacity
	}
	if settings.Congestion {
		result["congestion"] = true
	}
	if settings.WriteBuffer != 0 {
		result["write-buffer"] = settings.WriteBuffer
	}
	if settings.ReadBuffer != 0 {
		result["read-buffer"] = settings.ReadBuffer
	}
	if settings.Seed != "" {
		result["seed"] = settings.Seed
	}
	if settings.Header != "" {
		result["header"] = settings.Header
	}
	return result
}

// tlsMirrorRecommendedCipherSuites is transport/tlsmirror's
// RecommendedExplicitNonceCipherSuites: the TLS 1.2 suites whose records carry an
// explicit nonce. Nothing in the kernel applies the list on its own — an empty
// explicit-nonce-ciphersuites means "treat none of them that way" — so the panel
// only offers it as a one-click filler, and a unit test pins the drawer's copy of
// the list to this one.
var tlsMirrorRecommendedCipherSuites = []int{
	156, 157, 158, 159, 160, 161, 162, 163, 164, 165, 166, 167, 168, 169, 170, 171,
	172, 173, 49195, 49196, 49197, 49198, 49199, 49200, 49201, 49202, 49290,
	49291, 49293, 49316, 49317, 49318, 49319, 49320, 49321, 49322, 49323,
	49324, 49325, 49326, 49327, 52392, 52393, 52394, 52395, 52396, 52397,
	52398,
}

// validateTLSMirror front-runs the checks listener/sing_vmess/server.go and
// transport/tlsmirror perform while starting a mirrored VMess listener.
func validateTLSMirror(inbound Inbound) error {
	settings := inbound.TLSMirror
	// tlsmirror-config is declared by the vmess listener and by nothing else.
	if inbound.Type != "vmess" {
		return fmt.Errorf("TLSMirror 不支持 %s 入口", inbound.Type)
	}
	// securityModes counts tlsmirror as soon as the primary key is non-empty and
	// server.go refuses to start with more than one wrapper configured.
	if inbound.TLS || inbound.Certificate != "" || inbound.PrivateKey != "" {
		return fmt.Errorf("TLSMirror 借用真实 TLS 服务器的握手，不能同时配置证书和私钥")
	}
	if inbound.Reality.Enabled || inbound.ShadowTLS.Enabled || inbound.RestTLS.Enabled || inbound.JLS.Enabled || inbound.TrojanSS.Enabled {
		return fmt.Errorf("TLSMirror 与 Reality / ShadowTLS / RestTLS / JLS 互斥，只能启用一种伪装")
	}
	// DecodePrimaryKey takes standard base64 and insists on exactly 32 bytes.
	key := strings.TrimSpace(settings.PrimaryKey)
	if key == "" {
		return fmt.Errorf("TLSMirror 必须配置 Primary Key")
	}
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return fmt.Errorf("TLSMirror Primary Key 必须是标准 base64: %s", settings.PrimaryKey)
	}
	if len(decoded) != 32 {
		return fmt.Errorf("TLSMirror Primary Key 解码后必须正好 32 字节，当前 %d 字节", len(decoded))
	}
	// listener/tlsmirror hands every accepted connection to inner.HandleTcp with
	// Dest, so a missing port kills each connection at runtime rather than at
	// startup — which is why the panel demands host:port despite the omitempty tag.
	dest := strings.TrimSpace(settings.Dest)
	if dest == "" {
		return fmt.Errorf("TLSMirror 必须配置载体地址，例如 www.example.com:443")
	}
	host, port, err := net.SplitHostPort(dest)
	if err != nil || host == "" {
		return fmt.Errorf("TLSMirror 载体地址必须是 host:port，例如 www.example.com:443: %s", settings.Dest)
	}
	if number, convErr := strconv.Atoi(port); convErr != nil || number < 1 || number > 65535 {
		return fmt.Errorf("TLSMirror 载体地址端口无效: %s", settings.Dest)
	}
	for _, suite := range splitList(settings.ExplicitNonceCipherSuites) {
		number, convErr := strconv.Atoi(suite)
		if convErr != nil || number < 0 || number > 65535 {
			return fmt.Errorf("TLSMirror Explicit Nonce Cipher Suite %q 必须是 0-65535 的整数", suite)
		}
	}
	return nil
}

// tlsMirrorConfigMap renders the vmess `tlsmirror-config` block. The primary key
// and the carrier address are always written because both are load-bearing; the
// rest is omitted at its zero value, which is safe here because listener/parse.go
// hands vmess a bare &IN.VmessOption{} with nothing pre-filled to restore.
func tlsMirrorConfigMap(settings TLSMirrorSettings) map[string]any {
	result := map[string]any{
		"primary-key": strings.TrimSpace(settings.PrimaryKey),
		"dest":        strings.TrimSpace(settings.Dest),
	}
	if proxy := strings.TrimSpace(settings.Proxy); proxy != "" {
		result["proxy"] = proxy
	}
	if suites := splitList(settings.ExplicitNonceCipherSuites); len(suites) > 0 {
		values := make([]any, 0, len(suites))
		for _, suite := range suites {
			number, err := strconv.Atoi(suite)
			if err != nil {
				continue
			}
			values = append(values, number)
		}
		if len(values) > 0 {
			result["explicit-nonce-ciphersuites"] = values
		}
	}
	if settings.DeferBaseNanoseconds != 0 || settings.DeferRandomNanoseconds != 0 {
		writeTime := map[string]any{}
		if settings.DeferBaseNanoseconds != 0 {
			writeTime["base-nanoseconds"] = settings.DeferBaseNanoseconds
		}
		if settings.DeferRandomNanoseconds != 0 {
			writeTime["uniform-random-multiplier-nanoseconds"] = settings.DeferRandomNanoseconds
		}
		result["defer-instance-derived-write-time"] = writeTime
	}
	if settings.TransportLayerPadding {
		result["transport-layer-padding"] = map[string]any{"enabled": true}
	}
	// The server half only ever nil-checks this block: a non-nil enrolment makes
	// WrapTunnel intercept the key-derived control host and server.go register each
	// handshake with the enrollment processor. Its two members are read exclusively
	// by the client half in transport/tlsmirror/{client,enrollment}.go, so an empty
	// mapping is the entire switch.
	if settings.ConnectionEnrolment {
		result["connection-enrolment"] = map[string]any{}
	}
	if settings.SequenceWatermarkingEnabled {
		result["sequence-watermarking-enabled"] = true
	}
	return result
}

func applyXHTTPConfig(result map[string]any, settings XHTTPSettings) {
	config := map[string]any{}
	if settings.Path != "" {
		config["path"] = settings.Path
	}
	if settings.Host != "" {
		config["host"] = settings.Host
	}
	if settings.Mode != "" && settings.Mode != "auto" {
		config["mode"] = settings.Mode
	}
	if settings.XPaddingBytes != "" {
		config["x-padding-bytes"] = settings.XPaddingBytes
	}
	if settings.XPaddingObfsMode {
		config["x-padding-obfs-mode"] = true
	}
	if settings.XPaddingKey != "" {
		config["x-padding-key"] = settings.XPaddingKey
	}
	if settings.XPaddingHeader != "" {
		config["x-padding-header"] = settings.XPaddingHeader
	}
	if settings.XPaddingPlacement != "" {
		config["x-padding-placement"] = settings.XPaddingPlacement
	}
	if settings.XPaddingMethod != "" {
		config["x-padding-method"] = settings.XPaddingMethod
	}
	if settings.UplinkHTTPMethod != "" {
		config["uplink-http-method"] = strings.ToUpper(settings.UplinkHTTPMethod)
	}
	if settings.SessionPlacement != "" {
		config["session-placement"] = settings.SessionPlacement
	}
	if settings.SessionKey != "" {
		config["session-key"] = settings.SessionKey
	}
	if settings.SeqPlacement != "" {
		config["seq-placement"] = settings.SeqPlacement
	}
	if settings.SeqKey != "" {
		config["seq-key"] = settings.SeqKey
	}
	if settings.UplinkDataPlacement != "" {
		config["uplink-data-placement"] = settings.UplinkDataPlacement
	}
	if settings.UplinkDataKey != "" {
		config["uplink-data-key"] = settings.UplinkDataKey
	}
	if settings.UplinkChunkSize != "" {
		config["uplink-chunk-size"] = settings.UplinkChunkSize
	}
	if settings.NoSSEHeader {
		config["no-sse-header"] = true
	}
	if settings.ScStreamUpServerSecs != "" {
		config["sc-stream-up-server-secs"] = settings.ScStreamUpServerSecs
	}
	if settings.ScMaxBufferedPosts != "" {
		config["sc-max-buffered-posts"] = settings.ScMaxBufferedPosts
	}
	if settings.ScMaxEachPostBytes != "" {
		config["sc-max-each-post-bytes"] = settings.ScMaxEachPostBytes
	}
	result["xhttp-config"] = config
}

func splitList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' })
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// validateHysteria2 mirrors the checks sing_hysteria2 performs while starting the
// listener, so an operator sees the problem in the panel instead of a core that
// refuses to boot. Everything Mihomo hardcodes (TLS 1.3, BBR congestion, the
// masquerade reverse-proxy host rewrite and its InsecureSkipVerify) has no knob
// here on purpose.
func validateHysteria2(settings HysteriaSettings) error {
	obfs := strings.ToLower(strings.TrimSpace(settings.Obfs))
	if !containsString([]string{"", "salamander", "gecko"}, obfs) {
		return fmt.Errorf("Hysteria2 Obfs 不受 Mihomo 支持: %s", settings.Obfs)
	}
	if obfs != "" && strings.TrimSpace(settings.ObfsPassword) == "" {
		return fmt.Errorf("Hysteria2 启用 %s 混淆时必须配置混淆密码", obfs)
	}
	if settings.ObfsMinPacketSize < 0 || settings.ObfsMaxPacketSize < 0 {
		return fmt.Errorf("Hysteria2 混淆报文长度不能为负数")
	}
	if obfs != "gecko" && (settings.ObfsMinPacketSize != 0 || settings.ObfsMaxPacketSize != 0) {
		return fmt.Errorf("Hysteria2 混淆报文长度仅 gecko 混淆支持")
	}
	if settings.ObfsMinPacketSize != 0 && settings.ObfsMaxPacketSize != 0 && settings.ObfsMinPacketSize > settings.ObfsMaxPacketSize {
		return fmt.Errorf("Hysteria2 混淆最小报文长度不能大于最大报文长度")
	}
	if masquerade := strings.TrimSpace(settings.Masquerade); masquerade != "" {
		parsed, err := url.Parse(masquerade)
		if err != nil {
			return fmt.Errorf("Hysteria2 Masquerade 不是有效的 URL: %s", settings.Masquerade)
		}
		switch parsed.Scheme {
		case "file":
			// Mihomo serves http.Dir(url.Path) and ignores the host, so a
			// two-slash file://var/www would silently publish /www instead.
			if parsed.Host != "" {
				return fmt.Errorf("Hysteria2 Masquerade 的 file:// 需要三个斜杠的绝对目录，例如 file:///var/www: %s", settings.Masquerade)
			}
			if parsed.Path == "" {
				return fmt.Errorf("Hysteria2 Masquerade 的 file:// 必须带绝对目录，例如 file:///var/www")
			}
		case "http", "https":
			if parsed.Host == "" {
				return fmt.Errorf("Hysteria2 Masquerade 的 %s:// 必须带主机名", parsed.Scheme)
			}
		default:
			return fmt.Errorf("Hysteria2 Masquerade 仅支持 file://、http:// 和 https://: %s", settings.Masquerade)
		}
	}
	if !containsString([]string{"", "conservative", "standard", "aggressive"}, strings.ToLower(strings.TrimSpace(settings.BBRProfile))) {
		return fmt.Errorf("Hysteria2 BBR Profile 无效: %s", settings.BBRProfile)
	}
	if settings.CWND < 0 {
		return fmt.Errorf("Hysteria2 Init CWND 不能为负数")
	}
	if settings.UdpMTU < 0 {
		return fmt.Errorf("Hysteria2 UDP MTU 不能为负数")
	}
	if settings.InitialStreamReceiveWindow != 0 && settings.MaxStreamReceiveWindow != 0 && settings.InitialStreamReceiveWindow > settings.MaxStreamReceiveWindow {
		return fmt.Errorf("Hysteria2 Init Stream Window 不能大于 Max Stream Window")
	}
	if settings.InitialConnectionReceiveWindow != 0 && settings.MaxConnectionReceiveWindow != 0 && settings.InitialConnectionReceiveWindow > settings.MaxConnectionReceiveWindow {
		return fmt.Errorf("Hysteria2 Init Conn Window 不能大于 Max Conn Window")
	}
	if err := validateHysteriaRealm(settings.Realm); err != nil {
		return err
	}
	return nil
}

func applyHysteriaConfig(result map[string]any, settings HysteriaSettings) {
	if obfs := strings.ToLower(strings.TrimSpace(settings.Obfs)); obfs != "" {
		result["obfs"] = obfs
	}
	if settings.ObfsPassword != "" {
		result["obfs-password"] = settings.ObfsPassword
	}
	if settings.ObfsMinPacketSize != 0 {
		result["obfs-min-packet-size"] = settings.ObfsMinPacketSize
	}
	if settings.ObfsMaxPacketSize != 0 {
		result["obfs-max-packet-size"] = settings.ObfsMaxPacketSize
	}
	if settings.Up != "" {
		result["up"] = settings.Up
	}
	if settings.Down != "" {
		result["down"] = settings.Down
	}
	if settings.IgnoreClientBandwidth {
		result["ignore-client-bandwidth"] = true
	}
	if settings.Masquerade != "" {
		result["masquerade"] = strings.TrimSpace(settings.Masquerade)
	}
	if settings.ALPN != "" {
		result["alpn"] = splitList(settings.ALPN)
	}
	if settings.UdpMTU != 0 {
		result["udp-mtu"] = settings.UdpMTU
	}
	if settings.CWND != 0 {
		result["cwnd"] = settings.CWND
	}
	if profile := strings.ToLower(strings.TrimSpace(settings.BBRProfile)); profile != "" {
		result["bbr-profile"] = profile
	}
	if settings.InitialStreamReceiveWindow != 0 {
		result["initial-stream-receive-window"] = settings.InitialStreamReceiveWindow
	}
	if settings.MaxStreamReceiveWindow != 0 {
		result["max-stream-receive-window"] = settings.MaxStreamReceiveWindow
	}
	if settings.InitialConnectionReceiveWindow != 0 {
		result["initial-connection-receive-window"] = settings.InitialConnectionReceiveWindow
	}
	if settings.MaxConnectionReceiveWindow != 0 {
		result["max-connection-receive-window"] = settings.MaxConnectionReceiveWindow
	}
	if settings.Realm.Enabled {
		result["realm-opts"] = hysteriaRealmConfigMap(settings.Realm)
	}
}

// validateHysteriaRealm front-runs what can be checked locally before the
// hysteria2 listener turns itself into a client of a rendezvous API. The realm
// client lives in github.com/metacubex/sing-quic and cannot be inspected here, so
// this only enforces the shape of the URL and of the TLS material handed to
// component/ca — everything semantic is left to the core.
func validateHysteriaRealm(settings HysteriaRealmOptions) error {
	if !settings.Enabled {
		return nil
	}
	rawURL := strings.TrimSpace(settings.ServerURL)
	if rawURL == "" {
		return fmt.Errorf("Hysteria2 Realm 必须配置 Server URL，例如 https://realm.hy2.io")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("Hysteria2 Realm Server URL 必须是完整的 http:// 或 https:// 地址: %s", settings.ServerURL)
	}
	if strings.TrimSpace(settings.Token) == "" {
		return fmt.Errorf("Hysteria2 Realm 必须配置 Token")
	}
	if strings.TrimSpace(settings.RealmID) == "" {
		return fmt.Errorf("Hysteria2 Realm 必须配置 Realm ID")
	}
	for _, server := range splitList(settings.STUNServers) {
		host, port, splitErr := net.SplitHostPort(server)
		if splitErr != nil || host == "" {
			return fmt.Errorf("Hysteria2 Realm STUN 服务器必须是 host:port，例如 stun.sip.us:3478: %s", server)
		}
		if number, convErr := strconv.Atoi(port); convErr != nil || number < 1 || number > 65535 {
			return fmt.Errorf("Hysteria2 Realm STUN 服务器端口无效: %s", server)
		}
	}
	// ca.NewFingerprintVerifier pins the certificate by SHA-256: it rejects the
	// browser names outright (those belong to client-fingerprint), then strips the
	// colons, hex-decodes the rest and demands 32 bytes.
	if fingerprint := strings.TrimSpace(settings.Fingerprint); fingerprint != "" {
		if containsString([]string{"chrome", "firefox", "safari", "ios", "android", "edge", "360", "qq", "random", "randomized"}, fingerprint) {
			return fmt.Errorf("Hysteria2 Realm Fingerprint 是证书 SHA-256 指纹，不能填浏览器指纹名: %s", settings.Fingerprint)
		}
		decoded, decodeErr := hex.DecodeString(strings.ReplaceAll(fingerprint, ":", ""))
		if decodeErr != nil {
			return fmt.Errorf("Hysteria2 Realm Fingerprint 不是有效的十六进制字符串: %s", settings.Fingerprint)
		}
		if len(decoded) != 32 {
			return fmt.Errorf("Hysteria2 Realm Fingerprint 必须是 32 字节的 SHA-256 指纹，当前 %d 字节", len(decoded))
		}
	}
	// GetTLSConfig only builds a client certificate when both halves are present,
	// so half a pair is silently ignored instead of enabling mTLS.
	certificate := strings.TrimSpace(settings.Certificate)
	privateKey := strings.TrimSpace(settings.PrivateKey)
	if (certificate == "") != (privateKey == "") {
		return fmt.Errorf("Hysteria2 Realm 的客户端证书和私钥必须同时配置")
	}
	if sni := strings.TrimSpace(settings.SNI); sni != "" && strings.ContainsAny(sni, " \t/@:?#&") {
		return fmt.Errorf("Hysteria2 Realm SNI 只能填写域名: %s", settings.SNI)
	}
	// NameCertVerify only retargets the certificate's DNSName check, so it is a
	// bare hostname just like the SNI.
	if name := strings.TrimSpace(settings.NameCertVerify); name != "" && strings.ContainsAny(name, " \t/@:?#&") {
		return fmt.Errorf("Hysteria2 Realm Name Cert Verify 只能填写域名: %s", settings.NameCertVerify)
	}
	return nil
}

// hysteriaRealmConfigMap renders the hysteria2 `realm-opts` block. `enable` is the
// only gate the kernel reads, and the three identity keys are always written
// because the panel refuses to save the block without them; the TLS keys are
// omitted at their zero value, which is safe because listener/parse.go hands
// hysteria2 a bare &IN.Hysteria2Option{} with nothing pre-filled to restore.
func hysteriaRealmConfigMap(settings HysteriaRealmOptions) map[string]any {
	result := map[string]any{
		"enable":     true,
		"server-url": strings.TrimSpace(settings.ServerURL),
		"token":      strings.TrimSpace(settings.Token),
		"realm-id":   strings.TrimSpace(settings.RealmID),
	}
	if servers := splitList(settings.STUNServers); len(servers) > 0 {
		result["stun-servers"] = servers
	}
	if sni := strings.TrimSpace(settings.SNI); sni != "" {
		result["sni"] = sni
	}
	if settings.SkipCertVerify {
		result["skip-cert-verify"] = true
	}
	if name := strings.TrimSpace(settings.NameCertVerify); name != "" {
		result["name-cert-verify"] = name
	}
	if fingerprint := strings.TrimSpace(settings.Fingerprint); fingerprint != "" {
		result["fingerprint"] = fingerprint
	}
	if certificate := strings.TrimSpace(settings.Certificate); certificate != "" {
		result["certificate"] = certificate
	}
	if privateKey := strings.TrimSpace(settings.PrivateKey); privateKey != "" {
		result["private-key"] = privateKey
	}
	if alpn := splitList(settings.ALPN); len(alpn) > 0 {
		result["alpn"] = alpn
	}
	if proxy := strings.TrimSpace(settings.Proxy); proxy != "" {
		result["proxy"] = proxy
	}
	return result
}

func applyTUICConfig(result map[string]any, settings TUICSettings) {
	if controller := strings.ToLower(strings.TrimSpace(settings.CongestionController)); controller != "" {
		result["congestion-controller"] = controller
	}
	if settings.MaxIdleTime != 0 {
		result["max-idle-time"] = settings.MaxIdleTime
	}
	if settings.AuthenticationTimeout != 0 {
		result["authentication-timeout"] = settings.AuthenticationTimeout
	}
	if settings.ALPN != "" {
		result["alpn"] = splitList(settings.ALPN)
	}
	if settings.MaxUDPRelayPacketSize != 0 {
		result["max-udp-relay-packet-size"] = settings.MaxUDPRelayPacketSize
	}
	if settings.CWND != 0 {
		result["cwnd"] = settings.CWND
	}
	if profile := strings.ToLower(strings.TrimSpace(settings.BBRProfile)); profile != "" {
		result["bbr-profile"] = profile
	}
}

// applySudokuConfig writes the sudoku listener options. aead-method, table-type
// and enable-pure-downlink are always emitted even when they equal Mihomo's
// default: all three have to match on both ends, and the client-side default is
// not guaranteed to stay in step with the listener's (the same reason Snell
// always states its version). Padding is only written when the user opted in,
// because 0 is a valid rate and would otherwise be indistinguishable from unset.
func applySudokuConfig(result map[string]any, settings SudokuSettings) {
	result["aead-method"] = valueOr(strings.ToLower(strings.TrimSpace(settings.AEADMethod)), "chacha20-poly1305")
	result["table-type"] = valueOr(strings.ToLower(strings.TrimSpace(settings.TableType)), "prefer_entropy")
	if settings.PaddingEnabled {
		result["padding-min"] = settings.PaddingMin
		result["padding-max"] = settings.PaddingMax
	}
	if settings.HandshakeTimeout > 0 {
		result["handshake-timeout"] = settings.HandshakeTimeout
	}
	result["enable-pure-downlink"] = settings.EnablePureDownlink
	// custom-tables wins over custom-table inside Mihomo, so only ever write one
	// of them: a single pattern goes to the scalar key, several to the list.
	if tables := sudokuCustomTables(settings); len(tables) == 1 {
		result["custom-table"] = tables[0]
	} else if len(tables) > 1 {
		result["custom-tables"] = tables
	}
	// The nested httpmask object is what Mihomo recommends, and its `disable`
	// field overrides the flat disable-http-mask key unconditionally, so the
	// whole trio is written as one object.
	mask := map[string]any{}
	if settings.DisableHTTPMask {
		mask["disable"] = true
	}
	if mode := strings.ToLower(strings.TrimSpace(settings.HTTPMaskMode)); mode != "" {
		mask["mode"] = mode
	}
	if root := strings.Trim(strings.TrimSpace(settings.PathRoot), "/"); root != "" {
		mask["path-root"] = root
	}
	if len(mask) > 0 {
		result["httpmask"] = mask
	}
	if fallback := strings.TrimSpace(settings.Fallback); fallback != "" {
		result["fallback"] = fallback
	}
}

// quicCongestionControllers are the only names transport/tuic/common/congestion.go
// and transport/shadowquic/congestion.go know; anything else silently leaves the
// connection on quic-go's built-in controller. The same switch backs tuic,
// shadowquic and trusttunnel, so all three share this list.
var quicCongestionControllers = []string{"cubic", "new_reno", "bbr", "bbr_meta_v1", "bbr_meta_v2"}

// quicBBRProfileControllers are the congestion controllers whose Mihomo
// implementation reads bbr-profile; bbr_meta_v1 uses the older sender and ignores it.
var quicBBRProfileControllers = []string{"bbr", "bbr_meta_v2"}

// quicCWNDControllers are the controllers that consume cwnd as an initial
// congestion window; cubic and new_reno never look at it.
var quicCWNDControllers = []string{"bbr", "bbr_meta_v1", "bbr_meta_v2"}

// validateQUICCongestion checks the congestion-controller / cwnd / bbr-profile
// trio that tuic, shadowquic and trusttunnel all declare with identical
// semantics. label prefixes the message so the operator still sees which
// protocol complained.
func validateQUICCongestion(label, controllerRaw, profileRaw string, cwnd int) error {
	controller := strings.ToLower(strings.TrimSpace(controllerRaw))
	if controller != "" && !containsString(quicCongestionControllers, controller) {
		return fmt.Errorf("%s Congestion Controller 不受 Mihomo 支持: %s", label, controllerRaw)
	}
	profile := strings.ToLower(strings.TrimSpace(profileRaw))
	if !containsString([]string{"", "conservative", "standard", "aggressive"}, profile) {
		return fmt.Errorf("%s BBR Profile 无效: %s", label, profileRaw)
	}
	if profile != "" && !containsString(quicBBRProfileControllers, controller) {
		return fmt.Errorf("%s BBR Profile 仅 bbr 与 bbr_meta_v2 拥塞控制支持", label)
	}
	if cwnd < 0 {
		return fmt.Errorf("%s Init CWND 不能为负数", label)
	}
	if cwnd != 0 && !containsString(quicCWNDControllers, controller) {
		return fmt.Errorf("%s Init CWND 仅 bbr 系列拥塞控制支持", label)
	}
	return nil
}

func validateTUIC(settings TUICSettings) error {
	if err := validateQUICCongestion("TUIC", settings.CongestionController, settings.BBRProfile, settings.CWND); err != nil {
		return err
	}
	if settings.MaxIdleTime < 0 {
		return fmt.Errorf("TUIC Max Idle Time 不能为负数")
	}
	if settings.AuthenticationTimeout < 0 {
		return fmt.Errorf("TUIC Authentication Timeout 不能为负数")
	}
	if settings.MaxUDPRelayPacketSize < 0 {
		return fmt.Errorf("TUIC Max UDP Relay Packet Size 不能为负数")
	}
	return nil
}

// anytlsDefaultPaddingScheme mirrors the scheme Mihomo compiles in at
// transport/anytls/padding/padding.go so the panel can offer it verbatim.
const anytlsDefaultPaddingScheme = `stop=8
0=30-30
1=100-400
2=400-500,c,500-1000,c,500-1000,c,500-1000,c,500-1000
3=9-9,500-1000
4=500-1000
5=500-1000
6=500-1000
7=500-1000`

// normalizePaddingScheme makes a pasted scheme parseable by Mihomo, whose
// StringMapFromBytes splits on "\n" only and keeps every other byte verbatim: a
// CRLF paste would leave "8\r" behind and fail the stop lookup.
func normalizePaddingScheme(scheme string) string {
	return strings.TrimSpace(strings.ReplaceAll(scheme, "\r", ""))
}

// validateAnyTLS rejects padding schemes that Mihomo's NewPaddingFactory would
// refuse: it needs at least one key=value line and a "stop" line holding an
// integer, otherwise the listener aborts with "incorrect padding scheme format".
// Keys and values are matched byte for byte, exactly as Mihomo reads them, so a
// stray space in "stop = 8" is reported here instead of at startup.
func validateAnyTLS(settings AnyTLSSettings) error {
	scheme := normalizePaddingScheme(settings.PaddingScheme)
	if scheme == "" {
		return nil
	}
	entries := map[string]string{}
	for _, line := range strings.Split(scheme, "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		entries[parts[0]] = parts[1]
	}
	if len(entries) == 0 {
		return fmt.Errorf("AnyTLS Padding Scheme 每行都要写成 key=value，例如 stop=8")
	}
	stop, ok := entries["stop"]
	if !ok {
		return fmt.Errorf("AnyTLS Padding Scheme 必须包含 stop=N 指明第几个包之后停止填充（不能有多余空格）")
	}
	if _, err := strconv.Atoi(stop); err != nil {
		return fmt.Errorf("AnyTLS Padding Scheme 的 stop 必须是整数: %s", stop)
	}
	return nil
}

// kcpTunCryptModes lists the ciphers Config.NewBlock actually switches on
// (transport/kcptun/common.go). An unknown name is silently downgraded to "aes"
// there, which would leave the panel showing something the tunnel never used.
// kcpTunModes covers FillDefaults' preset switch plus "manual": anything outside
// normal/fast/fast2/fast3 falls through, keeping the raw nodelay/interval/
// resend/nc values, and "manual" is the spelling upstream kcptun uses for that.
var (
	kcpTunCryptModes = []string{"aes", "aes-128", "aes-192", "aes-128-gcm", "salsa20", "blowfish", "twofish", "cast5", "3des", "tea", "xtea", "xor", "none", "null"}
	kcpTunModes      = []string{"fast", "fast2", "fast3", "normal", "manual"}
)

const (
	kcpTunDefaultCrypt     = "aes"
	kcpTunDefaultMode      = "fast"
	kcpTunManualMode       = "manual"
	kcpTunDefaultSockBuf   = 4194304
	kcpTunDefaultSmuxBuf   = 4194304
	kcpTunDefaultStreamBuf = 2097152
	kcpTunDefaultFrameSize = 8192
	kcpTunMaxSmuxVer       = 2
	kcpTunMaxMTU           = 1500
	kcpTunMinMTU           = 50
	kcpTunMaxFrameSize     = 65535
	kcpTunMaxShards        = 256
)

// kcpTunEffective resolves one numeric field the way Config.FillDefaults does,
// so the cross-field checks below compare the values the tunnel will really run
// with rather than the blanks the form left behind.
func kcpTunEffective(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

// validateKcpTun guards the shadowsocks KCP tunnel. Nothing here is reported by
// `mihomo -t`: NewServer only fills defaults, the ciphers are resolved with a
// silent fallback, and the smux parameters are verified per accepted session, so
// a bad value either changes the tunnel behind the admin's back or drops every
// client after the listener has started cleanly.
func validateKcpTun(inbound Inbound) error {
	settings := inbound.KcpTun
	if !settings.Enabled {
		return nil
	}
	if inbound.Type != "shadowsocks" {
		return fmt.Errorf("KCP Tunnel 只有 Shadowsocks 入口支持")
	}
	if strings.TrimSpace(settings.Key) == "" {
		return fmt.Errorf("KCP Tunnel 需要填写 Key，否则内核会回落到公开的默认口令")
	}
	if crypt := strings.ToLower(strings.TrimSpace(settings.Crypt)); crypt != "" && !containsString(kcpTunCryptModes, crypt) {
		return fmt.Errorf("KCP Tunnel Crypt 不受 Mihomo 支持: %s", settings.Crypt)
	}
	if mode := strings.ToLower(strings.TrimSpace(settings.Mode)); mode != "" && !containsString(kcpTunModes, mode) {
		return fmt.Errorf("KCP Tunnel Mode 不受 Mihomo 支持: %s", settings.Mode)
	}
	if inbound.SimpleObfs.Enabled {
		return fmt.Errorf("KCP Tunnel 与 simple-obfs 不能同时启用：客户端的 kcptun 插件不会再叠加 obfs")
	}
	if inbound.ShadowTLS.Enabled || inbound.RestTLS.Enabled || inbound.JLS.Enabled {
		return fmt.Errorf("KCP Tunnel 启用后监听端口只有 UDP，Shadow-TLS / RestTLS / JLS 都不会生效")
	}
	for _, field := range []struct {
		label string
		value int
	}{
		{"MTU", settings.MTU}, {"Send Window", settings.SndWnd}, {"Receive Window", settings.RcvWnd},
		{"Data Shard", settings.DataShard}, {"Parity Shard", settings.ParityShard}, {"DSCP", settings.DSCP},
		{"Rate Limit", settings.RateLimit}, {"NoDelay", settings.NoDelay}, {"Interval", settings.Interval},
		{"Resend", settings.Resend}, {"NoCongestion", settings.NoCongestion}, {"Socket Buffer", settings.SockBuf},
		{"smux Version", settings.SmuxVer}, {"smux Buffer", settings.SmuxBuf}, {"Stream Buffer", settings.StreamBuf},
		{"Frame Size", settings.FrameSize}, {"Keep Alive", settings.KeepAlive},
	} {
		if field.value < 0 {
			return fmt.Errorf("KCP Tunnel %s 不能为负数: %d", field.label, field.value)
		}
	}
	if settings.MTU != 0 && (settings.MTU < kcpTunMinMTU || settings.MTU > kcpTunMaxMTU) {
		return fmt.Errorf("KCP Tunnel MTU 必须在 %d-%d 之间: %d", kcpTunMinMTU, kcpTunMaxMTU, settings.MTU)
	}
	if settings.SmuxVer > kcpTunMaxSmuxVer {
		return fmt.Errorf("KCP Tunnel smux 版本只支持 1 或 2: %d", settings.SmuxVer)
	}
	if settings.FrameSize > kcpTunMaxFrameSize {
		return fmt.Errorf("KCP Tunnel Frame Size 不能超过 %d（smux 帧长度是 16 位）: %d", kcpTunMaxFrameSize, settings.FrameSize)
	}
	smuxBuf := kcpTunEffective(settings.SmuxBuf, kcpTunDefaultSmuxBuf)
	streamBuf := kcpTunEffective(settings.StreamBuf, kcpTunDefaultStreamBuf)
	if streamBuf > smuxBuf {
		return fmt.Errorf("KCP Tunnel Stream Buffer 不能大于 smux Buffer: %d > %d", streamBuf, smuxBuf)
	}
	shards := kcpTunEffective(settings.DataShard, 10) + kcpTunEffective(settings.ParityShard, 3)
	if shards > kcpTunMaxShards {
		return fmt.Errorf("KCP Tunnel Data Shard 与 Parity Shard 之和不能超过 %d: %d", kcpTunMaxShards, shards)
	}
	return nil
}

// mieruTransports mirrors validateMieruOption: Mihomo compares the string
// against "TCP"/"UDP" verbatim, so anything else — including lower case — makes
// the listener refuse to start.
var mieruTransports = []string{"TCP", "UDP"}

// validateMieru replicates Mihomo's validateMieruOption. The traffic pattern is
// a base64-encoded protobuf that only mieru's own mierutp.Decode can check, so
// the panel stops at the encoding: a well-formed but meaningless blob still gets
// rejected by the kernel at startup.
func validateMieru(inbound Inbound) error {
	transport := strings.ToUpper(strings.TrimSpace(inbound.Mieru.Transport))
	if transport == "" {
		return fmt.Errorf("Mieru 必须选择 Transport（TCP 或 UDP）")
	}
	if !containsString(mieruTransports, transport) {
		return fmt.Errorf("Mieru Transport 只支持 TCP 或 UDP: %s", inbound.Mieru.Transport)
	}
	clients := enabledClients(inbound)
	if len(clients) == 0 {
		return fmt.Errorf("Mieru 至少需要一个启用的客户端")
	}
	for _, client := range inbound.Clients {
		if !client.Enabled {
			continue
		}
		if strings.TrimSpace(valueOr(client.Username, client.Name)) == "" {
			return fmt.Errorf("Mieru 客户端必须填写用户名")
		}
	}
	if pattern := strings.TrimSpace(inbound.Mieru.TrafficPattern); pattern != "" {
		if _, err := base64.StdEncoding.DecodeString(pattern); err != nil {
			return fmt.Errorf("Mieru Traffic Pattern 必须是 mieru 工具导出的 base64 字符串")
		}
	}
	return nil
}

// sudokuAEADMethods, sudokuTableTypes and sudokuHTTPMaskModes list what
// transport/sudoku accepts. The legacy "ascii"/"entropy" spellings are kept
// because ASCIIMode.Canonical() still maps them onto the four current modes.
var (
	sudokuAEADMethods    = []string{"aes-128-gcm", "chacha20-poly1305", "none"}
	sudokuTableTypes     = []string{"prefer_ascii", "prefer_entropy", "up_ascii_down_entropy", "up_entropy_down_ascii", "ascii", "entropy"}
	sudokuHTTPMaskModes  = []string{"legacy", "stream", "poll", "auto", "ws"}
	sudokuTableSymbolSet = map[rune]int{'x': 2, 'p': 2, 'v': 4}
)

// validateSudoku mirrors transport/sudoku's ProtocolConfig.Validate() plus the
// checks NormalizeTableType and newCustomLayout perform. This matters more than
// for other protocols: Validate() runs inside ServerHandshake, i.e. once per
// connection, so an invalid aead-method or path-root lets the listener start
// normally and then breaks every single client instead of failing loudly.
func validateSudoku(inbound Inbound) error {
	settings := inbound.Sudoku
	if len(inbound.Clients) != 1 {
		return fmt.Errorf("Sudoku 仅支持一个客户端")
	}
	client := inbound.Clients[0]
	if !client.Enabled || strings.TrimSpace(client.Password) == "" {
		return fmt.Errorf("Sudoku 至少需要一个启用且填写了 Key 的客户端")
	}
	if method := strings.ToLower(strings.TrimSpace(settings.AEADMethod)); method != "" && !containsString(sudokuAEADMethods, method) {
		return fmt.Errorf("Sudoku AEAD Method 不受支持: %s", settings.AEADMethod)
	}
	if table := strings.ToLower(strings.TrimSpace(settings.TableType)); table != "" && !containsString(sudokuTableTypes, table) {
		return fmt.Errorf("Sudoku Table Type 不受支持: %s", settings.TableType)
	}
	if settings.PaddingEnabled {
		for label, value := range map[string]int{"Padding Min": settings.PaddingMin, "Padding Max": settings.PaddingMax} {
			if value < 0 || value > 100 {
				return fmt.Errorf("Sudoku %s 必须在 0-100 之间: %d", label, value)
			}
		}
		if settings.PaddingMax < settings.PaddingMin {
			return fmt.Errorf("Sudoku Padding Max 不能小于 Padding Min: %d < %d", settings.PaddingMax, settings.PaddingMin)
		}
	}
	if settings.HandshakeTimeout < 0 {
		return fmt.Errorf("Sudoku Handshake Timeout 不能为负数: %d", settings.HandshakeTimeout)
	}
	if mode := strings.ToLower(strings.TrimSpace(settings.HTTPMaskMode)); mode != "" && !containsString(sudokuHTTPMaskModes, mode) {
		return fmt.Errorf("Sudoku HTTP Mask Mode 不受支持: %s", settings.HTTPMaskMode)
	}
	if err := validateSudokuPathRoot(settings.PathRoot); err != nil {
		return err
	}
	for _, table := range sudokuCustomTables(settings) {
		if err := validateSudokuCustomTable(table); err != nil {
			return err
		}
	}
	if fallback := strings.TrimSpace(settings.Fallback); fallback != "" {
		host, port, err := net.SplitHostPort(fallback)
		if err != nil || host == "" || port == "" {
			return fmt.Errorf("Sudoku Fallback 必须写成 host:port，例如 127.0.0.1:8080: %s", settings.Fallback)
		}
		if _, err := strconv.Atoi(port); err != nil {
			return fmt.Errorf("Sudoku Fallback 的端口必须是数字: %s", settings.Fallback)
		}
	}
	return nil
}

// validateSudokuPathRoot follows ProtocolConfig.Validate: the value is trimmed
// of slashes and must stay a single [A-Za-z0-9_-] segment, because it becomes
// one path component of the HTTP mask endpoint.
func validateSudokuPathRoot(value string) error {
	root := strings.Trim(strings.TrimSpace(value), "/")
	if root == "" {
		if strings.TrimSpace(value) != "" {
			return fmt.Errorf("Sudoku Path Root 不能只有斜杠")
		}
		return nil
	}
	if strings.Contains(root, "/") {
		return fmt.Errorf("Sudoku Path Root 只能是一段路径，不能包含 /: %s", value)
	}
	for _, char := range root {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			continue
		}
		return fmt.Errorf("Sudoku Path Root 只能包含字母、数字、下划线和连字符: %s", value)
	}
	return nil
}

// validateSudokuCustomTable reproduces newCustomLayout: the pattern is
// lowercased with spaces stripped and must be exactly two x, two p and four v.
func validateSudokuCustomTable(table string) error {
	pattern := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(table), " ", ""))
	if pattern == "" {
		return nil
	}
	counts := map[rune]int{}
	for _, char := range pattern {
		if _, ok := sudokuTableSymbolSet[char]; !ok {
			return fmt.Errorf("Sudoku Custom Table 只能由 x / p / v 组成: %s", table)
		}
		counts[char]++
	}
	if len(pattern) != 8 {
		return fmt.Errorf("Sudoku Custom Table 必须是 8 个符号: %s", table)
	}
	for symbol, want := range sudokuTableSymbolSet {
		if counts[symbol] != want {
			return fmt.Errorf("Sudoku Custom Table 必须包含 2 个 x、2 个 p、4 个 v: %s", table)
		}
	}
	return nil
}

// sudokuCustomTables normalizes the panel's textarea into the patterns Mihomo
// reads. Empty lines are dropped so a trailing newline cannot turn into an
// entry that newCustomLayout would reject.
func sudokuCustomTables(settings SudokuSettings) []string {
	tables := make([]string, 0, len(settings.CustomTables))
	for _, table := range settings.CustomTables {
		if trimmed := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(table), " ", "")); trimmed != "" {
			tables = append(tables, trimmed)
		}
	}
	return tables
}

// hysteria2RealmDefaultNamePattern mirrors defaultRealmNamePattern in
// listener/hysteria2_realm/session.go so the panel can offer Mihomo's own regex
// verbatim (the frontend has a "↪ Default Pattern" button pinned to it by a test).
const hysteria2RealmDefaultNamePattern = `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`

// hysteria2RealmDefaultMaxRealms / …PerIP mirror hysteria2_realm.DefaultMaxRealms
// and DefaultMaxRealmsPerIP, which listener/parse.go pre-fills before decoding.
const (
	hysteria2RealmDefaultMaxRealms      = 65536
	hysteria2RealmDefaultMaxRealmsPerIP = 4
)

func validateHysteria2Realm(inbound Inbound) error {
	settings := inbound.Hysteria2Realm
	// checkRealmToken bails out on an empty expected token, so without one the
	// whole rendezvous API answers 401 and the listener is useless.
	if strings.TrimSpace(settings.Token) == "" {
		return fmt.Errorf("Hysteria2 Realm 必须配置 Token")
	}
	if settings.MaxRealms < 0 || settings.MaxRealmsPerIP < 0 {
		return fmt.Errorf("Hysteria2 Realm 的 Realm 数量上限不能为负数")
	}
	if pattern := strings.TrimSpace(settings.RealmNamePattern); pattern != "" {
		// server.go compiles the pattern before anything else and refuses to start
		// when it does not compile, so the panel rejects it up front.
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("Hysteria2 Realm Name Pattern 不是有效的正则: %v", err)
		}
	}
	if header := strings.TrimSpace(settings.TrustedProxyHeader); header != "" && strings.ContainsAny(header, " \t:") {
		return fmt.Errorf("Hysteria2 Realm Trusted Proxy Header 只能是 HTTP 头名称，例如 X-Forwarded-For: %s", settings.TrustedProxyHeader)
	}
	// The listener serves nothing but the HTTP API: it drops the tunnel it is
	// given, has no user table and never wraps the socket in anything.
	if len(inbound.Clients) > 0 {
		return fmt.Errorf("Hysteria2 Realm 是集合点 API，不需要客户端")
	}
	// inbound.UDP is deliberately not rejected: the panel hides that checkbox for
	// every protocol but mixed/socks while setInboundForm still defaults it to
	// checked, so a stray true must stay harmless. inboundConfig only renders
	// `udp` for mixed/socks/shadowsocks/snell, exactly like hysteria2 and tuic.
	if inbound.Reality.Enabled || inbound.ShadowTLS.Enabled || inbound.RestTLS.Enabled || inbound.JLS.Enabled || inbound.TrojanSS.Enabled {
		return fmt.Errorf("Hysteria2 Realm 不支持 Reality / ShadowTLS / RestTLS / JLS 等传输层伪装")
	}
	if inbound.Mux.Padding || inbound.Mux.BrutalEnabled {
		return fmt.Errorf("Hysteria2 Realm 没有 mux-option 选项")
	}
	if inbound.AllowInsecure {
		return fmt.Errorf("Hysteria2 Realm 没有 allow-insecure 选项")
	}
	return nil
}

func applyHysteria2RealmConfig(result map[string]any, settings Hysteria2RealmSettings) {
	result["token"] = strings.TrimSpace(settings.Token)
	// Both limits mean "unlimited" at 0, so they are always written: omitting them
	// would silently restore Mihomo's 65536 / 4 defaults instead.
	result["max-realms"] = settings.MaxRealms
	result["max-realms-per-ip"] = settings.MaxRealmsPerIP
	if header := strings.TrimSpace(settings.TrustedProxyHeader); header != "" {
		result["trusted-proxy-header"] = header
	}
	// Written even when empty: listener/parse.go pre-fills Mihomo's own pattern, so
	// omitting the key silently keeps that restriction, while an explicit "" is a
	// regexp that matches every realm name.
	result["realm-name-pattern"] = strings.TrimSpace(settings.RealmNamePattern)
}

// shadowQuicVersions are the canonical QUIC version names the panel offers.
// ParseQUICVersion also accepts 1/rfc9000/rfc-9000 and 2/rfc9369/rfc-9369, but
// like sudoku's legacy table aliases those are deliberately not surfaced.
var shadowQuicVersions = []string{"v1", "v2"}

func validateShadowQuic(inbound Inbound) error {
	settings := inbound.ShadowQuic
	// listener/shadowquic/server.go refuses to start without it, and the address
	// goes through socks5.ParseAddr, which needs a port.
	addr := strings.TrimSpace(settings.JLSAddr)
	if addr == "" {
		return fmt.Errorf("ShadowQuic 必须配置 JLS Upstream 地址，例如 example.com:443")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return fmt.Errorf("ShadowQuic JLS Upstream 必须是 host:port，例如 example.com:443: %s", settings.JLSAddr)
	}
	if number, convErr := strconv.Atoi(port); convErr != nil || number < 1 || number > 65535 {
		return fmt.Errorf("ShadowQuic JLS Upstream 端口无效: %s", settings.JLSAddr)
	}
	for _, version := range splitList(settings.QUICVersions) {
		if !containsString(shadowQuicVersions, strings.ToLower(version)) {
			return fmt.Errorf("ShadowQuic QUIC 版本不受支持: %s", version)
		}
	}
	if err := validateQUICCongestion("ShadowQuic", settings.CongestionController, settings.BBRProfile, settings.CWND); err != nil {
		return err
	}
	if settings.MaxIdleTime < 0 || settings.MaxDatagramFrameSize < 0 || settings.RecvWindowConn < 0 || settings.RecvWindow < 0 {
		return fmt.Errorf("ShadowQuic 的 QUIC 参数不能为负数")
	}
	// The pair is contradictory and half-broken: transport/shadowquic/server.go's
	// configureBrutalCongestion fails the negotiation outright when ReceiveBPS > 0,
	// IgnoreClientBandwidth is set and the client announces no rate, and otherwise
	// falls back to the client's rate — which is exactly what the switch says to
	// ignore. The panel refuses it up front (the drawer also clears Down).
	if settings.IgnoreClientBandwidth && strings.TrimSpace(settings.Down) != "" {
		return fmt.Errorf("ShadowQuic 忽略客户端带宽时不能再设置 Down，否则协商会失败")
	}
	if len(enabledClients(inbound)) == 0 {
		return fmt.Errorf("ShadowQuic 至少需要一个启用的客户端")
	}
	for _, client := range enabledClients(inbound) {
		if strings.TrimSpace(valueOr(client.Username, client.Name)) == "" {
			return fmt.Errorf("ShadowQuic 客户端必须有用户名")
		}
	}
	// JLS replaces the certificate entirely: the listener self-signs a throwaway
	// P-256 pair, so none of the TLS keys or wrappers are declared.
	if inbound.TLS || inbound.Certificate != "" || inbound.PrivateKey != "" {
		return fmt.Errorf("ShadowQuic 由 JLS 认证握手，不需要证书")
	}
	if inbound.Reality.Enabled || inbound.ShadowTLS.Enabled || inbound.RestTLS.Enabled || inbound.JLS.Enabled || inbound.TrojanSS.Enabled {
		return fmt.Errorf("ShadowQuic 不支持 Reality / ShadowTLS / RestTLS / JLS 等传输层伪装")
	}
	if inbound.AllowInsecure {
		return fmt.Errorf("ShadowQuic 没有 allow-insecure 选项")
	}
	return nil
}

// applyShadowQuicConfig writes the shadowquic listener options. zero-rtt is
// always emitted because listener/parse.go pre-fills the option with true, so an
// omitted key would silently switch 0-RTT back on (same reason Snell always
// states its version).
func applyShadowQuicConfig(result map[string]any, settings ShadowQuicSettings) {
	upstream := map[string]any{"addr": strings.TrimSpace(settings.JLSAddr)}
	if sni := strings.TrimSpace(settings.JLSSNI); sni != "" {
		upstream["sni"] = sni
	}
	if proxy := strings.TrimSpace(settings.JLSProxy); proxy != "" {
		upstream["proxy"] = proxy
	}
	if settings.JLSRateLimit != 0 {
		upstream["rate-limit"] = settings.JLSRateLimit
	}
	result["jls-upstream"] = upstream
	if alpn := splitList(settings.ALPN); len(alpn) > 0 {
		result["alpn"] = alpn
	}
	if versions := splitList(settings.QUICVersions); len(versions) > 0 {
		lowered := make([]string, 0, len(versions))
		for _, version := range versions {
			lowered = append(lowered, strings.ToLower(version))
		}
		result["quic-versions"] = lowered
	}
	result["zero-rtt"] = settings.ZeroRTT
	if controller := strings.ToLower(strings.TrimSpace(settings.CongestionController)); controller != "" {
		result["congestion-controller"] = controller
	}
	if settings.CWND != 0 {
		result["cwnd"] = settings.CWND
	}
	if profile := strings.ToLower(strings.TrimSpace(settings.BBRProfile)); profile != "" {
		result["bbr-profile"] = profile
	}
	if up := strings.TrimSpace(settings.Up); up != "" {
		result["up"] = up
	}
	if down := strings.TrimSpace(settings.Down); down != "" {
		result["down"] = down
	}
	if settings.IgnoreClientBandwidth {
		result["ignore-client-bandwidth"] = true
	}
	if settings.MaxIdleTime != 0 {
		result["max-idle-time"] = settings.MaxIdleTime
	}
	if settings.MaxDatagramFrameSize != 0 {
		result["max-datagram-frame-size"] = settings.MaxDatagramFrameSize
	}
	if settings.RecvWindowConn != 0 {
		result["recv-window-conn"] = settings.RecvWindowConn
	}
	if settings.RecvWindow != 0 {
		result["recv-window"] = settings.RecvWindow
	}
	if settings.DisableMTUDiscovery {
		result["disable-mtu-discovery"] = true
	}
}

// trustTunnelNetworks are the network combinations the panel offers. The kernel
// lowercases every entry and prefix-matches tcp*/udp*, so anything else creates
// no socket at all.
var trustTunnelNetworks = []string{"tcp", "udp", "tcp,udp"}

func validateTrustTunnel(inbound Inbound) error {
	settings := inbound.TrustTunnel
	// trusttunnel.New bails out with "disallow using TrustTunnel without
	// certificates config" when the pair is missing.
	if inbound.Certificate == "" || inbound.PrivateKey == "" {
		return fmt.Errorf("TrustTunnel 入口必须配置证书和私钥")
	}
	networks := splitList(settings.Network)
	if len(networks) == 0 {
		return fmt.Errorf("TrustTunnel 必须选择监听的网络类型")
	}
	tcp, udp := false, false
	for _, network := range networks {
		switch lowered := strings.ToLower(network); {
		case strings.HasPrefix(lowered, "tcp"):
			tcp = true
		case strings.HasPrefix(lowered, "udp"):
			udp = true
		default:
			return fmt.Errorf("TrustTunnel Network 只支持 tcp 和 udp: %s", network)
		}
	}
	if !tcp && !udp {
		return fmt.Errorf("TrustTunnel Network 至少要包含 tcp 或 udp")
	}
	if err := validateQUICCongestion("TrustTunnel", settings.CongestionController, settings.BBRProfile, settings.CWND); err != nil {
		return err
	}
	// The congestion trio only reaches the QUIC listener.
	if !udp && (strings.TrimSpace(settings.CongestionController) != "" || settings.CWND != 0 || strings.TrimSpace(settings.BBRProfile) != "") {
		return fmt.Errorf("TrustTunnel 的拥塞控制仅在监听 udp（QUIC）时生效")
	}
	// service.ServeHTTP answers 407 whenever verify() misses, so an empty user
	// table rejects every client.
	if len(enabledClients(inbound)) == 0 {
		return fmt.Errorf("TrustTunnel 至少需要一个启用的客户端")
	}
	for _, client := range enabledClients(inbound) {
		if strings.TrimSpace(valueOr(client.Username, client.Name)) == "" {
			return fmt.Errorf("TrustTunnel 客户端必须有用户名")
		}
	}
	// Network alone decides whether the TCP and/or QUIC socket is created; the
	// inbound-wide udp checkbox is hidden for this type and never rendered, so it
	// is left untouched rather than rejected (see validateHysteria2Realm).
	if inbound.Reality.Enabled || inbound.ShadowTLS.Enabled || inbound.RestTLS.Enabled || inbound.JLS.Enabled || inbound.TrojanSS.Enabled {
		return fmt.Errorf("TrustTunnel 不支持 Reality / ShadowTLS / RestTLS / JLS 等传输层伪装")
	}
	// sing.NewListenerHandler is built without a MuxOption here.
	if inbound.Mux.Padding || inbound.Mux.BrutalEnabled {
		return fmt.Errorf("TrustTunnel 没有 mux-option 选项")
	}
	if inbound.AllowInsecure {
		return fmt.Errorf("TrustTunnel 没有 allow-insecure 选项")
	}
	return nil
}

func applyTrustTunnelConfig(result map[string]any, settings TrustTunnelSettings) {
	networks := splitList(settings.Network)
	lowered := make([]string, 0, len(networks))
	for _, network := range networks {
		lowered = append(lowered, strings.ToLower(network))
	}
	if len(lowered) == 0 {
		lowered = append(lowered, "tcp")
	}
	result["network"] = lowered
	if controller := strings.ToLower(strings.TrimSpace(settings.CongestionController)); controller != "" {
		result["congestion-controller"] = controller
	}
	if settings.CWND != 0 {
		result["cwnd"] = settings.CWND
	}
	if profile := strings.ToLower(strings.TrimSpace(settings.BBRProfile)); profile != "" {
		result["bbr-profile"] = profile
	}
}

func enabledClients(inbound Inbound) []Client {
	clients := make([]Client, 0, len(inbound.Clients))
	now := time.Now().UnixMilli()
	for _, client := range inbound.Clients {
		if clientDepleted(client, now) {
			continue
		}
		clients = append(clients, client)
	}
	return clients
}

func clientDepleted(client Client, now int64) bool {
	return !client.Enabled || (client.ExpiryTime > 0 && client.ExpiryTime <= now) || (client.Total > 0 && client.Traffic.Up+client.Traffic.Down >= client.Total)
}

func (m *CoreManager) initializeEnforcedClientsLocked() {
	if m.enforcedClients == nil {
		m.enforcedClients = map[string]bool{}
	}
	now := time.Now().UnixMilli()
	for _, inbound := range m.state.Inbounds {
		for _, client := range inbound.Clients {
			if clientDepleted(client, now) {
				m.enforcedClients[inbound.ID+":"+client.ID] = true
			}
		}
	}
}

func clientMap(clients []Client, fallbackUser, fallbackPassword string) map[string]any {
	users := map[string]any{}
	for _, client := range clients {
		users[valueOr(client.Username, client.Name)] = client.Password
	}
	if len(users) == 0 {
		users[fallbackUser] = fallbackPassword
	}
	return users
}

// clientUserList renders the `users:` list form (a sequence of username/password
// pairs) that Mihomo's trojan, shadowquic and trusttunnel listeners expect, as
// opposed to the map form clientMap builds for hysteria2/anytls/mieru.
func clientUserList(clients []Client, fallbackUser, fallbackPassword string) []map[string]any {
	users := make([]map[string]any, 0, len(clients))
	for _, client := range clients {
		users = append(users, map[string]any{"username": valueOr(client.Username, client.Name), "password": client.Password})
	}
	if len(users) == 0 {
		users = append(users, map[string]any{"username": fallbackUser, "password": fallbackPassword})
	}
	return users
}

func (m *CoreManager) migrateStateLocked() bool {
	changed := false
	for index := range m.state.Inbounds {
		inbound := &m.state.Inbounds[index]
		_ = normalizeInboundSecrets(inbound)
		if inbound.CreatedAt == "" {
			inbound.CreatedAt = time.Now().Format(time.RFC3339)
			changed = true
		}
		if len(inbound.Clients) == 0 && (inbound.Username != "" || inbound.Password != "" || inbound.UUID != "") {
			clientID := inbound.ID + "-default"
			inbound.Clients = []Client{{ID: clientID, Name: valueOr(inbound.Username, "default"), Username: inbound.Username, Password: inbound.Password, UUID: inbound.UUID, Enabled: true, CreatedAt: inbound.CreatedAt}}
			changed = true
		}
		if singleSecretType(inbound.Type) {
			before := len(inbound.Clients)
			hadLegacyFields := inbound.Password != "" || inbound.Username != "" || inbound.UUID != ""
			normalizeSingleSecretInbound(inbound)
			if before != len(inbound.Clients) || hadLegacyFields {
				changed = true
			}
		}
		if normalizeMuxBrutal(inbound) {
			changed = true
		}
		if (inbound.Type == "mixed" || inbound.Type == "http" || inbound.Type == "socks") && len(inbound.Clients) > 0 {
			if !inbound.SimpleAuthEnabled || !inbound.SimpleAuthConfigured {
				inbound.SimpleAuthEnabled = true
				inbound.SimpleAuthConfigured = true
				changed = true
			}
		}
		for clientIndex := range inbound.Clients {
			client := &inbound.Clients[clientIndex]
			if client.ID == "" {
				client.ID = fmt.Sprintf("%s-client-%d", inbound.ID, clientIndex+1)
				changed = true
			}
			if client.Name == "" {
				client.Name = valueOr(client.Username, "client")
				changed = true
			}
			if client.CreatedAt == "" {
				client.CreatedAt = inbound.CreatedAt
				changed = true
			}
		}
	}
	if normalizeSubscriptionClientToggles(&m.state.Settings) {
		changed = true
	}
	return changed
}

// singleSecretType reports whether the Mihomo listener for this protocol holds
// exactly one shared secret and has no users list. Sudoku belongs here too: its
// `key` is a single PSK (or an ED25519 public key) shared by every client.
func singleSecretType(inboundType string) bool {
	return inboundType == "shadowsocks" || inboundType == "snell" || inboundType == "sudoku"
}

// singleSecretLabel names the shared secret each single-secret listener uses, so
// error messages talk about a PSK, a Key or a password as the user sees it.
func singleSecretLabel(inboundType string) string {
	switch inboundType {
	case "snell":
		return "PSK"
	case "sudoku":
		return "Key"
	default:
		return "密码"
	}
}

// Mihomo's Shadowsocks, Snell and Sudoku listeners carry one secret and no users
// list. Keep that secret in the sole managed client so the UI's client model
// remains useful without emitting unsupported listener configuration.
func normalizeSingleSecretInbound(inbound *Inbound) {
	if inbound == nil || !singleSecretType(inbound.Type) {
		return
	}
	if len(inbound.Clients) == 0 && strings.TrimSpace(inbound.Password) != "" {
		name := valueOr(inbound.Username, valueOr(inbound.Name, "default"))
		inbound.Clients = []Client{{Name: name, Username: name, Password: inbound.Password, Enabled: true, CreatedAt: inbound.CreatedAt}}
	}
	if len(inbound.Clients) > 1 {
		inbound.Clients = inbound.Clients[:1]
	}
	inbound.Username = ""
	inbound.Password = ""
	inbound.UUID = ""
}

// brutalHostSupported reports whether the machine running the panel can honour
// mux-option.brutal. sing-mux builds TCP Brutal on Linux congestion-control
// hooks, so a listener that asks for it on any other kernel refuses to start
// ("TCP Brutal is only supported on Linux") and takes the whole inbound down.
// A variable rather than a call so the tests can exercise both kinds of host.
var brutalHostSupported = runtime.GOOS == "linux"

// normalizeMuxBrutal drops a Brutal Mux switch this kernel cannot implement and
// reports whether anything changed, so the caller can persist the correction.
func normalizeMuxBrutal(inbound *Inbound) bool {
	if inbound == nil || brutalHostSupported {
		return false
	}
	if !inbound.Mux.BrutalEnabled && inbound.Mux.Up == "" && inbound.Mux.Down == "" {
		return false
	}
	inbound.Mux.BrutalEnabled = false
	inbound.Mux.Up = ""
	inbound.Mux.Down = ""
	return true
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func (m *CoreManager) writeConfig() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := renderStateConfig(m.state)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.dataDir, "config.yaml"), data, 0600)
}

func (m *CoreManager) actualConfig() ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(m.dataDir, "config.yaml"))
	if err == nil && len(data) > 0 {
		return data, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return m.renderConfig()
}

func (m *CoreManager) startCore() error {
	config, err := m.renderConfig()
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runningLocked() {
		return nil
	}
	configPath := filepath.Join(m.dataDir, "config.yaml")
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		return err
	}
	corePath := strings.TrimSpace(m.state.Settings.CorePath)
	if corePath == "" {
		return fmt.Errorf("请先设置 Mihomo 核心路径")
	}
	if _, err := os.Stat(corePath); err != nil {
		return fmt.Errorf("找不到 Mihomo 核心: %s", corePath)
	}
	testCmd := exec.Command(corePath, "-d", m.dataDir, "-t", "-f", configPath)
	testCmd.Dir = m.dataDir
	if output, err := testCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("Mihomo 配置测试失败: %s", strings.TrimSpace(string(output)))
	}
	cmd := exec.Command(corePath, "-d", m.dataDir, "-f", configPath)
	cmd.Dir = m.dataDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	m.cmd, m.started = cmd, time.Now()
	m.addLogLocked("Mihomo 已启动")
	go m.captureLogs(stdout)
	go m.captureLogs(stderr)
	go func() {
		err := cmd.Wait()
		m.mu.Lock()
		if m.cmd == cmd {
			m.addLogLocked("Mihomo 已退出: " + errorText(err))
			m.cmd = nil
		}
		m.mu.Unlock()
	}()
	return nil
}

func (m *CoreManager) validateConfig() error {
	if err := m.writeConfig(); err != nil {
		return err
	}
	m.mu.Lock()
	corePath := strings.TrimSpace(m.state.Settings.CorePath)
	m.mu.Unlock()
	if corePath == "" {
		return fmt.Errorf("请先设置 Mihomo 核心路径")
	}
	if _, err := os.Stat(corePath); err != nil {
		return fmt.Errorf("找不到 Mihomo 核心: %s", corePath)
	}
	cmd := exec.Command(corePath, "-d", m.dataDir, "-t", "-f", filepath.Join(m.dataDir, "config.yaml"))
	cmd.Dir = m.dataDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Mihomo 配置测试失败: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func (m *CoreManager) stopCore() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.runningLocked() {
		return nil
	}
	cmd := m.cmd
	m.cmd = nil
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		m.cmd = cmd
		return err
	}
	m.addLogLocked("Mihomo 已停止")
	_ = m.saveLocked()
	return nil
}

func (m *CoreManager) restartCore() error {
	if err := m.stopCore(); err != nil {
		return err
	}
	time.Sleep(150 * time.Millisecond)
	return m.startCore()
}

func (m *CoreManager) runningLocked() bool { return m.cmd != nil && m.cmd.ProcessState == nil }

func (m *CoreManager) coreVersion() string {
	m.mu.Lock()
	corePath := strings.TrimSpace(m.state.Settings.CorePath)
	if m.versionCache != "" && m.versionCorePath == corePath {
		version := m.versionCache
		m.mu.Unlock()
		return version
	}
	m.mu.Unlock()
	if corePath == "" {
		return "Unknown"
	}
	command := exec.Command(corePath, "-v")
	output, err := command.CombinedOutput()
	if err != nil {
		return "Unknown"
	}
	version := strings.TrimSpace(string(output))
	if newline := strings.IndexByte(version, '\n'); newline >= 0 {
		version = strings.TrimSpace(version[:newline])
	}
	version = shortCoreVersion(version)
	m.mu.Lock()
	m.versionCache = version
	m.versionCorePath = corePath
	m.mu.Unlock()
	return version
}

func shortCoreVersion(output string) string {
	for _, field := range strings.Fields(output) {
		candidate := strings.Trim(field, " ,;()[]{}")
		if len(candidate) < 2 || candidate[0] != 'v' || candidate[1] < '0' || candidate[1] > '9' {
			continue
		}
		valid := true
		for _, character := range candidate[1:] {
			if (character >= '0' && character <= '9') || character == '.' || character == '-' || character == '+' {
				continue
			}
			valid = false
			break
		}
		if valid {
			return candidate
		}
	}
	return strings.TrimSpace(output)
}

func (m *CoreManager) captureLogs(reader io.ReadCloser) {
	defer reader.Close()
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		m.mu.Lock()
		m.addLogLocked(line)
		m.mu.Unlock()
	}
}

func (m *CoreManager) addLogLocked(line string) {
	basics := effectiveMihomoBasics(m.state)
	if basics.MaskLogAddress {
		line = maskLogAddresses(line)
	}
	m.logs = append(m.logs, time.Now().Format("15:04:05")+"  "+line)
	if len(m.logs) > basics.LogBufferSize {
		m.logs = m.logs[len(m.logs)-basics.LogBufferSize:]
	}
}

func (m *CoreManager) coreGet(path string) (any, error) {
	return m.coreRequest(http.MethodGet, path)
}

func (m *CoreManager) coreRequest(method, path string) (any, error) {
	m.mu.Lock()
	address, secret := m.state.Settings.APIAddress, m.state.Settings.APISecret
	m.mu.Unlock()
	client := &http.Client{Timeout: 700 * time.Millisecond}
	req, err := http.NewRequest(method, "http://"+address+path, nil)
	if err != nil {
		return nil, err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("core api status %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNoContent {
		return map[string]any{}, nil
	}
	var data any
	err = json.NewDecoder(resp.Body).Decode(&data)
	if errors.Is(err, io.EOF) {
		err = nil
		data = map[string]any{}
	}
	return data, err
}

func randomToken(out *string) error {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	*out = hex.EncodeToString(b)
	return nil
}

func hashPassword(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("密码不能为空")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const iterations = 120000
	key := pbkdf2SHA256([]byte(password), salt, iterations, 32)
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", iterations, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return hmac.Equal([]byte(encoded), []byte(password))
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	actual := pbkdf2SHA256([]byte(password), salt, iterations, len(expected))
	return hmac.Equal(actual, expected)
}

func pbkdf2SHA256(password, salt []byte, iterations, keyLength int) []byte {
	const hashLength = 32
	blocks := (keyLength + hashLength - 1) / hashLength
	result := make([]byte, 0, blocks*hashLength)
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(sha256.New, password)
		_, _ = mac.Write(salt)
		_, _ = mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			mac = hmac.New(sha256.New, password)
			_, _ = mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		result = append(result, t...)
	}
	return result[:keyLength]
}

func errorText(err error) string {
	if err == nil {
		return "正常退出"
	}
	return err.Error()
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, payload apiResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
