package gp

import (
	"github.com/spf13/cobra"
	"github.com/wal-g/wal-g/internal/databases/greenplum"
)

var (
	port   string
	segnum string
)

// deleteCmd represents the delete command
var checkCmd = &cobra.Command{
	Use: "domagic",
	Run: func(cmd *cobra.Command, args []string) {
		greenplum.CheckWTF(port, segnum)
	},
}

func init() {
	checkCmd.PersistentFlags().StringVarP(&port, "port", "p", "5432", `database port (default: "5432")`)
	checkCmd.PersistentFlags().StringVarP(&segnum, "segnum", "s", "", `database segment number`)

	cmd.AddCommand(checkCmd)
}
