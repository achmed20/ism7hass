package cmd

import (
	"context"
	"errors"
	"log/slog"

	"ism7/internal/ism7"

	"github.com/spf13/cobra"
)

var rebootCmd = &cobra.Command{
	Use:   "reboot",
	Short: "Reboot the ISM7 gateway",
	RunE: func(cmd *cobra.Command, args []string) error {
		if config.ISM7.IP == "" || config.ISM7.Password == "" {
			return errors.New("ism7.ip and ism7.password are required")
		}
		if err := ism7.Reboot(context.Background(), "http://"+config.ISM7.IP, config.ISM7.Password); err != nil {
			return err
		}
		slog.Info("reboot requested")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(rebootCmd)
	rebootCmd.Flags().StringP("ip", "i", "", "ISM7 hostname or IP address")
	rebootCmd.Flags().StringP("password", "p", "", "ISM7 password")
}
