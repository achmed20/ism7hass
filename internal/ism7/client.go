package ism7

import (
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Options struct {
	Host                string
	Port                uint16
	Password            string
	Interval            int // push interval in seconds
	StartupTimeout      time.Duration
	SerializeSubscribes bool
	Debug               bool
}

// Client talks to one ISM7. Create with NewClient and call Run; Run returns when the connection
// is lost or ctx is cancelled.
type Client struct {
	opts Options
	cfg  *Config

	// OnData is called (from the reader goroutine) after telegrams were processed and values are pending.
	OnData func()
	// OnInitialized is called once the initial values were pulled and push subscriptions were sent.
	OnInitialized func()

	conn     net.Conn
	wmu      sync.Mutex
	hmu      sync.Mutex
	handlers map[string]func(*BundleResp)
	login    chan *LoginResp
	sysconf  chan *SystemconfigResp
	lastKA   atomic.Int32
}

func NewClient(opts Options, cfg *Config) *Client {
	return &Client{opts: opts, cfg: cfg}
}

// session connects and runs fn next to the reader until either ends.
func (c *Client) session(ctx context.Context, fn func(context.Context) error) error {
	slog.Info("connecting to ISM7", "host", c.opts.Host, "port", c.opts.Port)
	conn, err := Dial(ctx, c.opts.Host, c.opts.Port)
	if err != nil {
		return err
	}
	c.conn = conn
	c.handlers = map[string]func(*BundleResp){}
	c.login = make(chan *LoginResp, 1)
	c.sysconf = make(chan *SystemconfigResp, 1)
	defer conn.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { <-ctx.Done(); conn.Close() }()

	errc := make(chan error, 2)
	go func() { errc <- c.readLoop() }()
	go func() { errc <- fn(ctx) }()

	select {
	case err = <-errc:
	case <-ctx.Done():
		err = ctx.Err()
	}
	return err
}

func (c *Client) Run(ctx context.Context) error { return c.session(ctx, c.start) }

// ReadSystemConfig logs in and returns the bus devices known to the ISM7.
func (c *Client) ReadSystemConfig(ctx context.Context) (*SystemconfigResp, error) {
	var res *SystemconfigResp
	err := c.session(ctx, func(ctx context.Context) error {
		sid, err := c.doLogin(ctx)
		if err != nil {
			return err
		}
		if err := c.send(TypeSystemconfigReq, &SystemconfigReq{Sid: sid}); err != nil {
			return err
		}
		select {
		case res = <-c.sysconf:
			return nil
		case <-time.After(c.opts.StartupTimeout):
			return errors.New("timeout waiting for system config")
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if res != nil {
		return res, nil
	}
	return nil, err
}

func (c *Client) doLogin(ctx context.Context) (string, error) {
	if err := c.send(TypeLoginReq, &LoginReq{Password: c.opts.Password}); err != nil {
		return "", err
	}
	select {
	case r := <-c.login:
		if r.State != "ok" {
			return "", fmt.Errorf("login failed: state %q %s", r.State, r.ErrorMsg)
		}
		slog.Info("connected to ISM7", "host", c.opts.Host, "port", c.opts.Port)
		return r.Sid, nil
	case <-time.After(c.opts.StartupTimeout):
		return "", errors.New("timeout waiting for login response")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// start logs in, loads the initial values, subscribes to pushes and then runs the keep-alive loop.
func (c *Client) start(ctx context.Context) error {
	if _, err := c.doLogin(ctx); err != nil {
		return err
	}
	if err := c.loadInitial(ctx); err != nil {
		return err
	}
	return c.keepAlive(ctx)
}

func (c *Client) loadInitial(ctx context.Context) error {
	var subs []Bundle
	var smu sync.Mutex
	done := make(chan error, 1)

	// Phase 1: pull one bundle at a time (firmware 5.1+ chokes on parallel pulls).
	for _, bus := range c.cfg.BusAddresses() {
		bundles, err := c.cfg.Bundles(bus)
		if err != nil {
			return err
		}
		for _, b := range bundles {
			c.handle(b.ID, false, func(r *BundleResp) {
				if err := checkResp(r); err != nil {
					done <- err
					return
				}
				has, err := c.cfg.Process(r.Reads, false)
				if err != nil {
					done <- err
					return
				}
				if has {
					c.OnData()
					smu.Lock()
					subs = append(subs, b)
					smu.Unlock()
				}
				done <- nil
			})
			err := c.request(BundleReq{BundleID: b.ID, Type: "pull", Reads: c.cfg.Prepare(b, -1)})
			if err != nil {
				return err
			}
			if err := c.wait(ctx, done); err != nil {
				return err
			}
		}
	}

	// Phase 2: push subscriptions.
	smu.Lock()
	defer smu.Unlock()
	for _, b := range subs {
		id := c.cfg.NewBundleID()
		c.handle(id, true, func(r *BundleResp) {
			if err := checkResp(r); err != nil {
				slog.Error("push response", "error", err)
			} else if has, err := c.cfg.Process(r.Reads, false); err != nil {
				slog.Error("push response", "error", err)
			} else if has {
				c.OnData()
			}
			select {
			case done <- nil:
			default:
			}
		})
		err := c.request(BundleReq{BundleID: id, Type: "push", Reads: c.cfg.Prepare(b, c.opts.Interval)})
		if err != nil {
			return err
		}
		if c.opts.SerializeSubscribes {
			if err := c.wait(ctx, done); err != nil {
				return err
			}
		}
	}
	if c.OnInitialized != nil {
		c.OnInitialized()
	}
	return nil
}

func checkResp(r *BundleResp) error {
	if r.ErrorMsg != "" {
		return errors.New(r.ErrorMsg)
	}
	if r.State != "OK" {
		return fmt.Errorf("unexpected bundle state %q", r.State)
	}
	return nil
}

func (c *Client) wait(ctx context.Context, done chan error) error {
	select {
	case err := <-done:
		return err
	case <-time.After(c.opts.StartupTimeout):
		return errors.New("timeout during startup: no response to request")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Write sends write telegrams; the response updates values and triggers OnData.
func (c *Client) Write(writes []InfoWrite) error {
	id := c.cfg.NewBundleID()
	c.handle(id, false, func(r *BundleResp) {
		if err := checkResp(r); err != nil {
			slog.Error("write failed", "error", err)
			return
		}
		for _, line := range c.cfg.DescribeWrites(r.Writes) {
			slog.Info("set command acknowledged", "bundle", r.BundleID, "ack", line)
		}
		if has, err := c.cfg.Process(r.Writes, true); err != nil {
			slog.Error("write response", "error", err)
		} else if has {
			c.OnData()
		}
	})
	return c.request(BundleReq{BundleID: id, Type: "write", AbortOnError: true, Writes: writes})
}

func (c *Client) request(r BundleReq) error {
	r.Gateway = "1"
	return c.send(TypeBundleReq, r)
}

func (c *Client) handle(id string, persistent bool, fn func(*BundleResp)) {
	c.hmu.Lock()
	defer c.hmu.Unlock()
	if !persistent {
		inner := fn
		fn = func(r *BundleResp) {
			c.hmu.Lock()
			delete(c.handlers, id)
			c.hmu.Unlock()
			inner(r)
		}
	}
	c.handlers[id] = fn
}

func (c *Client) keepAlive(ctx context.Context) error {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	var cur int16
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
		if int32(cur) != c.lastKA.Load() {
			return errors.New("no keepalive response within the last 60 seconds")
		}
		cur++
		b := binary.BigEndian.AppendUint16(nil, uint16(cur))
		if err := c.write(TypeKeepAlive, b); err != nil {
			return err
		}
	}
}

func (c *Client) send(typ uint16, v any) error {
	b, err := marshal(v)
	if err != nil {
		return err
	}
	return c.write(typ, b)
}

func (c *Client) write(typ uint16, payload []byte) error {
	if c.opts.Debug {
		slog.Debug("ism7 >", "type", typ, "payload", string(payload))
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return WriteFrame(c.conn, typ, payload)
}

func (c *Client) readLoop() error {
	for {
		typ, payload, err := ReadFrame(c.conn)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		if c.opts.Debug {
			slog.Debug("ism7 <", "type", typ, "payload", string(payload))
		}
		if len(payload) == 0 {
			slog.Warn("received empty message", "type", typ)
			continue
		}
		switch typ {
		case TypeKeepAlive:
			if len(payload) >= 2 {
				c.lastKA.Store(int32(int16(binary.BigEndian.Uint16(payload))))
			}
		case TypeLoginResp:
			var r LoginResp
			if err := xml.Unmarshal(payload, &r); err != nil {
				return fmt.Errorf("decode login response: %w", err)
			}
			select {
			case c.login <- &r:
			default:
			}
		case TypeSystemconfigResp:
			var r SystemconfigResp
			if err := xml.Unmarshal(payload, &r); err != nil {
				return fmt.Errorf("decode system config: %w", err)
			}
			select {
			case c.sysconf <- &r:
			default:
			}
		case TypeBundleResp:
			var r BundleResp
			if err := xml.Unmarshal(payload, &r); err != nil {
				return fmt.Errorf("decode bundle response: %w", err)
			}
			c.hmu.Lock()
			fn := c.handlers[r.BundleID]
			c.hmu.Unlock()
			if fn != nil {
				fn(&r)
			}
		default:
			slog.Debug("ignoring message", "type", typ)
		}
	}
}
