package mqtt

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"ism7/internal/ism7"

	paho "github.com/eclipse/paho.mqtt.golang"
)

type Options struct {
	Host     string
	Port     int
	Username string
	Password string
	ClientID string
	QoS      byte
	Retain   bool
	Separate bool // one topic per value instead of one JSON object per device
	IP       string
	// HAStatusTopic, if set, is subscribed and OnHAStatus is called for each message on it.
	HAStatusTopic string
}

// Bridge publishes ISM7 values and turns <device>/set messages into write requests.
type Bridge struct {
	opts   Options
	cfg    *ism7.Config
	client paho.Client

	// Send writes telegrams to the heat pump. Must return an error while not connected.
	Send func([]ism7.InfoWrite) error
	// OnHAStatus is called when Home Assistant announces (re)start.
	OnHAStatus func()
}

func New(opts Options, cfg *ism7.Config) *Bridge {
	return &Bridge{opts: opts, cfg: cfg}
}

func (b *Bridge) Connect() error {
	o := paho.NewClientOptions().
		AddBroker(fmt.Sprintf("tcp://%s:%d", b.opts.Host, b.opts.Port)).
		SetClientID(b.opts.ClientID).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetOrderMatters(true).
		SetOnConnectHandler(b.onConnect).
		SetConnectionLostHandler(func(_ paho.Client, err error) { slog.Warn("mqtt disconnected, reconnecting", "error", err) })
	if b.opts.Username != "" || b.opts.Password != "" {
		o.SetUsername(b.opts.Username).SetPassword(b.opts.Password)
	}
	b.client = paho.NewClient(o)
	t := b.client.Connect()
	t.Wait()
	return t.Error()
}

func (b *Bridge) Close() {
	if b.client != nil {
		b.client.Disconnect(500)
	}
}

func (b *Bridge) onConnect(c paho.Client) {
	slog.Info("mqtt connected", "host", b.opts.Host)
	// one subscription: "+/set/#" also matches "+/set", two overlapping ones deliver every message twice
	topics := []string{fmt.Sprintf("Wolf/%s/+/set/#", b.opts.IP)}
	if b.opts.HAStatusTopic != "" {
		topics = append(topics, b.opts.HAStatusTopic)
	}
	for _, t := range topics {
		c.Subscribe(t, b.opts.QoS, b.onMessage)
	}
}

// Publish sends all values that are currently pending in the config.
func (b *Bridge) Publish() {
	if b.client == nil || !b.client.IsConnectionOpen() {
		slog.Debug("mqtt not connected, dropping values")
		b.cfg.Messages(b.opts.Separate) // consume, so stale values are not sent later
		return
	}
	for _, m := range b.cfg.Messages(b.opts.Separate) {
		slog.Debug("mqtt publish", "topic", m.Topic, "payload", m.Payload)
		b.client.Publish(m.Topic, b.opts.QoS, b.opts.Retain, m.Payload)
	}
}

// PublishRaw publishes an arbitrary message (used by Home Assistant discovery).
func (b *Bridge) PublishRaw(topic, payload string, retain bool) {
	b.client.Publish(topic, b.opts.QoS, retain, payload)
}

func (b *Bridge) onMessage(_ paho.Client, m paho.Message) {
	topic, payload := m.Topic(), string(m.Payload())
	slog.Debug("mqtt received", "topic", topic, "payload", payload)
	if topic == b.opts.HAStatusTopic && b.opts.HAStatusTopic != "" {
		if b.OnHAStatus != nil {
			b.OnHAStatus()
		}
		return
	}
	writes, err := b.parse(topic, payload)
	if err != nil {
		slog.Error("invalid set message", "topic", topic, "payload", payload, "error", err)
		return
	}
	if len(writes) == 0 {
		slog.Warn("nothing to send", "topic", topic, "payload", payload)
		return
	}
	if b.Send == nil {
		return
	}
	if err := b.Send(writes); err != nil {
		slog.Error("write failed", "topic", topic, "error", err)
	}
}

func (b *Bridge) parse(topic, payload string) ([]ism7.InfoWrite, error) {
	if dev, ok := strings.CutSuffix(topic, "/set"); ok {
		return b.cfg.WriteFromJSON(dev, []byte(payload))
	}
	i := strings.LastIndex(topic, "/set/")
	if i < 0 {
		return nil, nil
	}
	return b.cfg.WriteFromParts(topic[:i], strings.Split(topic[i+5:], "/"), payload)
}
