package cmd

import (
	"context"
	"errors"
	"os"
	"os/signal"

	"ism7/internal/ism7"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Generate parameter.json from the devices found on the ISM7",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := config
		if c.ISM7.IP == "" || c.ISM7.Password == "" {
			return errors.New("ism7.ip and ism7.password are required")
		}
		tpl, err := ism7.LoadTemplates(c.ISM7.ParameterXMLOverride)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()

		_, err = ism7.GenerateConfigFile(ctx, tpl, c.ISM7.IP, c.ISM7.Password, c.ISM7.Parameter, viper.GetBool("config.enabled"))
		return err
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	f := configCmd.Flags()
	f.StringP("ip", "i", "", "ISM7 hostname or IP address")
	f.StringP("password", "p", "", "ISM7 password")
	f.Bool("enabled", true, "mark all parameters as enabled (use --enabled=false to disable all, then enable the ones you want)")
	f.StringP("target", "t", "", "target file (default parameter.json)")
}
