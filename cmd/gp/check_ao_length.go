package gp

import (
	"github.com/spf13/cobra"
	"github.com/wal-g/wal-g/internal/databases/greenplum"
)

var (
	logsDir string
)

// deleteCmd represents the delete command
var checkMasterCmd = &cobra.Command{
	Use: "check_it",
	Run: func(cmd *cobra.Command, args []string) {
		greenplum.CheckWT4F(logsDir)
	},
}

func init() {
	checkCmd.PersistentFlags().StringVarP(&logsDir, "logs", "l", "/mylogs", `TODO`)

	cmd.AddCommand(checkMasterCmd)
}
