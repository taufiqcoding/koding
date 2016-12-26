package kloud

import (
	"time"

	cfg "koding/kites/config"
	"koding/kites/config/configstore"
	"koding/kites/kloud/stack"
	"koding/klientctl/config"
	"koding/klientctl/ctlcli"

	"github.com/koding/kite"
	kitecfg "github.com/koding/kite/config"
	"github.com/koding/kite/protocol"
	"github.com/koding/logging"
)

var kdCacheOpts = configstore.CacheOptions("kd")

// Transport is an interface that abstracts underlying
// RPC round trip.
//
// Default implementation used in this package is
// a kiteTransport, but plain net/rpc can also be
// used.
type Transport interface {
	Call(method string, arg, reply interface{}) error
}

// DefaultLog is a logger used by Client with nil Log.
var DefaultLog logging.Logger = logging.NewCustom("endpoint-kloud", false)

// DefaultClient is a default client used by Cache, Kite,
// KiteConfig and Kloud functions.
var DefaultClient = &Client{
	Transport: &KiteTransport{},
}

// Client is responsible for communication with Kloud kite.
type Client struct {
	// Transport is used for RPC communication.
	//
	// Required.
	Transport Transport

	cache *cfg.Cache
}

func (c *Client) Cache() *cfg.Cache {
	if c.cache != nil {
		return c.cache
	}

	c.cache = cfg.NewCache(kdCacheOpts)
	ctlcli.CloseOnExit(c.cache)

	return c.cache
}

func (c *Client) Username() string {
	if kt, ok := c.Transport.(*KiteTransport); ok {
		return kt.kiteConfig().Username
	}
	return cfg.CurrentUser.Username
}

func (c *Client) Call(method string, arg, reply interface{}) error {
	return c.Transport.Call(method, arg, reply)
}

// KiteTransport is a default transport that uses github.com/koding/kite
// for underlying communication.
//
// Zero value of KiteTransport tries to connect to Kloud and Kontrol
// endpoints defined in config.Konfig (read from konfig.bolt).
type KiteTransport struct {
	// KiteName is a name of kite we're connecting to.
	//
	// If empty, "kloud" is going to be used instead.
	KiteName string

	// KiteURL is a URL of an endpoint we're going to connect to.
	//
	// If empty, public Kloud endpoint is going to be used instead.
	KiteURL string

	// KontrolURL is a URL of kontrol endpoint we're going to use
	// for GetKites if necessary.
	//
	// If empty, public Kontrol endpoint is going to be used instead.
	KontrolURL string

	// DialTimeout is a maximum time external kite is
	// going to be dialed for.
	//
	// If zero, 30s is going to be used instead.
	DialTimeout time.Duration

	// TellTimeout is a maximum time of kite's
	// request/response roundtrip.
	//
	// If zero, 60s is going to be used instead.
	TellTimeout time.Duration

	// Log is used for logging.
	//
	// If nil, DefaultLog is going to be used instead.
	Log logging.Logger

	k      *kite.Kite
	kCfg   *kitecfg.Config
	kKloud *kite.Client
}

var _ Transport = (*KiteTransport)(nil)
var _ stack.Validator = (*KiteTransport)(nil)

func (kt *KiteTransport) Call(method string, arg, reply interface{}) error {
	k, err := kt.kloud()
	if err != nil {
		return err
	}

	r, err := k.TellWithTimeout(method, kt.tellTimeout(), arg)
	if err != nil {
		return err
	}

	if reply != nil {
		return r.Unmarshal(reply)
	}

	return nil
}

func (kt *KiteTransport) kite() *kite.Kite {
	if kt.k != nil {
		return kt.k
	}

	kt.k = kite.New(config.Name, config.KiteVersion)
	kt.k.Config = kt.kiteConfig()
	kt.k.Log = kt.Log

	return kt.k
}

func (kt *KiteTransport) kiteConfig() *kitecfg.Config {
	if kt.kCfg != nil {
		return kt.kCfg
	}

	kt.kCfg = config.Konfig.KiteConfig()
	kt.kCfg.KontrolURL = kt.kontrolURL()
	kt.kCfg.Environment = config.Environment
	kt.kCfg.Transport = kitecfg.XHRPolling

	return kt.kCfg
}

func (kt *KiteTransport) kloud() (*kite.Client, error) {
	if kt.kKloud != nil {
		return kt.kKloud, nil
	}

	kloud := kt.kite().NewClient(kt.kiteURL())

	if err := kloud.DialTimeout(kt.dialTimeout()); err != nil {
		query := &protocol.KontrolQuery{
			Name:        kt.kiteName(),
			Environment: kt.kiteConfig().Environment,
		}

		clients, err := kt.kite().GetKites(query)
		if err != nil {
			return nil, err
		}

		kloud = kt.kite().NewClient(clients[0].URL)

		if err := kloud.DialTimeout(kt.DialTimeout); err != nil {
			return nil, err
		}
	}

	kt.kKloud = kloud

	if kitekey := kt.kiteConfig().KiteKey; kitekey != "" {
		kt.kKloud.Auth = &kite.Auth{
			Type: "kiteKey",
			Key:  kitekey,
		}
	}

	return kt.kKloud, nil
}

func (kt *KiteTransport) dialTimeout() time.Duration {
	if kt.DialTimeout != 0 {
		return kt.DialTimeout
	}
	return 30 * time.Second
}

func (kt *KiteTransport) tellTimeout() time.Duration {
	if kt.TellTimeout != 0 {
		return kt.TellTimeout
	}
	return 60 * time.Second
}

func (kt *KiteTransport) log() logging.Logger {
	if kt.Log != nil {
		return kt.Log
	}
	return DefaultLog
}

func (kt *KiteTransport) kiteURL() string {
	if kt.KiteURL != "" {
		return kt.KiteURL
	}
	return config.Konfig.Endpoints.Kloud().Public.String()
}

func (kt *KiteTransport) kontrolURL() string {
	if kt.KontrolURL != "" {
		return kt.KontrolURL
	}
	return config.Konfig.Endpoints.Kontrol().Public.String()
}

func (kt *KiteTransport) kiteName() string {
	if kt.KiteName != "" {
		return kt.KiteName
	}
	return "kloud"
}

func (kt *KiteTransport) Valid() error {
	// In order to test whether we're able to authenticate with kloud
	// we need to call some kite method. For that purpose we
	// use builtin "kite.print" method with empty string, since
	// this is the only nop method available.
	return kt.Call("kite.print", "", nil)
}

func Cache() *cfg.Cache { return DefaultClient.Cache() }
func Username() string  { return DefaultClient.Username() }
func Call(method string, arg, reply interface{}) error {
	return DefaultClient.Call(method, arg, reply)
}
