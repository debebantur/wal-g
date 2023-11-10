package greenplum

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/blang/semver"
	"github.com/greenplum-db/gp-common-go-libs/cluster"
	"github.com/jackc/pgx"
	"github.com/wal-g/tracelog"
	"github.com/wal-g/wal-g/internal/databases/postgres"
)

func /*(some handler)*/ CheckWT4F() {

	conn, err := postgres.Connect()
	if err != nil {
		tracelog.ErrorLogger.FatalfOnError("unable to get connection %v", err)
	}

	globalCluster, _, _, err := getGpClusterInf(conn)
	if err != nil {
		tracelog.ErrorLogger.FatalfOnError("wtf %v", err)
	}

	remoteOutput := globalCluster.GenerateAndExecuteCommand("Testing command",
		cluster.ON_SEGMENTS,
		func(contentID int) string {
			return buildBackupPushCommand(contentID, globalCluster)
		})
	globalCluster.CheckClusterError(remoteOutput, "Unable to run wal-g", func(contentID int) string {
		return "Unable to run wal-g"
	}, true)

	for _, command := range remoteOutput.Commands {
		if command.Stderr != "" {
			tracelog.ErrorLogger.Printf("stderr (segment %d):\n%s\n", command.Content, command.Stderr)
		}
	}

}

func buildBackupPushCommand(contentID int, globalCluster *cluster.Cluster) string {
	segment := globalCluster.ByContent[contentID][0]

	backupPushArgs := []string{
		fmt.Sprintf("--port=%d", segment.Port),
		fmt.Sprintf("--segnum=%d", segment.ContentID),
	}

	backupPushArgsLine := "'" + strings.Join(backupPushArgs, " ") + "'"

	cmd := []string{
		// nohup to avoid the SIGHUP on SSH session disconnect
		"nohup", "wal-g domagic",
		// actual arguments to be passed to the backup-push command
		backupPushArgsLine,
		// forward stdout and stderr to the log file
		"&>>", formatSegmentLogPath(contentID),
		// run in the background and get the launched process PID
		"& echo $!",
	}

	cmdLine := strings.Join(cmd, " ")
	tracelog.InfoLogger.Printf("Command to run on segment %d: %s", contentID, cmdLine)
	return cmdLine
}

func getGpClusterInf(conn *pgx.Conn) (globalCluster *cluster.Cluster, version semver.Version, systemIdentifier *uint64, err error) {
	queryRunner, err := NewGpQueryRunner(conn)
	if err != nil {
		return globalCluster, semver.Version{}, nil, err
	}

	versionStr, err := queryRunner.GetGreenplumVersion()
	if err != nil {
		return globalCluster, semver.Version{}, nil, err
	}
	tracelog.InfoLogger.Printf("Greenplum version: %s", versionStr)
	versionStart := strings.Index(versionStr, "(Greenplum Database ") + len("(Greenplum Database ")
	versionEnd := strings.Index(versionStr, ")")
	versionStr = versionStr[versionStart:versionEnd]
	pattern := regexp.MustCompile(`\d+\.\d+\.\d+`)
	threeDigitVersion := pattern.FindStringSubmatch(versionStr)[0]
	semVer, err := semver.Make(threeDigitVersion)
	if err != nil {
		return globalCluster, semver.Version{}, nil, err
	}

	segConfigs, err := queryRunner.GetGreenplumSegmentsInfo(semVer)
	if err != nil {
		return globalCluster, semver.Version{}, nil, err
	}
	globalCluster = cluster.NewCluster(segConfigs)

	return globalCluster, semVer, queryRunner.SystemIdentifier, nil
}
