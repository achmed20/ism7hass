package cmd

import (
	"encoding/json"
	"fmt"

	"ism7/version"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version",
	Run: func(cmd *cobra.Command, args []string) {
		b, _ := json.MarshalIndent(version.GetVersion(), "", "  ")
		fmt.Println(string(b))
	},
}

func init() { rootCmd.AddCommand(versionCmd) }
