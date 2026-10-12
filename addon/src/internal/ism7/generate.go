package ism7

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// GenerateConfigFile reads the system config from the ISM7 (port 9092, then 9091 for firmware < 200)
// and writes parameter.json atomically (temp file + rename).
func GenerateConfigFile(ctx context.Context, t *Templates, host, password, path string, enabled bool, ports ...uint16) (*ConfigFile, error) {
	if len(ports) == 0 {
		ports = []uint16{9092, 9091}
	}
	var resp *SystemconfigResp
	var port uint16
	var errs []error
	for _, p := range ports {
		cl := NewClient(Options{Host: host, Port: p, Password: password, StartupTimeout: 120 * time.Second}, nil)
		r, err := cl.ReadSystemConfig(ctx)
		if err == nil {
			resp, port = r, p
			break
		}
		errs = append(errs, fmt.Errorf("port %d: %w", p, err))
	}
	if resp == nil {
		return nil, fmt.Errorf("could not read system config: %w", errors.Join(errs...))
	}
	cf, skipped := t.BuildConfigFile(resp.Devices, port, enabled)
	for _, s := range skipped {
		slog.Warn("no device template found, skipping", "device", s)
	}
	b, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".parameter-*.json")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return nil, err
	}
	slog.Info("parameter file written", "file", path, "devices", len(cf.Devices))
	return cf, nil
}
