package aof

import (
	"fmt"

	"github.com/wal-g/wal-g/internal"
	"github.com/wal-g/wal-g/internal/databases/redis/archive"
	"github.com/wal-g/wal-g/pkg/storages/storage"
)

func DownloadSentinel(folder storage.Folder, backupName string) (*archive.Backup, error) {
	var sentinel archive.Backup
	backup, err := internal.GetBackupByName(backupName, "", folder)
	if err != nil {
		return nil, err
	}
	if err := backup.FetchSentinel(&sentinel); err != nil {
		return nil, err
	}
	if sentinel.BackupName == "" {
		sentinel.BackupName = backupName
	}
	if sentinel.BackupType == "" {
		sentinel.BackupType = archive.RDBBackupType
	}
	if sentinel.Version == "" {
		return nil, fmt.Errorf("expecting sentinel file for aof backup with always filled version: %+v", sentinel)
	}
	return &sentinel, nil
}
