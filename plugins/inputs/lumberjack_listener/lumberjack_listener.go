package lumberjack_listener

import (
	"crypto/tls"
	"net"
	"sync"
	"time"
	"fmt"

	"github.com/elastic/go-lumber/server/v2"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/internal"
	tlsint "github.com/influxdata/telegraf/internal/tls"
	"github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/parsers"
)

// defaultMaxBodySize is the default maximum request body size, in bytes.
// if the request body is over this size, we will return an HTTP 413 error.
// 500 MB
const defaultMaxBodySize = 500 * 1024 * 1024

// TimeFunc provides a timestamp for the metrics
type TimeFunc func() time.Time

// LumberjackListener is an input plugin that collects external metrics sent via HTTP
type LumberjackListener struct {
	ServiceAddress string            `toml:"service_address"`
	ReadTimeout    internal.Duration `toml:"read_timeout"`
	WriteTimeout   internal.Duration `toml:"write_timeout"`
	MaxBodySize    internal.Size     `toml:"max_body_size"`
	Port           int               `toml:"port"`
	TagKeys        []string

	tlsint.ServerConfig

	TimeFunc
	Log telegraf.Logger

	wg sync.WaitGroup

	listener net.Listener

	parser parsers.Parser
	acc telegraf.Accumulator
}

const sampleConfig = `
  ## Address and port to host HTTP listener on
  service_address = ":5044"

  ## maximum duration before timing out read of the request
  # read_timeout = "10s"
  ## maximum duration before timing out write of the response
  # write_timeout = "10s"

  ## Maximum allowed http request body size in bytes.
  ## 0 means to use the default of 524,288,00 bytes (500 mebibytes)
  # max_body_size = "500MB"

  ## Set one or more allowed client CA certificate file names to
  ## enable mutually authenticated TLS connections
  # tls_allowed_cacerts = ["/etc/telegraf/clientca.pem"]

  ## Add service certificate and key
  # tls_cert = "/etc/telegraf/cert.pem"
  # tls_key = "/etc/telegraf/key.pem"
`

func (h *LumberjackListener) SampleConfig() string {
	return sampleConfig
}

func (h *LumberjackListener) Description() string {
	return "Lumberjack V2 listener"
}

func (h *LumberjackListener) Gather(_ telegraf.Accumulator) error {
	return nil
}

// Start starts the http listener service.
func (h *LumberjackListener) Start(acc telegraf.Accumulator) error {
	if h.MaxBodySize.Size == 0 {
		h.MaxBodySize.Size = defaultMaxBodySize
	}

	if h.ReadTimeout.Duration < time.Second {
		h.ReadTimeout.Duration = time.Second
	}
	if h.WriteTimeout.Duration < time.Second {
		h.WriteTimeout.Duration = time.Second
	}

	h.acc = acc

	tlsConf, err := h.ServerConfig.TLSConfig()
	if err != nil {
		return err
	}

	var listener net.Listener
	if tlsConf != nil {
		listener, err = tls.Listen("tcp", h.ServiceAddress, tlsConf)
	} else {
		listener, err = net.Listen("tcp", h.ServiceAddress)
	}
	if err != nil {
		return err
	}
	h.listener = listener
	h.Port = listener.Addr().(*net.TCPAddr).Port

	parser, err := parsers.NewParser(&parsers.Config{
		DataFormat:  "json",
		MetricName:  "lumberjack",
		TagKeys:     h.TagKeys,
		JSONTimeKey: "time",
		JSONTimeFormat:  "unix",
	})
	if err != nil {
		return err
	}
	h.parser = parser

	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		server, _ := v2.NewWithListener(h.listener)
		for batch := range server.ReceiveChan() {
			batch.ACK()
			events := batch.Events
			for _, e := range events {
				fields := e.(map[string]interface{})
				metrics, err := h.parser.Parse([]byte(fmt.Sprintf("%v", fields["message"])))
				if err != nil {
					h.Log.Debugf("Parse error: %s", err.Error())
					return
				}
			
				for _, m := range metrics {
					h.acc.AddMetric(m)
				}		
			}
		}
		server.Close()
		h.Log.Infof("Stopped listening on %s", listener.Addr().String())
	}()

	h.Log.Infof("Listening on %s", listener.Addr().String())

	return nil
}

// Stop cleans up all resources
func (h *LumberjackListener) Stop() {
	h.listener.Close()
	h.wg.Wait()
}

func init() {
	inputs.Add("lumberjack_listener", func() telegraf.Input {
		return &LumberjackListener{
			ServiceAddress: ":5044",
			TimeFunc:       time.Now,
		}
	})
}
