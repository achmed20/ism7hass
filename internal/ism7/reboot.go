package ism7

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Reboot restarts the gateway through its web UI (plain HTTP, basic auth, fixed user "admin").
// base is "http://<host>" (host may include a port).
func Reboot(ctx context.Context, base, password string) error {
	form := url.Values{"name": {"reboKickoff"}, "rebo": {"init"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/protect/reboot.htm", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.SetBasicAuth("admin", password)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		// the device may drop the connection while going down
		if errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) {
			return nil
		}
		return fmt.Errorf("reboot request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("reboot request: unexpected status %s", resp.Status)
	}
	return nil
}
